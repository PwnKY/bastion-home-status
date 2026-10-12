package status

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCachedSourceAndObservationExpireIndependently(t *testing.T) {
	for _, sourceExpires := range []bool{true, false} {
		t.Run(map[bool]string{true: "source", false: "observation"}[sourceExpires], func(t *testing.T) {
			s, h, _, now := fixture(t)
			heartAt, sampleAt := now, now
			if sourceExpires {
				heartAt = now.Add(-89 * time.Second)
				s.Config.Services[0].StaleAfterSeconds = 120
			} else {
				sampleAt = now.Add(-89 * time.Second)
				s.Config.Agents[0].StaleAfterSeconds = 120
			}
			if err := s.Heartbeat("home", "h", heartAt); err != nil {
				t.Fatal(err)
			}
			b := batch("b", "s", "healthy", 0)
			b.Samples[0].PathMode = "direct"
			b.Samples[0].ReadyConnections = new(2)
			b.Samples[0].UsagePercent = number(21)
			b.Samples[0].Evidence = &Evidence{Kind: "peer_success", At: now.Format(time.RFC3339), Historical: true}
			if err := s.Ingest("home", b, sampleAt); err != nil {
				t.Fatal(err)
			}
			first, err := h.snapshot(context.Background())
			if err != nil || first.Services[0].Status != "healthy" {
				t.Fatal(err)
			}
			h.Now = func() time.Time { return now.Add(time.Second) }
			next, err := h.snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			x := next.Services[0]
			if x.Status != "unknown" || x.LatencyMS != nil || x.PathMode != "unknown" || x.ReadyConnections != nil || x.UsagePercent != nil || x.Evidence == nil {
				t.Fatal("cache retained expired measurement or dropped reference", x)
			}
			if first.Services[0].Status != "healthy" || first.Services[0].LatencyMS == nil {
				t.Fatal("ageing mutated previous response")
			}
		})
	}
}
func TestSnapshotBuildErrorsAreNeverCached(t *testing.T) {
	s, h, _, _ := fixture(t)
	calls := 0
	h.buildSnapshot = func(ctx context.Context, at time.Time) (Snapshot, error) {
		calls++
		if calls == 1 {
			return Snapshot{}, errors.New("unavailable")
		}
		return s.SnapshotContext(ctx, at)
	}
	if _, err := h.snapshot(context.Background()); err == nil {
		t.Fatal("hidden read failure")
	}
	if _, err := h.snapshot(context.Background()); err != nil || calls != 2 {
		t.Fatal("error cached", err)
	}
}
