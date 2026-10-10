package collector

import (
	"math"
	"testing"

	"github.com/PwnKY/bastion-home-status/internal/status"
)

func TestQueuedControlAgesBothClocksAndClearsUnlocatableReplay(t *testing.T) {
	boot, uptime := clock()
	if boot == "" || uptime < 5 {
		t.Skip("kernel boot clock unavailable")
	}
	age, checked := 30.0, 10.0
	q := Queued{BootID: boot, Uptime: uptime - 5, Batch: status.Batch{Samples: []status.Observation{{ID: "event", AgeSeconds: &age, CheckedAgeSeconds: &checked, Code: "control_fresh", Evidence: &status.Evidence{Kind: "control_message", At: "2026-01-01T00:00:00Z"}}}}}
	sent := q.ForSend(false).Samples[0]
	if sent.AgeSeconds == nil || sent.CheckedAgeSeconds == nil || *sent.AgeSeconds < 35 || *sent.CheckedAgeSeconds < 15 || math.Abs((*sent.AgeSeconds-*sent.CheckedAgeSeconds)-20) > 1e-6 || *q.Batch.Samples[0].AgeSeconds != 30 || *q.Batch.Samples[0].CheckedAgeSeconds != 10 || sent.Evidence.At != q.Batch.Samples[0].Evidence.At {
		t.Fatal("queue renewed evidence or mutated stored clocks", sent)
	}
	q.BootID = "other-boot"
	replay := q.ForSend(true)
	if !replay.Replay || replay.Samples[0].AgeSeconds != nil || replay.Samples[0].CheckedAgeSeconds != nil || replay.Samples[0].Evidence == nil {
		t.Fatal("unlocatable clocks retained", replay)
	}
}
