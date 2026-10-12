package status

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Handler struct {
	Store         *Store
	Now           func() time.Time
	mu            sync.Mutex
	limits        map[string]window
	cacheMu       sync.Mutex
	cached        *Snapshot
	cachedAt      time.Time
	generation    uint64
	flight        *snapshotFlight
	buildSnapshot func(context.Context, time.Time) (Snapshot, error)
}

const readBudget = 4 * time.Second

// A client clock may run slightly ahead of the server. Allow a small skew on
// the requested upper bound instead of rejecting an otherwise valid window.
const observationsClockSkew = 5 * time.Second

type snapshotFlight struct {
	done  chan struct{}
	value Snapshot
	err   error
}
type window struct {
	minute int64
	count  int
}

func NewHandler(s *Store) *Handler {
	return &Handler{Store: s, Now: time.Now, limits: map[string]window{}, buildSnapshot: s.SnapshotContext}
}
func (h *Handler) authenticate(r *http.Request) (string, bool) {
	raw := r.Header.Get("Authorization")
	if !strings.HasPrefix(raw, "Bearer ") || len(raw) > 140 {
		return "", false
	}
	digest := sha256.Sum256([]byte(strings.TrimPrefix(raw, "Bearer ")))
	for _, a := range h.Store.Config.Agents {
		if subtle.ConstantTimeCompare(digest[:], a.Digest[:]) == 1 {
			return a.ID, true
		}
	}
	return "", false
}
func (h *Handler) allowed(agent string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	minute := h.Now().Unix() / 60
	v := h.limits[agent]
	if v.minute != minute {
		v = window{minute: minute}
	}
	v.count++
	h.limits[agent] = v
	return v.count <= 120
}
func jsonResponse(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, code int, message string) {
	jsonResponse(w, code, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		problem(w, 400, "invalid request")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		problem(w, 400, "invalid request")
		return false
	}
	return true
}

// One bounded shared build per cold miss; no database computation under the
// cache mutex. Cancelled browsers stop waiting, and ingestion never waits for
// history computation merely to invalidate the cache.
func (h *Handler) snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	h.cacheMu.Lock()
	now := h.Now()
	if h.cached != nil && now.Sub(h.cachedAt) >= 0 && now.Sub(h.cachedAt) < 2*time.Second {
		value := *h.cached
		h.cacheMu.Unlock()
		return ageSnapshot(value, now), nil
	}
	f := h.flight
	if f == nil {
		f = &snapshotFlight{done: make(chan struct{})}
		h.flight = f
		generation := h.generation
		go func() {
			buildCtx, cancel := context.WithTimeout(context.Background(), readBudget)
			defer cancel()
			value, err := h.buildSnapshot(buildCtx, now)
			h.cacheMu.Lock()
			if err == nil && generation == h.generation {
				h.cached = &value
				h.cachedAt = h.Now()
			}
			f.value, f.err = value, err
			h.flight = nil
			close(f.done)
			h.cacheMu.Unlock()
		}()
	}
	h.cacheMu.Unlock()
	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-f.done:
		if f.err != nil {
			return Snapshot{}, f.err
		}
		return ageSnapshot(f.value, h.Now()), nil
	}
}
func (h *Handler) invalidate() {
	h.cacheMu.Lock()
	h.generation++
	h.cached = nil
	h.cacheMu.Unlock()
}

// A cached computation must never keep a source or observation green past its
// independent deadline. Return a copy aged at response time, not a stale cache
// timestamp; histories/evidence remain immutable and cannot renew themselves.
func ageSnapshot(value Snapshot, now time.Time) Snapshot {
	value.ServerTime = iso(now.UnixMilli())
	value.Collectors = append([]Collector(nil), value.Collectors...)
	byAgent := map[string]Collector{}
	for i := range value.Collectors {
		c := &value.Collectors[i]
		c.Stale = true
		if c.LastHeartbeatAt != nil {
			if stamp, err := time.Parse(time.RFC3339Nano, *c.LastHeartbeatAt); err == nil {
				age := max(0, now.Sub(stamp).Seconds())
				c.HeartbeatAgeSeconds = number(age)
				c.Stale = age >= float64(c.StaleAfterSeconds)
			}
		}
		byAgent[c.ID] = *c
	}
	if c, ok := byAgent[value.Collector.ID]; ok {
		value.Collector = c
	}
	value.Services = append([]Service(nil), value.Services...)
	for i := range value.Services {
		s := &value.Services[i]
		c := byAgent[s.CollectorID]
		expired := true
		if s.ObservedAt != nil {
			if stamp, err := time.Parse(time.RFC3339Nano, *s.ObservedAt); err == nil {
				age := max(0, now.Sub(stamp).Seconds())
				s.ObservedAgeSeconds = number(age)
				expired = age >= float64(s.StaleAfterSeconds)
			}
		}
		if s.ID == "collectors" {
			s.ObservedAt = c.LastHeartbeatAt
			s.ObservedAgeSeconds = c.HeartbeatAgeSeconds
			s.Status = "healthy"
			if c.Stale {
				s.Status = "unknown"
			}
			continue
		}
		if expired || c.Stale {
			if s.Status != "untested" {
				s.Status = "unknown"
			}
			s.Summary = summaries["stale"]
			s.LatencyMS = nil
			s.PathMode = "unknown"
			s.ReadyConnections = nil
			s.UsagePercent = nil
		}
	}
	return value
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// No CORS wildcard: browsers use the frontend's same-origin proxy.
	switch r.URL.Path {
	case "/healthz":
		if r.Method != "GET" {
			problem(w, 405, "method not allowed")
			return
		}
		if h.Store.db.PingContext(r.Context()) != nil {
			problem(w, 503, "storage unavailable")
			return
		}
		jsonResponse(w, 200, map[string]string{"status": "ok"})
	case "/api/v1/status":
		if r.Method != "GET" {
			problem(w, 405, "method not allowed")
			return
		}
		snapshot, err := h.snapshot(r.Context())
		if err != nil {
			problem(w, 503, "data unavailable")
			return
		}
		jsonResponse(w, 200, snapshot)
	case "/api/v1/history":
		// The alias reuses the existing protected edge read route.
		if r.URL.Query().Get("resolution") == "raw" {
			h.handleObservations(w, r)
			return
		}
		if r.URL.Query().Get("resolution") != "" {
			problem(w, 400, "invalid resolution")
			return
		}
		if r.Method != "GET" {
			problem(w, 405, "method not allowed")
			return
		}
		id := r.URL.Query().Get("service")
		found := false
		for _, d := range h.Store.Config.Services {
			if d.ID == id {
				found = true
			}
		}
		hours := 24
		if r.URL.Query().Get("range") == "7d" {
			hours = 168
		} else if r.URL.Query().Get("range") != "" && r.URL.Query().Get("range") != "24h" {
			problem(w, 400, "invalid range")
			return
		}
		if !found {
			problem(w, 404, "service not found")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), readBudget)
		defer cancel()
		points, err := h.Store.HistoryContext(ctx, id, h.Now(), hours)
		if err != nil {
			problem(w, 503, "data unavailable")
			return
		}
		jsonResponse(w, 200, map[string]any{"serviceId": id, "rangeHours": hours, "points": points})
	case "/api/v1/observations":
		h.handleObservations(w, r)
	case "/api/v1/incidents":
		if r.Method != "GET" {
			problem(w, 405, "method not allowed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), readBudget)
		defer cancel()
		events, err := h.Store.incidentsContext(ctx)
		if err != nil {
			problem(w, 503, "data unavailable")
			return
		}
		jsonResponse(w, 200, events)
	case "/api/v1/ingest", "/api/v1/heartbeat":
		if r.Method != "POST" {
			problem(w, 405, "method not allowed")
			return
		}
		agent, ok := h.authenticate(r)
		if !ok {
			problem(w, 401, "authentication required")
			return
		}
		if !h.allowed(agent) {
			problem(w, 429, "rate limit exceeded")
			return
		}
		if r.URL.Path == "/api/v1/heartbeat" {
			var body Heartbeat
			if !decode(w, r, &body) {
				return
			}
			if !identifier.MatchString(body.ID) {
				problem(w, 400, "invalid heartbeat")
				return
			}
			if h.Store.Heartbeat(agent, body.ID, h.Now()) != nil {
				problem(w, 503, "storage unavailable")
				return
			}
		} else {
			var body Batch
			if !decode(w, r, &body) {
				return
			}
			if validateBatch(body, h.Store.Config, agent) != nil {
				problem(w, 400, "invalid batch")
				return
			}
			if h.Store.Ingest(agent, body, h.Now()) != nil {
				problem(w, 503, "storage unavailable")
				return
			}
		}
		h.invalidate()
		jsonResponse(w, 200, map[string]bool{"accepted": true})
	default:
		problem(w, 404, "not found")
	}
}

// handleObservations serves one configured service's raw persisted samples for a
// bounded RFC3339 window (default last hour, maximum seven days). It follows the
// same access contract as the other read endpoints (GET only, no application
// Bearer credential; the private listener / edge access policy gates it) and
// shares their read-budget and cancellation behaviour.
func (h *Handler) handleObservations(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		problem(w, 405, "method not allowed")
		return
	}
	query := r.URL.Query()
	id := query.Get("service")
	found := false
	for _, d := range h.Store.Config.Services {
		if d.ID == id {
			found = true
		}
	}
	if !found {
		problem(w, 404, "service not found")
		return
	}
	now := h.Now()
	to := now
	if value := query.Get("to"); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			problem(w, 400, "invalid to")
			return
		}
		to = parsed
	}
	from := to.Add(-time.Hour)
	if value := query.Get("from"); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			problem(w, 400, "invalid from")
			return
		}
		from = parsed
	}
	if to.After(now.Add(observationsClockSkew)) || from.After(to) || to.Sub(from) > 7*24*time.Hour {
		problem(w, 400, "invalid range")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), readBudget)
	defer cancel()
	page, err := h.Store.ObservationsContext(ctx, id, from, to, observationsMaxLimit, query.Get("cursor"))
	if errors.Is(err, errInvalidObservationCursor) {
		problem(w, 400, "invalid cursor")
		return
	}
	if err != nil {
		problem(w, 503, "data unavailable")
		return
	}
	jsonResponse(w, 200, ObservationResponse{
		ServiceID:    id,
		From:         from.UTC().Format(time.RFC3339Nano),
		To:           to.UTC().Format(time.RFC3339Nano),
		Observations: page.Records,
		NextCursor:   page.NextCursor,
		Truncated:    page.Truncated,
	})
}

func ValidateListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) {
		return &net.AddrError{Err: "backend must bind a loopback or private interface", Addr: "redacted"}
	}
	return nil
}
