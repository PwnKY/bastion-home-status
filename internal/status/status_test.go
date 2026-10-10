package status

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Store, *Handler, string, time.Time) {
	t.Helper()
	dir := t.TempDir()
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := hex.EncodeToString(raw)
	secret := filepath.Join(dir, "credential")
	if err := os.WriteFile(secret, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{Database: filepath.Join(dir, "state.sqlite"), Agents: []Agent{{ID: "home", Label: "家庭采集", Kind: "family", SecretFile: secret, IntervalSeconds: 30, StaleAfterSeconds: 90}}, Services: []Definition{{ID: "ipv4", Name: "出口探测", Group: "network", Scope: "家庭侧请求；不是外部业务验证", CollectorID: "home", StaleAfterSeconds: 90}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Unix(1760000000, 0)
	h := NewHandler(s)
	h.Now = func() time.Time { return now }
	return s, h, token, now
}
func request(h *Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func batch(id, sampleID, state string, age float64) Batch {
	return Batch{ID: id, Samples: []Observation{{ID: sampleID, ServiceID: "ipv4", Status: state, Code: "ok", AgeSeconds: &age, LatencyMS: number(12)}}}
}
func TestAuthAndLimits(t *testing.T) {
	_, h, token, _ := fixture(t)
	for _, key := range []string{"", "invalid"} {
		if w := request(h, "POST", "/api/v1/heartbeat", key, Heartbeat{ID: "one"}); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if w := request(h, "POST", "/api/v1/heartbeat", token, Heartbeat{ID: "one"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	b := batch("b", "s", "healthy", 0)
	b.Samples[0].ServiceID = "unauthorized"
	if w := request(h, "POST", "/api/v1/ingest", token, b); w.Code != 400 {
		t.Fatal(w.Code)
	}
	for i := 0; i < 125; i++ {
		_ = request(h, "POST", "/api/v1/heartbeat", token, Heartbeat{ID: "same"})
	}
	if w := request(h, "POST", "/api/v1/heartbeat", token, Heartbeat{ID: "same"}); w.Code != 429 {
		t.Fatal(w.Code)
	}
}
func TestMissingAndExpiryAreUnknown(t *testing.T) {
	s, _, _, now := fixture(t)
	snap, err := s.Snapshot(now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services[0].Status != "unknown" || !snap.Collector.Stale {
		t.Fatal(snap)
	}
	if err = s.Heartbeat("home", "h1", now); err != nil {
		t.Fatal(err)
	}
	if err = s.Ingest("home", batch("b1", "s1", "healthy", 0), now); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot(now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services[0].Status != "healthy" || snap.Services[0].LatencyMS == nil {
		t.Fatal(snap)
	}
	snap, err = s.Snapshot(now.Add(90 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services[0].Status != "unknown" || snap.Services[0].LatencyMS != nil || !snap.Collector.Stale {
		t.Fatal(snap)
	}
}
func TestReplayCannotRefreshStateOrHeartbeat(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	_ = s.Ingest("home", batch("b", "s", "healthy", 0), now)
	future := now.Add(5 * time.Minute)
	replay := batch("replay", "older", "down", 0)
	replay.Replay = true
	if err := s.Ingest("home", replay, future); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(future)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Collector.Stale || snap.Services[0].Status != "unknown" || *snap.Services[0].ObservedAt != iso(now.UnixMilli()) {
		t.Fatal(snap)
	}
	if err = s.Heartbeat("home", "h", future); err != nil {
		t.Fatal(err)
	}
	cols, _ := s.Collectors(future)
	if !cols[0].Stale {
		t.Fatal("duplicate heartbeat refreshed freshness")
	}
}
func TestDedupAndOlderLiveSamples(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	first := batch("b", "s", "healthy", 0)
	for i := 0; i < 3; i++ {
		if err := s.Ingest("home", first, now); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	_ = s.db.QueryRow(`SELECT count(*) FROM samples`).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	if err := s.Ingest("home", batch("older", "older", "down", 60), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services[0].Status != "healthy" {
		t.Fatal(snap.Services[0])
	}
}
func TestFailureAndRecoveryHysteresis(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	_ = s.Ingest("home", batch("b0", "s0", "healthy", 0), now)
	expected := []string{"unknown", "unknown", "down", "down", "healthy"}
	for i, want := range expected {
		state := "down"
		if i >= 3 {
			state = "healthy"
		}
		id := string(rune('a' + i))
		now = now.Add(time.Second)
		if err := s.Ingest("home", batch("batch"+id, "sample"+id, state, 0), now); err != nil {
			t.Fatal(err)
		}
		snap, err := s.Snapshot(now)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Services[0].Status != want {
			t.Fatalf("%d got %s want %s", i, snap.Services[0].Status, want)
		}
	}
	events, err := s.Incidents()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Status != "resolved" {
		t.Fatal(events)
	}
}
func TestHeartbeatLossIncidentAndRecovery(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Heartbeat("home", "h1", now)
	if err := s.Reconcile(now.Add(90 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_ = s.Reconcile(now.Add(91 * time.Second))
	events, _ := s.Incidents()
	if len(events) != 1 || events[0].Status != "investigating" {
		t.Fatal(events)
	}
	_ = s.Heartbeat("home", "h2", now.Add(92*time.Second))
	events, _ = s.Incidents()
	if events[0].Status != "resolved" {
		t.Fatal(events)
	}
}
func TestPublicSnapshotHasNoCredentialsOrInventedHistory(t *testing.T) {
	s, h, token, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	_ = s.Ingest("home", batch("b", "s", "healthy", 0), now)
	w := request(h, "GET", "/api/v1/status", "", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, forbidden := range []string{token, s.Config.Database, s.Config.Agents[0].SecretFile, "secretFile", "Digest"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatal("private output", forbidden)
		}
	}
	snap, err := s.Snapshot(now)
	if err != nil {
		t.Fatal(err)
	}
	known := 0
	for _, p := range snap.Services[0].History {
		if p.Status != "unknown" {
			known++
		}
	}
	if known != 1 || len(snap.Services[0].History) != 48 {
		t.Fatal(known)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cache risk")
	}
}
func TestStorageSurvivesReopen(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Heartbeat("home", "h", now)
	_ = s.Ingest("home", batch("b", "s", "healthy", 0), now)
	other, err := Open(s.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	snap, err := other.Snapshot(now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services[0].Status != "healthy" {
		t.Fatal(snap)
	}
}
func TestRetentionAndHourlyAggregation(t *testing.T) {
	s, _, _, now := fixture(t)
	_ = s.Ingest("home", batch("old", "old", "healthy", 0), now.Add(-31*24*time.Hour))
	_ = s.Ingest("home", batch("new", "new", "healthy", 0), now)
	if err := s.Cleanup(now); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM samples`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	_ = s.db.QueryRow(`SELECT count(*) FROM hourly`).Scan(&n)
	if n != 2 {
		t.Fatal(n)
	}
}
func TestInvalidRequestsAndPrivateBinding(t *testing.T) {
	s, h, token, _ := fixture(t)
	for _, b := range []Batch{batch("b", "s", "healthy", -1), batch("b", "s", "healthy", 1e9), batch("b", "s", "invalid", 0)} {
		if validateBatch(b, s.Config, "home") == nil {
			t.Fatal("invalid accepted")
		}
	}
	r := httptest.NewRequest("POST", "/api/v1/heartbeat", strings.NewReader(`{"id":"one","unexpected":true}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/api/v1/status", token, nil); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request(h, "GET", "/api/v1/admin/peers", "", nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if ValidateListen("0.0.0.0:8080") == nil {
		t.Fatal("public binding permitted")
	}
	if ValidateListen("127.0.0.1:8080") != nil {
		t.Fatal("loopback rejected")
	}
}
