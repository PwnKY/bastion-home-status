package collector

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
)

func TestHTTPAndTailscaleSemantics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(204)
		case "/client":
			_, _ = w.Write([]byte(`{"BackendState":"Running","Health":[]}`))
		case "/bad":
			_, _ = w.Write([]byte(`{"BackendState":"Stopped","Health":[]}`))
		case "/metrics":
			_, _ = w.Write([]byte("# TYPE cloudflared_tunnel_ha_connections gauge\ncloudflared_tunnel_ha_connections 4\n"))
		default:
			w.WriteHeader(200)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	if got := Probe(ctx, Check{Kind: "http", URL: server.URL + "/ok", ExpectedStatus: 204}); got.Status != "healthy" || got.LatencyMS == nil {
		t.Fatal(got)
	}
	if got := Probe(ctx, Check{Kind: "tailscale", URL: server.URL + "/client"}); got.Status != "healthy" || got.PathMode != "unknown" {
		t.Fatal(got)
	}
	if got := Probe(ctx, Check{Kind: "tailscale", URL: server.URL + "/bad"}); got.Status != "down" {
		t.Fatal(got)
	}
	if got := Probe(ctx, Check{Kind: "tunnel", URL: server.URL, MetricURL: server.URL + "/metrics"}); got.ReadyConnections == nil || *got.ReadyConnections != 4 {
		t.Fatal(got)
	}
	if got := Probe(ctx, Check{Kind: "untested"}); got.Status != "untested" {
		t.Fatal(got)
	}
}
func TestTLSMustBeValidatedAndRedirectsNotFollowed(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer tlsServer.Close()
	if got := Probe(context.Background(), Check{Kind: "http", URL: tlsServer.URL}); got.Status != "down" {
		t.Fatal("untrusted TLS accepted")
	}
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			hits++
		}
		http.Redirect(w, r, "/target", 302)
	}))
	defer server.Close()
	got := Probe(context.Background(), Check{Kind: "http", URL: server.URL})
	if got.Status != "down" || hits != 0 {
		t.Fatal(got, hits)
	}
}
func agentFixture(t *testing.T, url string) *Agent {
	t.Helper()
	dir := t.TempDir()
	secret := filepath.Join(dir, "credential")
	if err := os.WriteFile(secret, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{ServerURL: url, SecretFile: secret, StateDirectory: filepath.Join(dir, "queue"), IntervalSeconds: 30, MaxQueuedBatches: 2, MaxQueueBytes: 100000, Checks: []Check{{ServiceID: "probe", Kind: "untested"}}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestQueueBoundedReplayAndAcknowledgement(t *testing.T) {
	var replays []bool
	online := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !online {
			w.WriteHeader(503)
			return
		}
		var b status.Batch
		_ = json.NewDecoder(r.Body).Decode(&b)
		replays = append(replays, b.Replay)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()
	a := agentFixture(t, server.URL)
	for i := 0; i < 3; i++ {
		q := a.Collect(context.Background())
		a.next = map[string]time.Time{}
		if err := a.Save(q); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	files, err := a.queue()
	if err != nil || len(files) != 2 {
		t.Fatal(files, err)
	}
	unrelated := filepath.Join(a.Config.StateDirectory, "unrelated.json")
	_ = os.WriteFile(unrelated, []byte("retain me"), 0600)
	if err = a.Flush(context.Background()); err == nil {
		t.Fatal("failed delivery acknowledged")
	}
	files, _ = a.queue()
	if len(files) != 2 {
		t.Fatal(files)
	}
	online = true
	if err = a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, _ = a.queue()
	if len(files) != 0 || len(replays) != 2 || !replays[0] || !replays[1] {
		t.Fatal(files, replays)
	}
	if _, err = os.Stat(unrelated); err != nil {
		t.Fatal("unrelated file removed")
	}
}
func TestRebootHistoryHasUnknownAge(t *testing.T) {
	a := agentFixture(t, "http://127.0.0.1:1")
	q := a.Collect(context.Background())
	live := q.ForSend(false)
	if len(live.Samples) != 1 || live.Samples[0].AgeSeconds == nil {
		t.Fatal(live)
	}
	q.BootID = "previous-kernel-boot"
	old := q.ForSend(true)
	if !old.Replay || old.Samples[0].AgeSeconds != nil {
		t.Fatal(old)
	}
}
func TestPlaintextPublicReportingDenied(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	_ = os.WriteFile(secret, []byte(strings.Repeat("a", 64)), 0600)
	if _, err := New(Config{ServerURL: "http://example.com", SecretFile: secret, StateDirectory: dir, IntervalSeconds: 30, Checks: []Check{{ServiceID: "p", Kind: "untested"}}}); err == nil {
		t.Fatal("public plaintext permitted")
	}
}
func TestDNSUsesRealProtocolAndExpectedRcode(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			response := append([]byte(nil), buf[:n]...)
			binary.BigEndian.PutUint16(response[2:4], 0x8183)
			_, _ = conn.WriteTo(response, addr)
		}
	}()
	c := Check{Kind: "dns", DNSServer: conn.LocalAddr().String(), Query: "example.com", ExpectedRCode: 3}
	if got := Probe(context.Background(), c); got.Status != "healthy" {
		t.Fatal(got)
	}
	c.ExpectedRCode = 0
	if got := Probe(context.Background(), c); got.Status != "down" {
		t.Fatal(got)
	}
}
