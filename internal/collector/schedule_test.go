package collector

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// jitter returns a deterministic value in [-10ms, +10ms] derived from the
// round number, so the cadence test is reproducible without randomness.
func jitter(round int) time.Duration {
	return time.Duration((round*7)%21-10) * time.Millisecond
}

func TestScheduleDueThirtySecondCadenceWithJitter(t *testing.T) {
	const interval = 30 * time.Second
	start := time.Now()
	due, next := scheduleDue(time.Time{}, start, interval)
	if !due || !next.Equal(start.Add(interval)) {
		t.Fatalf("first run: due=%v next=%v", due, next)
	}
	prev := next
	for round := 1; round <= 20; round++ {
		now := start.Add(time.Duration(round) * interval).Add(jitter(round))
		due, next = scheduleDue(prev, now, interval)
		if !due {
			t.Fatalf("round %d skipped (jitter %v)", round, jitter(round))
		}
		want := start.Add(time.Duration(round+1) * interval)
		if !next.Equal(want) {
			t.Fatalf("round %d: next=%v want stable phase %v", round, next, want)
		}
		prev = next
	}
}

func TestScheduleDueSixtySecondCheckNotEveryThirty(t *testing.T) {
	const interval = 60 * time.Second
	start := time.Now()
	prev := start.Add(interval)
	due, next := scheduleDue(prev, start.Add(30*time.Second), interval)
	if due || !next.Equal(prev) {
		t.Fatalf("60s check fired on the 30s tick: due=%v next=%v", due, next)
	}
	due, next = scheduleDue(prev, start.Add(60*time.Second), interval)
	if !due || !next.Equal(start.Add(120*time.Second)) {
		t.Fatalf("60s check skipped its own period: due=%v next=%v", due, next)
	}
}

func TestScheduleDueMissedTicksAdvanceOnce(t *testing.T) {
	const interval = 30 * time.Second
	start := time.Now()
	prev := start.Add(interval)
	now := prev.Add(95 * time.Second) // roughly three missed periods
	due, next := scheduleDue(prev, now, interval)
	if !due || !next.Equal(start.Add(150*time.Second)) {
		t.Fatalf("missed ticks: due=%v next=%v", due, next)
	}
	if !next.After(now) {
		t.Fatalf("next deadline %v is not in the future of %v", next, now)
	}
	// Re-evaluating immediately must not replay the missed periods.
	dueAgain, nextAgain := scheduleDue(next, now, interval)
	if dueAgain || !nextAgain.Equal(next) {
		t.Fatalf("catch-up burst: due=%v next=%v", dueAgain, nextAgain)
	}
}

func TestScheduleDueMuchTooEarlyNotDue(t *testing.T) {
	const interval = 30 * time.Second
	prev := time.Now().Add(interval)
	now := prev.Add(-200 * time.Millisecond) // beyond the 100ms slack
	due, next := scheduleDue(prev, now, interval)
	if due || !next.Equal(prev) {
		t.Fatalf("too-early tick was treated as due: due=%v next=%v", due, next)
	}
}

func TestScheduleDueEarlyWithinSlackAdvancesOneInterval(t *testing.T) {
	const interval = 30 * time.Second
	prev := time.Now().Add(interval)
	now := prev.Add(-5 * time.Millisecond)
	due, next := scheduleDue(prev, now, interval)
	if !due {
		t.Fatal("tick just before the deadline was skipped")
	}
	// Advancing by exactly one interval prevents a second probe for the same period.
	if !next.Equal(prev.Add(interval)) {
		t.Fatalf("early advance=%v want %v", next, prev.Add(interval))
	}
}

func TestScheduleDueSlackBound(t *testing.T) {
	// Minimum configurable interval: slack is clamped to 100ms.
	prev := time.Now().Add(10 * time.Second)
	if due, _ := scheduleDue(prev, prev.Add(-150*time.Millisecond), 10*time.Second); due {
		t.Fatal("150ms-early tick accepted for a 10s interval")
	}
	if due, next := scheduleDue(prev, prev.Add(-50*time.Millisecond), 10*time.Second); !due || !next.Equal(prev.Add(10*time.Second)) {
		t.Fatalf("50ms-early tick rejected: due=%v next=%v", due, next)
	}
	// A sub-second interval keeps the interval/10 bound instead of 100ms.
	prev = time.Now().Add(500 * time.Millisecond)
	if due, _ := scheduleDue(prev, prev.Add(-60*time.Millisecond), 500*time.Millisecond); due {
		t.Fatal("60ms-early tick accepted for a 500ms interval")
	}
	if due, _ := scheduleDue(prev, prev.Add(-40*time.Millisecond), 500*time.Millisecond); !due {
		t.Fatal("40ms-early tick rejected for a 500ms interval")
	}
	if _, next := scheduleDue(prev, prev, 0); !next.Equal(prev) {
		t.Fatalf("non-positive interval next=%v", next)
	}
}

func scheduleTestAgent(t *testing.T, checks []Check) *Agent {
	t.Helper()
	dir := t.TempDir()
	secret := filepath.Join(dir, "credential")
	if err := os.WriteFile(secret, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{
		ServerURL:       "http://127.0.0.1:1",
		SecretFile:      secret,
		StateDirectory:  filepath.Join(dir, "queue"),
		IntervalSeconds: 30,
		Checks:          checks,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestCollectRespectsSchedulePhaseAndOverrides exercises the real Collect path
// with network-free "untested" checks.
func TestCollectRespectsSchedulePhaseAndOverrides(t *testing.T) {
	checks := []Check{
		{ServiceID: "default", Kind: "untested"},
		{ServiceID: "slow", Kind: "untested", IntervalSeconds: 60},
		{ServiceID: "future", Kind: "untested"},
	}
	a := scheduleTestAgent(t, checks)
	base := time.Now()
	defaultPrev := base.Add(-time.Millisecond)
	slowPrev := base.Add(-time.Millisecond)
	futurePrev := base.Add(30 * time.Second)
	a.next["default"] = defaultPrev
	a.next["slow"] = slowPrev
	a.next["future"] = futurePrev

	q := a.Collect(context.Background())
	seen := map[string]bool{}
	for _, o := range q.Batch.Samples {
		seen[o.ServiceID] = true
	}
	if !seen["default"] || !seen["slow"] || seen["future"] {
		t.Fatalf("unexpected due set: %v", seen)
	}
	if got := a.next["default"]; !got.Equal(defaultPrev.Add(30 * time.Second)) {
		t.Fatalf("default next=%v", got)
	}
	if got := a.next["slow"]; !got.Equal(slowPrev.Add(60 * time.Second)) {
		t.Fatalf("per-check override next=%v", got)
	}
	if got := a.next["future"]; !got.Equal(futurePrev) {
		t.Fatalf("not-due deadline mutated: %v", got)
	}
}

// TestCollectAbsorbsEarlyTickerJitter reproduces the deployed 60s-round bug: a
// tick arriving just before the stored deadline must still run exactly once and
// keep the original phase.
func TestCollectAbsorbsEarlyTickerJitter(t *testing.T) {
	a := scheduleTestAgent(t, []Check{{ServiceID: "probe", Kind: "untested"}})
	deadline := time.Now().Add(50 * time.Millisecond)
	a.next["probe"] = deadline
	q := a.Collect(context.Background())
	if len(q.Batch.Samples) != 1 {
		t.Fatalf("early deadline skipped: %d samples", len(q.Batch.Samples))
	}
	if got := a.next["probe"]; !got.Equal(deadline.Add(30 * time.Second)) {
		t.Fatalf("phase advanced to %v want %v", got, deadline.Add(30*time.Second))
	}
}

// TestRunCancellationBoundedSingleRound uses an unreachable loopback origin so
// the failed batch is cached to disk. That gives an exact, network-free count of
// rounds and probes after cancellation.
func TestRunCancellationBoundedSingleRound(t *testing.T) {
	checks := []Check{{ServiceID: "a", Kind: "untested"}, {ServiceID: "b", Kind: "untested"}}
	a := scheduleTestAgent(t, checks)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
	files, err := a.queue()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly one bounded round, got %d cached batches", len(files))
	}
	raw, err := os.ReadFile(filepath.Join(a.Config.StateDirectory, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var q Queued
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Batch.Samples) != len(checks) {
		t.Fatalf("one round ran %d probes, want %d", len(q.Batch.Samples), len(checks))
	}
}
