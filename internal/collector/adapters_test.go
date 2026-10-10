package collector

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnitAndOneshotSemantics(t *testing.T) {
	base := "LoadState=loaded\nActiveState=inactive\nResult=success\nExecMainStatus=0\nExecMainExitTimestampMonotonic=90000000\n"
	tests := []struct {
		raw  string
		c    Check
		up   float64
		want string
	}{
		{base, Check{Kind: "job", MaxAgeSeconds: 30}, 100, "healthy"},
		{base, Check{Kind: "unit"}, 100, "down"},
		{base, Check{Kind: "job", MaxAgeSeconds: 5}, 100, "degraded"},
		{base, Check{Kind: "job"}, 50, "unknown"},
		{strings.Replace(base, "90000000", "0", 1), Check{Kind: "job"}, 100, "unknown"},
		{strings.Replace(base, "success", "exit-code", 1), Check{Kind: "job"}, 100, "down"},
		{strings.Replace(base, "inactive", "activating", 1), Check{Kind: "job"}, 100, "unknown"},
		{"LoadState=not-found\n", Check{Kind: "unit"}, 100, "untested"},
		{strings.Replace(base, "inactive", "active", 1), Check{Kind: "unit"}, 100, "healthy"},
	}
	for _, tt := range tests {
		if got := unitResult([]byte(tt.raw), tt.c, tt.up); got.Status != tt.want {
			t.Fatalf("%+v got=%+v", tt, got)
		}
	}
}
func TestPVEResourceIdentityAndStoppedTemplates(t *testing.T) {
	raw := []byte(`[{"type":"node","status":"online"},{"type":"qemu","vmid":100,"status":"running"},{"type":"lxc","vmid":101,"status":"stopped"}]`)
	for _, tt := range []struct {
		c    Check
		want string
	}{
		{Check{ResourceType: "node"}, "healthy"}, {Check{ResourceType: "qemu", ResourceID: 100}, "healthy"},
		{Check{ResourceType: "lxc", ResourceID: 101}, "down"}, {Check{ResourceType: "qemu", ResourceID: 101}, "unknown"},
	} {
		if got := pveResult(raw, tt.c); got.Status != tt.want {
			t.Fatal(tt, got)
		}
	}
	if got := pveResult([]byte(`[{"type":"node","status":"online"},{"type":"node","status":"online"}]`), Check{ResourceType: "node"}); got.Status != "unknown" {
		t.Fatal("ambiguous node match", got)
	}
	if got := pveResult([]byte(`bad`), Check{ResourceType: "node"}); got.Status != "unknown" {
		t.Fatal(got)
	}
}
func peerFixture(now time.Time, pingAge time.Duration, ok bool) []byte {
	raw, _ := json.Marshal(map[string]any{"version": 1, "initialized": true, "lastSuccessfulPollAt": now.Format(time.RFC3339Nano), "peers": map[string]any{"private-peer": map[string]any{"present": true, "online": true, "lastPingAt": now.Add(-pingAge).Format(time.RFC3339Nano), "lastPingOK": ok, "lastPingPath": "direct", "lastPingResult": "secret-bearing discarded text"}}})
	return raw
}
func TestPeerEvidenceNoRefreshNoIdentityLeak(t *testing.T) {
	now := time.Now().UTC()
	raw := peerFixture(now, 10*time.Second, true)
	c := Check{Kind: "peer-pinger", ServiceID: "path", MaxAgeSeconds: 60}
	first := peerResult(raw, c, now)
	again := peerResult(raw, c, now.Add(time.Second))
	if first.Status != "healthy" || first.PathMode != "direct" || first.LatencyMS != nil || *first.AgeSeconds != 10 {
		t.Fatal(first)
	}
	if first.ID != again.ID || *again.AgeSeconds != 11 || first.CapturedAt != again.CapturedAt {
		t.Fatal("reread refreshed source event")
	}
	marshaled, _ := json.Marshal(first)
	if strings.Contains(string(marshaled), "private-peer") || strings.Contains(string(marshaled), "secret-bearing") {
		t.Fatal("private evidence leaked")
	}
	if got := peerResult(peerFixture(now, 24*time.Hour, true), Check{Kind: "peer-poll"}, now); got.Status != "healthy" || got.LatencyMS != nil || got.PathMode != "unknown" {
		t.Fatal("polling scope confused with old data-path evidence", got)
	}
	if got := peerResult(raw, c, now.Add(31*time.Second)); got.Status != "unknown" {
		t.Fatal("stale poll accepted", got)
	}
	if got := peerResult(peerFixture(now, 70*time.Second, true), c, now); got.Status != "unknown" {
		t.Fatal("stale ping accepted", got)
	}
	if got := peerResult(peerFixture(now, -time.Second, true), c, now); got.Status != "unknown" {
		t.Fatal("future evidence accepted", got)
	}
	if got := peerResult(peerFixture(now, 10*time.Second, false), c, now); got.Status != "degraded" || got.PathMode != "unknown" {
		t.Fatal("failed subset promoted to total down/direct", got)
	}
}
func TestCollectorPreservesPeerEventAgeAndDedupID(t *testing.T) {
	now := time.Now().UTC()
	p := filepath.Join(t.TempDir(), "evidence.json")
	os.WriteFile(p, peerFixture(now, 10*time.Second, true), 0600)
	a := &Agent{Config: Config{IntervalSeconds: 30, Checks: []Check{{ServiceID: "path", Kind: "peer-pinger", Path: p}}}, next: map[string]time.Time{}}
	first := a.Collect(context.Background())
	a.next = map[string]time.Time{}
	second := a.Collect(context.Background())
	if len(first.Batch.Samples) != 1 || *first.Batch.Samples[0].AgeSeconds < 10 || first.Batch.Samples[0].ID != second.Batch.Samples[0].ID {
		t.Fatal("source identity/age overwritten")
	}
}
func TestAdapterValidationRejectsArbitraryCommands(t *testing.T) {
	for _, c := range []Check{{Kind: "unit", Unit: "--help"}, {Kind: "unit", Unit: "bad;touch-file.service"}, {Kind: "job", Unit: "../../bad.service"}, {Kind: "command"}, {Kind: "pve", ResourceType: "shell", ResourceID: 100}, {Kind: "tcp", Target: "localhost:0"}, {Kind: "peer-pinger"}, {Kind: "unit", Unit: "good.service", MaxAgeSeconds: 9999999}} {
		if validateAdapter(c) == nil {
			t.Fatal("unsafe check accepted", c)
		}
	}
	for _, c := range []Check{{Kind: "unit", Unit: "good@name.service"}, {Kind: "job", Unit: "refresh.service"}, {Kind: "pve", ResourceType: "qemu", ResourceID: 100}, {Kind: "tcp", Target: "127.0.0.1:22"}, {Kind: "peer-pinger", Path: "fixture.json"}} {
		if err := validateAdapter(c); err != nil {
			t.Fatal(c, err)
		}
	}
}
func TestReadCommandBudgetAndTimeout(t *testing.T) {
	b := &boundedOutput{limit: 3}
	b.Write([]byte("ab"))
	if _, err := b.Write([]byte("cd")); err == nil || b.String() != "ab" {
		t.Fatal("output budget not enforced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readCommand(ctx, "nonexistent-bastion-test-command"); err == nil {
		t.Fatal("canceled command succeeded")
	}
}
func TestTCPBannerIsOnlyTransportScope(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			defer c.Close()
			c.Write([]byte("SSH-2.0-fixture\r\n"))
		}
	}()
	o := Probe(context.Background(), Check{Kind: "tcp", Target: l.Addr().String(), ExpectedText: "SSH-2.0-"})
	if o.Status != "healthy" || o.LatencyMS == nil || o.PathMode != "unknown" {
		t.Fatal(o)
	}
}
func TestCustomProbeCAStillValidatesCertificate(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer s.Close()
	c := Check{Kind: "http", URL: s.URL, ExpectedStatus: 204}
	if o := Probe(context.Background(), c); o.Status != "down" {
		t.Fatal("untrusted certificate accepted")
	}
	cert, err := x509.ParseCertificate(s.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600)
	c.CAFile = p
	if o := Probe(context.Background(), c); o.Status != "healthy" {
		t.Fatal("explicit CA failed", o)
	}
}
func TestTunnelFailureKeepsObservedZeroConnections(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(503)
		} else {
			w.Write([]byte("cloudflared_tunnel_ha_connections 0\n"))
		}
	}))
	defer s.Close()
	o := Probe(context.Background(), Check{Kind: "tunnel", URL: s.URL + "/ready", MetricURL: s.URL + "/metrics"})
	if o.Status != "down" || o.ReadyConnections == nil || *o.ReadyConnections != 0 || o.LatencyMS != nil {
		t.Fatal(o)
	}
}
