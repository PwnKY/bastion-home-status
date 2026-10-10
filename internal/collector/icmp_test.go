package collector

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
)

func TestICMPTargetAndConfiguration(t *testing.T) {
	for _, target := range []string{"", "example.invalid", "::", "::1", "fe80::1", "fe80::1%eth0", "fd7a:115c:a1e0::1", "ff02::1", "127.0.0.1", "::ffff:7f00:1", "[2001:db8::1]:53", "2001:db8::1%eth0"} {
		if validateAdapter(Check{Kind: "icmp6", Target: target}) == nil {
			t.Fatalf("accepted unsafe/non-literal target %q", target)
		}
	}
	good := Check{Kind: "icmp6", Target: "2001:db8::1"}
	if err := validateAdapter(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Check{{Kind: "icmp6", Target: good.Target, ProxyURL: "http://localhost"}, {Kind: "icmp6", Target: good.Target, Socket: "socket"}, {Kind: "icmp6", Target: good.Target, Network: "tcp6"}, {Kind: "icmp6", Target: good.Target, URL: "https://example.invalid"}, {Kind: "icmp6", Target: good.Target, ConnectIP: good.Target}} {
		if validateAdapter(bad) == nil {
			t.Fatal("accepted ambiguous transport", bad)
		}
	}
}

func echoPacket(t *testing.T, id, seq int, payload []byte) []byte {
	t.Helper()
	raw, err := (&icmp.Message{Type: ipv6.ICMPTypeEchoReply, Body: &icmp.Echo{ID: id, Seq: seq, Data: payload}}).Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestICMPEchoRequiresCompleteTransaction(t *testing.T) {
	ip := netip.MustParseAddr("2001:db8::1")
	peer := &net.UDPAddr{IP: net.IP(ip.AsSlice())}
	payload := []byte("random-test-payload")
	good := echoPacket(t, 101, 202, payload)
	if !echoMatches(good, peer, ip, 101, 202, payload) {
		t.Fatal("valid echo rejected")
	}
	for _, raw := range [][]byte{nil, {129}, echoPacket(t, 102, 202, payload), echoPacket(t, 101, 203, payload), echoPacket(t, 101, 202, []byte("foreign")), append(append([]byte{}, good...), 0)} {
		if echoMatches(raw, peer, ip, 101, 202, payload) {
			t.Fatal("foreign/truncated/trailing echo accepted")
		}
	}
	for _, addr := range []net.Addr{nil, &net.UDPAddr{IP: net.ParseIP("2001:db8::2")}, &net.UDPAddr{IP: peer.IP, Zone: "eth0"}, &net.IPAddr{IP: peer.IP}} {
		if echoMatches(good, addr, ip, 101, 202, payload) {
			t.Fatal("foreign source accepted")
		}
	}
	wrongType := append([]byte{}, good...)
	wrongType[0] = 128
	wrongCode := append([]byte{}, good...)
	wrongCode[1] = 1
	if echoMatches(wrongType, peer, ip, 101, 202, payload) || echoMatches(wrongCode, peer, ip, 101, 202, payload) {
		t.Fatal("request/error accepted as response")
	}
}

type fakePingConn struct {
	mu       sync.Mutex
	request  []byte
	writes   int
	reads    int
	closed   bool
	deadline time.Time
	readErr  error
	foreign  bool
}

func (f *fakePingConn) ReadFrom(b []byte) (int, net.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.readErr != nil {
		return 0, nil, f.readErr
	}
	if f.closed {
		return 0, nil, net.ErrClosed
	}
	raw := append([]byte{}, f.request...)
	raw[0] = 129
	if f.foreign {
		raw[len(raw)-1] ^= 1
	}
	return copy(b, raw), &net.UDPAddr{IP: net.ParseIP("2001:db8::1")}, nil
}
func (f *fakePingConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.request = append([]byte{}, b...)
	f.writes++
	return len(b), nil
}
func (f *fakePingConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *fakePingConn) LocalAddr() net.Addr                { return &net.UDPAddr{IP: net.IPv6unspecified, Port: 1234} }
func (f *fakePingConn) SetDeadline(d time.Time) error      { f.deadline = d; return nil }
func (f *fakePingConn) SetReadDeadline(d time.Time) error  { return f.SetDeadline(d) }
func (f *fakePingConn) SetWriteDeadline(d time.Time) error { return f.SetDeadline(d) }

func TestICMPProbeBudgetsAndFailureSemantics(t *testing.T) {
	check := Check{Kind: "icmp6", Target: "2001:db8::1"}
	conn := &fakePingConn{}
	start := time.Now()
	got := icmpProbeWith(context.Background(), check, func() (net.PacketConn, error) { return conn, nil })
	if got.Status != "healthy" || got.LatencyMS == nil || *got.LatencyMS < 0 || conn.writes != 1 || !conn.closed || conn.deadline.After(start.Add(3100*time.Millisecond)) {
		t.Fatal("bad bounded echo result", got, conn)
	}
	conn = &fakePingConn{readErr: os.ErrDeadlineExceeded}
	got = icmpProbeWith(context.Background(), check, func() (net.PacketConn, error) { return conn, nil })
	if got.Status != "down" || got.Code != "icmp_error" || got.LatencyMS != nil {
		t.Fatal("timeout fabricated success/latency", got)
	}
	for _, err := range []error{os.ErrPermission, errUnsupported, errors.New("socket unavailable")} {
		got = icmpProbeWith(context.Background(), check, func() (net.PacketConn, error) { return nil, err })
		if got.Status != "unknown" || got.Code != "icmp_unavailable" || got.LatencyMS != nil {
			t.Fatal("local socket failure claimed target failure", got)
		}
	}
	conn = &fakePingConn{foreign: true}
	got = icmpProbeWith(context.Background(), check, func() (net.PacketConn, error) { return conn, nil })
	if got.Status != "unknown" || got.LatencyMS != nil || conn.reads != 16 || conn.writes != 1 {
		t.Fatal("receive flood not bounded", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = icmpProbeWith(ctx, check, func() (net.PacketConn, error) { t.Fatal("opened socket after cancel"); return nil, nil })
	if got.Status != "unknown" || got.LatencyMS != nil {
		t.Fatal("cancelled probe claimed downtime", got)
	}
}
