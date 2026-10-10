package status

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEvidenceValidationAndOptionalOldContract(t *testing.T) {
	s, h, token, now := fixture(t)
	if !validEvidence(nil) {
		t.Fatal("old collector contract rejected")
	}
	for _, kind := range []string{"job_success", "job_failure", "peer_success", "peer_failure", "control_message"} {
		if !validEvidence(&Evidence{Kind: kind, At: now.Format(time.RFC3339Nano), Historical: true}) {
			t.Fatal(kind)
		}
	}
	for _, e := range []*Evidence{{Kind: "raw_log", At: now.Format(time.RFC3339Nano)}, {Kind: "job_success", At: "private log text"}, {Kind: "job_success", At: "1900-01-01T00:00:00Z"}, {Kind: "job_success", At: "2200-01-01T00:00:00Z"}} {
		b := batch("invalid", "invalid", "unknown", 0)
		b.Samples[0].Evidence = e
		if w := request(h, "POST", "/api/v1/ingest", token, b); w.Code != 400 {
			t.Fatal("unsafe evidence accepted", e, w.Code)
		}
	}
	_ = s.Heartbeat("home", "h", now)
	b := batch("historical", "historical", "unknown", 0)
	b.Samples[0].Code, b.Samples[0].LatencyMS = "job_previous_boot", nil
	b.Samples[0].Evidence = &Evidence{Kind: "job_success", At: now.Add(-time.Hour).Format(time.RFC3339Nano), Historical: true}
	if err := s.Ingest("home", b, now); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(now)
	if err != nil || snap.Services[0].Status != "unknown" || snap.Services[0].Evidence == nil || snap.Services[0].LatencyMS != nil {
		t.Fatal(err, snap)
	}
	// An old successful reference is neither a recovery nor an availability sample.
	if snap.Services[0].Availability24h != nil {
		t.Fatal("reference counted as available")
	}
}
func TestEvidenceDedupExpiryReplayAndPersistence(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	baseline := batch("baseline", "baseline", "unknown", 0)
	baseline.Samples[0].Code, baseline.Samples[0].LatencyMS = "control_wait", nil
	if err := s.Ingest("home", baseline, now); err != nil {
		t.Fatal(err)
	}
	b := batch("b", "event", "healthy", 20)
	b.Samples[0].CheckedAgeSeconds = number(0)
	b.Samples[0].Code, b.Samples[0].LatencyMS = "control_fresh", nil
	b.Samples[0].Evidence = &Evidence{Kind: "control_message", At: now.Add(-20 * time.Second).Format(time.RFC3339Nano)}
	if err := s.Ingest("home", b, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	b.ID = "reread"
	*b.Samples[0].AgeSeconds = 60
	if err := s.Ingest("home", b, now.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot(now.Add(40 * time.Second))
	if snap.Services[0].Status != "healthy" || *snap.Services[0].ObservedAgeSeconds != 59 || snap.Services[0].Evidence.At != b.Samples[0].Evidence.At {
		t.Fatal("event renewed", snap.Services[0])
	}
	b.ID, b.Samples[0].ID, b.Replay = "replay", "old-event", true
	b.Samples[0].Evidence.At = now.Add(time.Hour).Format(time.RFC3339Nano)
	if err := s.Ingest("home", b, now.Add(71*time.Second)); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.Snapshot(now.Add(71 * time.Second))
	if snap.Services[0].Status != "unknown" || snap.Services[0].LatencyMS != nil || snap.Services[0].Evidence == nil || snap.Services[0].Evidence.At == b.Samples[0].Evidence.At {
		t.Fatal("replay evidence refreshed current", snap.Services[0])
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.Snapshot(now.Add(70 * time.Second))
	if err != nil || persisted.Services[0].Evidence == nil || persisted.Services[0].Evidence.At != snap.Services[0].Evidence.At {
		t.Fatal("evidence did not persist", err, persisted)
	}
	raw, _ := json.Marshal(snap.Services[0].Evidence)
	if strings.Contains(string(raw), "boot") || strings.Contains(string(raw), "counter") {
		t.Fatal("raw metadata exposed")
	}
}
