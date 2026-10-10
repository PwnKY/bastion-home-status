package status

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMultiCollectorIsolation(t *testing.T) {
	original, _, _, now := fixture(t)
	c := original.Config
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := hex.EncodeToString(raw)
	path := filepath.Join(t.TempDir(), "public-credential")
	_ = os.WriteFile(path, []byte(token), 0600)
	c.Agents = append(c.Agents, Agent{ID: "edge", Kind: "public", Label: "公网侧", SecretFile: path, IntervalSeconds: 30, StaleAfterSeconds: 90})
	c.Services = append(c.Services, Definition{ID: "edge-health", Name: "公网基础接口", Scope: "只测基础接口", Group: "monitoring", CollectorID: "edge", StaleAfterSeconds: 90})
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := NewHandler(s)
	later := now.Add(100 * time.Second)
	h.Now = func() time.Time { return later }
	_ = s.Heartbeat("home", "home-h", now)
	_ = s.Ingest("home", batch("home-b", "home-s", "healthy", 0), now)
	_ = s.Heartbeat("edge", "edge-h", later)
	b := batch("edge-b", "edge-s", "healthy", 0)
	b.Samples[0].ServiceID = "edge-health"
	if err = s.Ingest("edge", b, later); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(later)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services[0].Status != "unknown" || snap.Services[1].Status != "healthy" {
		t.Fatal(snap.Services)
	}
	bad := batch("bad", "bad-s", "healthy", 0)
	if w := request(h, "POST", "/api/v1/ingest", token, bad); w.Code != 400 {
		t.Fatal("public credential updated home target")
	}
}
func TestClockSkewIgnoredAndUnknownRebootAgeRetained(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	b := batch("b", "s", "healthy", 0)
	b.Samples[0].CapturedAt = "1970-01-01T00:00:00Z"
	if err := s.Ingest("home", b, now); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(now)
	if err != nil {
		t.Fatal(err)
	}
	if *snap.Services[0].ObservedAt != iso(now.UnixMilli()) {
		t.Fatal("wall clock trusted")
	}
	old := batch("old", "old-s", "down", 0)
	old.Replay = true
	old.Samples[0].AgeSeconds = nil
	if err = s.Ingest("home", old, now); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM samples WHERE observed IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
func TestLongProbeDoesNotInventUptimeAfterSourceLoss(t *testing.T) {
	s, _, _, now := fixture(t)
	s.Config.Services[0].StaleAfterSeconds = 86400
	_ = s.Heartbeat("home", "h", now)
	_ = s.Ingest("home", batch("b", "s", "healthy", 0), now)
	snap, err := s.Snapshot(now.Add(91 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services[0].Status != "unknown" || snap.Services[0].History[47].Status != "unknown" {
		t.Fatal(snap.Services[0])
	}
}
func TestConfirmedFailureDoesNotBecomeDegradedAfterOneRecovery(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	for i, state := range []string{"down", "down", "down", "healthy", "down"} {
		id := string(rune('a' + i))
		if err := s.Ingest("home", batch("b"+id, "s"+id, state, 0), now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := s.Snapshot(now.Add(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services[0].Status != "down" {
		t.Fatal(snap.Services[0])
	}
}
func TestPublicMetadataRejectsAddressesURLsAndOpaqueSecrets(t *testing.T) {
	s, _, _, _ := fixture(t)
	for _, text := range []string{[]string{"device", "ts", "net"}[0] + ".ts.net", "https://example.com", hex.EncodeToString(make([]byte, 32))} {
		c := s.Config
		c.Services = append([]Definition(nil), c.Services...)
		c.Services[0].Scope = text
		if c.Validate() == nil {
			t.Fatal("unsafe public metadata accepted")
		}
	}
}
