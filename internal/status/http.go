package status

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Handler struct {
	Store    *Store
	Now      func() time.Time
	mu       sync.Mutex
	limits   map[string]window
	cacheMu  sync.Mutex
	cached   *Snapshot
	cachedAt time.Time
}
type window struct {
	minute int64
	count  int
}

func NewHandler(s *Store) *Handler {
	return &Handler{Store: s, Now: time.Now, limits: map[string]window{}}
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
func (h *Handler) snapshot() (Snapshot, error) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	now := h.Now()
	if h.cached != nil && now.Sub(h.cachedAt) >= 0 && now.Sub(h.cachedAt) < 2*time.Second {
		return *h.cached, nil
	}
	value, err := h.Store.Snapshot(now)
	if err == nil {
		h.cached = &value
		h.cachedAt = now
	}
	return value, err
}
func (h *Handler) invalidate() { h.cacheMu.Lock(); h.cached = nil; h.cacheMu.Unlock() }
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
		snapshot, err := h.snapshot()
		if err != nil {
			problem(w, 503, "data unavailable")
			return
		}
		jsonResponse(w, 200, snapshot)
	case "/api/v1/history":
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
		points, err := h.Store.History(id, h.Now(), hours)
		if err != nil {
			problem(w, 503, "data unavailable")
			return
		}
		jsonResponse(w, 200, map[string]any{"serviceId": id, "rangeHours": hours, "points": points})
	case "/api/v1/incidents":
		if r.Method != "GET" {
			problem(w, 405, "method not allowed")
			return
		}
		events, err := h.Store.Incidents()
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
