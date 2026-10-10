package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
)

// A volatile, per-agent tracker: cumulative startup counters cannot turn a fresh
// collector/daemon restart green. It reads existing control traffic; never pings.
type controlTracker struct {
	mu     sync.Mutex
	states map[string]controlState
}
type controlState struct {
	identity string
	count    uint64
	readAt   float64
	proofAt  float64
	proofID  string
	wallAt   string
}
type controlFrame struct {
	identity  string
	uptime    float64
	readStart float64
	counter   uint64
	active    bool
	warning   bool
	now       time.Time
}

func controlIdentity(raw []byte, boot string, uptime float64) string {
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	stamp, err := strconv.ParseUint(values["ExecMainStartTimestampMonotonic"], 10, 64)
	if boot == "" || err != nil || stamp == 0 || float64(stamp)/1e6 > uptime || values["LoadState"] != "loaded" || values["ActiveState"] != "active" {
		return ""
	}
	return boot + ":" + values["ExecMainStartTimestampMonotonic"]
}

func controlMetrics(raw []byte) (uint64, bool, bool) {
	if len(raw) > 1024*1024 {
		return 0, false, false
	}
	var count uint64
	var active float64
	seenCount, seenActive := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		name := strings.SplitN(fields[0], "{", 2)[0]
		if name != "controlclient_map_response_message" && name != "controlclient_map_requests_active" {
			continue
		}
		if len(fields) != 2 || fields[0] != name {
			return 0, false, false
		}
		if name == "controlclient_map_response_message" {
			var err error
			count, err = strconv.ParseUint(fields[1], 10, 53)
			if err != nil || seenCount {
				return 0, false, false
			}
			seenCount = true
		} else {
			var err error
			active, err = strconv.ParseFloat(fields[1], 64)
			if err != nil || seenActive || math.IsNaN(active) || math.IsInf(active, 0) || active < 0 || active > 4 || math.Trunc(active) != active {
				return 0, false, false
			}
			seenActive = true
		}
	}
	return count, active > 0, seenCount && seenActive
}

func (t *controlTracker) reset(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, id)
}
func (t *controlTracker) probe(ctx context.Context, c Check) status.Observation {
	return t.probeWith(ctx, c, readCommand, clock, time.Now)
}

func (t *controlTracker) probeWith(ctx context.Context, c Check, read func(context.Context, string, ...string) ([]byte, error), sourceClock func() (string, float64), now func() time.Time) status.Observation {
	if validateAdapter(c) != nil {
		t.reset(c.ServiceID + "|" + c.Unit)
		return unknown("control_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	bootStart, readStart := sourceClock()
	readIdentity := func() string {
		raw, err := read(ctx, "systemctl", "show", "--no-pager", c.Unit, "--property=LoadState,ActiveState,ExecMainStartTimestampMonotonic")
		if err != nil {
			return ""
		}
		boot, uptime := sourceClock()
		return controlIdentity(raw, boot, uptime)
	}
	identity := readIdentity()
	unavailable := func() status.Observation { t.reset(c.ServiceID + "|" + c.Unit); return unknown("control_unavailable") }
	if identity == "" {
		return unavailable()
	}
	// Fixed read-only CLI operations use Tailscale's existing LocalAPI transport.
	// No login, reconnect, up/down, config changes, or active peer request.
	raw, err := read(ctx, "tailscale", "status", "--json")
	if err != nil {
		return unavailable()
	}
	var state struct {
		BackendState string
		Health       []string
		Self         struct{ Online bool }
	}
	if json.Unmarshal(raw, &state) != nil || state.BackendState != "Running" || !state.Self.Online {
		return unavailable()
	}
	raw, err = read(ctx, "tailscale", "debug", "metrics")
	if err != nil {
		return unavailable()
	}
	count, active, ok := controlMetrics(raw)
	if !ok || readIdentity() != identity || ctx.Err() != nil {
		return unavailable()
	}
	bootEnd, uptime := sourceClock()
	if bootEnd != bootStart {
		return unavailable()
	}
	return t.observe(c, controlFrame{identity: identity, uptime: uptime, readStart: readStart, counter: count, active: active, warning: len(state.Health) > 0, now: now()})
}

func (t *controlTracker) observe(c Check, f controlFrame) status.Observation {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.states == nil {
		t.states = map[string]controlState{}
	}
	key := c.ServiceID + "|" + c.Unit
	previous, exists := t.states[key]
	maxAge := float64(c.MaxAgeSeconds)
	if maxAge < 20 || maxAge > 180 || f.identity == "" || math.IsNaN(f.uptime) || math.IsInf(f.uptime, 0) || f.uptime < 0 || math.IsNaN(f.readStart) || math.IsInf(f.readStart, 0) || f.readStart < 0 || f.readStart > f.uptime || f.uptime-f.readStart >= maxAge || !f.active {
		delete(t.states, key)
		return unknown("control_unavailable")
	}
	if !exists || previous.identity != f.identity || f.counter < previous.count || f.readStart < previous.readAt || f.uptime < previous.readAt || f.uptime-previous.readAt >= maxAge {
		t.states[key] = controlState{identity: f.identity, count: f.counter, readAt: f.readStart}
		return unknown("control_wait")
	}
	if f.counter > previous.count {
		// A new message arrived between reads. Use the OLDEST possible instant,
		// not the current poll time, so a long polling gap cannot refresh evidence.
		previous.proofAt = previous.readAt
		previous.wallAt = f.now.Add(-time.Duration((f.uptime - previous.readAt) * float64(time.Second))).UTC().Format(time.RFC3339Nano)
		hash := sha256.Sum256([]byte(key + "|" + f.identity + "|" + strconv.FormatUint(f.counter, 10)))
		previous.proofID = hex.EncodeToString(hash[:16])
	}
	previous.count, previous.readAt = f.counter, f.readStart
	t.states[key] = previous
	if previous.proofID == "" {
		return unknown("control_wait")
	}
	age := f.uptime - previous.proofAt
	var evidence *status.Evidence
	if at, err := time.Parse(time.RFC3339Nano, previous.wallAt); err == nil && at.Year() >= 2000 && at.Year() <= 2100 {
		evidence = &status.Evidence{Kind: "control_message", At: previous.wallAt, Historical: age >= maxAge}
	}
	if age >= maxAge {
		o := unknown("control_stale")
		o.Evidence = evidence
		return o
	}
	o := healthy()
	o.Code, o.ID, o.CapturedAt, o.AgeSeconds, o.Evidence = "control_fresh", previous.proofID, previous.wallAt, &age, evidence
	checkedAge := 0.0
	o.CheckedAgeSeconds = &checkedAge
	if f.warning {
		o.Status, o.Code = "degraded", "control_warning"
		// Same message evidence, changed local health classification; its original
		// age still applies and repeated reads never manufacture a new message.
		hash := sha256.Sum256([]byte(previous.proofID + "|warning"))
		o.ID = hex.EncodeToString(hash[:16])
	}
	return o
}
