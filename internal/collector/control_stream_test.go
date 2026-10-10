package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func controlCheck() Check {
	return Check{ServiceID: "control", Kind: "control-stream", Unit: "tailscaled.service", MaxAgeSeconds: 90}
}
func controlFixture(at float64, count uint64) controlFrame {
	return controlFrame{identity: "private-boot:100", uptime: at, readStart: at, counter: count, active: true, now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(at) * time.Second)}
}
func TestControlRequiresGrowthAndNeverRenewsOldProof(t *testing.T) {
	var tracker controlTracker
	c := controlCheck()
	if o := tracker.observe(c, controlFixture(100, 1000)); o.Status != "unknown" || o.Evidence != nil {
		t.Fatal("nonzero counter promoted", o)
	}
	if o := tracker.observe(c, controlFixture(110, 1000)); o.Status != "unknown" {
		t.Fatal(o)
	}
	first := tracker.observe(c, controlFixture(120, 1001))
	if first.Status != "healthy" || first.Code != "control_fresh" || *first.AgeSeconds != 10 || first.Evidence == nil || first.LatencyMS != nil || first.PathMode != "unknown" {
		t.Fatal(first)
	}
	again := tracker.observe(c, controlFixture(140, 1001))
	if first.ID != again.ID || first.CapturedAt != again.CapturedAt || first.Evidence.At != again.Evidence.At || *again.AgeSeconds != 30 {
		t.Fatal("reread renewed evidence", first, again)
	}
	for _, at := range []float64{160, 180} {
		tracker.observe(c, controlFixture(at, 1001))
	}
	stale := tracker.observe(c, controlFixture(200, 1001))
	if stale.Status != "unknown" || stale.Code != "control_stale" || stale.Evidence == nil || !stale.Evidence.Historical || stale.ID != "" || stale.LatencyMS != nil {
		t.Fatal(stale)
	}
	fresh := tracker.observe(c, controlFixture(210, 1002))
	if fresh.Status != "healthy" || fresh.ID == first.ID || *fresh.AgeSeconds != 10 {
		t.Fatal(fresh)
	}
	raw, _ := json.Marshal(fresh)
	if strings.Contains(string(raw), "private-boot") || strings.Contains(string(raw), "controlclient_map_response_message") {
		t.Fatal("identity/counter leaked", string(raw))
	}
}
func TestControlResetAndLongGapNeedNewBaseline(t *testing.T) {
	for _, mode := range []string{"daemon", "counter", "uptime", "gap", "inactive", "read_error", "collector"} {
		t.Run(mode, func(t *testing.T) {
			var tracker controlTracker
			c := controlCheck()
			tracker.observe(c, controlFixture(100, 20))
			tracker.observe(c, controlFixture(110, 21))
			f := controlFixture(120, 22)
			switch mode {
			case "daemon":
				f.identity = "new-private-boot:200"
			case "counter":
				f.counter = 1
			case "uptime":
				f.uptime, f.readStart = 90, 90
			case "gap":
				f.uptime, f.readStart = 250, 250
			case "inactive":
				f.active = false
			case "read_error":
				tracker.reset(c.ServiceID + "|" + c.Unit)
			case "collector":
				tracker = controlTracker{}
			}
			if o := tracker.observe(c, f); o.Status != "unknown" || o.Evidence != nil {
				t.Fatal("reset fabricated growth", o)
			}
		})
	}
}
func TestControlUsesReadStartAndWarningScope(t *testing.T) {
	var tracker controlTracker
	c := controlCheck()
	first := controlFixture(100, 5)
	first.readStart = 95
	tracker.observe(c, first)
	next := controlFixture(120, 6)
	next.readStart, next.warning = 115, true
	o := tracker.observe(c, next)
	if o.Status != "degraded" || o.Code != "control_warning" || *o.AgeSeconds != 25 {
		t.Fatal("latency shortened evidence age", o)
	}
	for _, invalid := range []float64{math.NaN(), math.Inf(1), -1} {
		f := controlFixture(130, 7)
		f.readStart = invalid
		if o := tracker.observe(c, f); o.Status != "unknown" {
			t.Fatal(o)
		}
	}
}
func TestControlStrictCounterParsingAndValidation(t *testing.T) {
	base := "controlclient_map_response_message 100000\ncontrolclient_map_requests_active 1\nprivate_metric{private_label=\"hidden\"} 9\n"
	count, active, ok := controlMetrics([]byte(base))
	if !ok || !active || count != 100000 {
		t.Fatal(count, active, ok)
	}
	for _, raw := range []string{
		"", strings.Replace(base, "100000", "NaN", 1), strings.Replace(base, "100000", "-1", 1),
		strings.Replace(base, "100000", "1.5", 1), strings.Replace(base, "100000", "9007199254740992", 1),
		strings.Replace(base, "active 1", "active NaN", 1), strings.Replace(base, "active 1", "active +Inf", 1),
		strings.Replace(base, "active 1", "active 1.5", 1), strings.Replace(base, "active 1", "active 5", 1),
		base + "controlclient_map_response_message 1\n", strings.Replace(base, "message 100000", "message{label=\"x\"} 100000", 1),
	} {
		if _, _, ok := controlMetrics([]byte(raw)); ok {
			t.Fatal("invalid metrics accepted", raw)
		}
	}
	if validateAdapter(controlCheck()) != nil {
		t.Fatal("valid passive adapter rejected")
	}
	for _, c := range []Check{{Kind: "control-stream", Unit: "--help", MaxAgeSeconds: 90}, {Kind: "control-stream", Unit: "tailscaled.service", MaxAgeSeconds: 181}, {Kind: "control-stream", Unit: "tailscaled.service", MaxAgeSeconds: 90, URL: "http://127.0.0.1"}, {Kind: "unit", Unit: "test.service", JournalHistory: true}} {
		if validateAdapter(c) == nil {
			t.Fatal("unsafe check accepted", c)
		}
	}
}
func TestControlReadOnlyCommandsAndFailureReset(t *testing.T) {
	var tracker controlTracker
	c := controlCheck()
	tick, counter := float64(100), uint64(20)
	fail, restart := false, false
	unitReads := 0
	read := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 6*time.Second {
			t.Fatal("missing shared budget")
		}
		switch name + " " + strings.Join(args, " ") {
		case "systemctl show --no-pager tailscaled.service --property=LoadState,ActiveState,ExecMainStartTimestampMonotonic":
			unitReads++
			stamp := "1000000"
			if restart && unitReads%2 == 0 {
				stamp = "2000000"
			}
			return []byte("LoadState=loaded\nActiveState=active\nExecMainStartTimestampMonotonic=" + stamp + "\n"), nil
		case "tailscale status --json":
			if fail {
				return nil, errors.New("read failure")
			}
			return []byte(`{"BackendState":"Running","Self":{"Online":true},"Peer":{"private-name":{"TailscaleIPs":["private-address"]}}}`), nil
		case "tailscale debug metrics":
			return []byte(fmt.Sprintf("controlclient_map_response_message %d\ncontrolclient_map_requests_active 1\n", counter)), nil
		default:
			t.Fatalf("non-read-only invocation: %s %v", name, args)
			return nil, nil
		}
	}
	clock := func() (string, float64) { return "private-boot", tick }
	now := func() time.Time { return controlFixture(tick, counter).now }
	probe := func() string { return tracker.probeWith(context.Background(), c, read, clock, now).Status }
	if probe() != "unknown" {
		t.Fatal("startup green")
	}
	tick, counter = 110, 21
	if probe() != "healthy" {
		t.Fatal("growth not accepted")
	}
	fail = true
	if probe() != "unknown" {
		t.Fatal("error green")
	}
	fail = false
	tick = 120
	if probe() != "unknown" {
		t.Fatal("old proof survived read error")
	}
	restart = true
	tick, counter = 130, 22
	if probe() != "unknown" {
		t.Fatal("mixed daemon snapshots accepted")
	}
}
