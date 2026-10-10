package status

import (
	"testing"
	"time"
)

func TestControlAssessmentOrderCannotRenewProofOrReviveOlderDecision(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	baseline := batch("baseline", "baseline", "unknown", 0)
	baseline.Samples[0].Code = "control_wait"
	if err := s.Ingest("home", baseline, now); err != nil {
		t.Fatal(err)
	}
	fresh := batch("growth", "growth", "healthy", 20)
	fresh.Samples[0].Code = "control_fresh"
	fresh.Samples[0].CheckedAgeSeconds = number(0)
	fresh.Samples[0].LatencyMS = nil
	if err := s.Ingest("home", fresh, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot(now.Add(10 * time.Second))
	if snap.Services[0].Status != "healthy" || *snap.Services[0].ObservedAgeSeconds != 20 {
		t.Fatal("older proof blocked a newer assessment", snap.Services[0])
	}
	unavailable := batch("unavailable", "unavailable", "unknown", 0)
	unavailable.Samples[0].Code = "control_unavailable"
	if err := s.Ingest("home", unavailable, now.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	late := batch("late", "late", "healthy", 70)
	late.Samples[0].Code, late.Samples[0].CheckedAgeSeconds = "control_fresh", number(35)
	if err := s.Ingest("home", late, now.Add(50*time.Second)); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.Snapshot(now.Add(50 * time.Second))
	if snap.Services[0].Status != "unknown" || *snap.Services[0].ObservedAt != iso(now.Add(20*time.Second).UnixMilli()) {
		t.Fatal("older decision revived unavailable control", snap.Services[0])
	}
	for _, age := range []float64{-1, 21} {
		fresh.Samples[0].CheckedAgeSeconds = number(age)
		if validateBatch(fresh, s.Config, "home") == nil {
			t.Fatal("invalid assessment age accepted", age)
		}
	}
	fresh.Samples[0].CheckedAgeSeconds = number(0)
	fresh.Samples[0].Code = "ok"
	if validateBatch(fresh, s.Config, "home") == nil {
		t.Fatal("non-control assessment override accepted")
	}
}
