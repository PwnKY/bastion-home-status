package collector

import (
	"context"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// Generated fixture addresses are not deployment targets or public probes.
func fixtureLAN4() netip.Addr { return netip.AddrFrom4([4]byte{172, 20, 4, 3}) }

func TestICMP4PrivateLiteralConfiguration(t *testing.T) {
	for _, octets := range [][4]byte{{10, 23, 17, 42}, {172, 20, 4, 3}, {192, 168, 5, 2}} {
		if err := validateAdapter(Check{Kind: "icmp4", Target: netip.AddrFrom4(octets).String()}); err != nil {
			t.Fatal("RFC1918 fixture rejected", err)
		}
	}
	bad := []string{"", "example.invalid", "0.0.0.0", "127.0.0.1", "::", "::1", "2001:db8::1", "fd7a:115c:a1e0::1", "::ffff:ac14:403", fixtureLAN4().String() + ":80", fixtureLAN4().String() + "%eth0"}
	for _, octets := range [][4]byte{{100, 64, 2, 3}, {100, 127, 255, 254}, {169, 254, 4, 3}, {224, 0, 0, 1}, {255, 255, 255, 255}, {203, 0, 113, 7}, {172, 15, 4, 3}, {172, 32, 4, 3}} {
		bad = append(bad, netip.AddrFrom4(octets).String())
	}
	for _, target := range bad {
		if validateAdapter(Check{Kind: "icmp4", Target: target}) == nil {
			t.Fatalf("accepted non-LAN/non-literal target %q", target)
		}
	}
	good := fixtureLAN4().String()
	for _, check := range []Check{{Kind: "icmp4", Target: good, URL: "https://example.invalid"}, {Kind: "icmp4", Target: good, ProxyURL: "http://localhost"}, {Kind: "icmp4", Target: good, Socket: "socket"}, {Kind: "icmp4", Target: good, Network: "tcp4"}, {Kind: "icmp4", Target: good, ConnectIP: good}} {
		if validateAdapter(check) == nil {
			t.Fatal("accepted ambiguous IPv4 transport")
		}
	}
}

func echoPacket4(t *testing.T, id, seq int, payload []byte) []byte {
	t.Helper()
	raw, err := (&icmp.Message{Type: ipv4.ICMPTypeEchoReply, Body: &icmp.Echo{ID: id, Seq: seq, Data: payload}}).Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestICMP4CompleteTransactionAndFamilyIsolation(t *testing.T) {
	ip := fixtureLAN4()
	peer := &net.UDPAddr{IP: net.IP(ip.AsSlice())}
	payload := []byte("IPv4-nonce-fixture")
	good := echoPacket4(t, 1234, 4321, payload)
	if !echoMatches(good, peer, ip, 1234, 4321, payload) {
		t.Fatal("IPv4 Echo rejected")
	}
	badType := append([]byte{}, good...)
	badType[0] = 8
	badCode := append([]byte{}, good...)
	badCode[1] = 1
	for _, raw := range [][]byte{nil, {0}, badType, badCode, echoPacket(t, 1234, 4321, payload), echoPacket4(t, 1235, 4321, payload), echoPacket4(t, 1234, 4322, payload), echoPacket4(t, 1234, 4321, []byte("foreign")), append(append([]byte{}, good...), 0)} {
		if echoMatches(raw, peer, ip, 1234, 4321, payload) {
			t.Fatal("foreign/truncated/cross-family IPv4 Echo accepted")
		}
	}
	for _, addr := range []net.Addr{nil, &net.UDPAddr{IP: net.IPv6loopback}, &net.UDPAddr{IP: net.IP(netip.AddrFrom4([4]byte{172, 20, 4, 4}).AsSlice())}, &net.UDPAddr{IP: peer.IP, Zone: "eth0"}, &net.IPAddr{IP: peer.IP}} {
		if echoMatches(good, addr, ip, 1234, 4321, payload) {
			t.Fatal("foreign IPv4 source accepted")
		}
	}
	v6 := netip.MustParseAddr("2001:db8::1")
	if echoMatches(good, &net.UDPAddr{IP: net.IP(v6.AsSlice())}, v6, 1234, 4321, payload) {
		t.Fatal("IPv4 packet accepted by IPv6 matcher")
	}
}

func TestICMP4OneEchoAndFailureBudgets(t *testing.T) {
	ip := fixtureLAN4()
	check := Check{Kind: "icmp4", Target: ip.String()}
	open := func(conn *fakePingConn) func() (net.PacketConn, error) {
		return func() (net.PacketConn, error) { return conn, nil }
	}
	conn := &fakePingConn{peerIP: net.IP(ip.AsSlice())}
	began := time.Now()
	got := icmpProbeWith(context.Background(), check, open(conn))
	if got.Status != "healthy" || got.LatencyMS == nil || *got.LatencyMS < 0 || conn.writes != 1 || !conn.closed || conn.deadline.After(began.Add(3100*time.Millisecond)) {
		t.Fatal("invalid bounded IPv4 result", got)
	}
	protocol, _, _ := icmpFamily(ip)
	msg, err := icmp.ParseMessage(protocol, conn.request)
	if err != nil || msg.Type != ipv4.ICMPTypeEcho || !conn.sentTo.(*net.UDPAddr).IP.Equal(net.IP(ip.AsSlice())) {
		t.Fatal("wrong family or destination")
	}
	conn = &fakePingConn{peerIP: net.IP(ip.AsSlice()), readErr: os.ErrDeadlineExceeded}
	got = icmpProbeWith(context.Background(), check, open(conn))
	if got.Status != "down" || got.Code != "icmp_error" || got.LatencyMS != nil || conn.writes != 1 {
		t.Fatal("IPv4 timeout fabricated current RTT", got)
	}
	conn = &fakePingConn{peerIP: net.IP(ip.AsSlice()), foreign: true}
	got = icmpProbeWith(context.Background(), check, open(conn))
	if got.Status != "unknown" || got.LatencyMS != nil || conn.reads != 16 || conn.writes != 1 {
		t.Fatal("IPv4 receive flood exceeded budget", got)
	}
	got = icmpProbeWith(context.Background(), check, func() (net.PacketConn, error) { return nil, os.ErrPermission })
	if got.Status != "unknown" || got.Code != "icmp_unavailable" || got.LatencyMS != nil {
		t.Fatal("IPv4 local permission failure claimed target downtime", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = icmpProbeWith(ctx, check, func() (net.PacketConn, error) { t.Fatal("opened cancelled IPv4 socket"); return nil, nil })
	if got.Status != "unknown" || got.LatencyMS != nil {
		t.Fatal("IPv4 cancellation claimed downtime")
	}
	for _, kind := range []string{"icmp6", "invalid"} {
		got = icmpProbeWith(context.Background(), Check{Kind: kind, Target: ip.String()}, func() (net.PacketConn, error) { t.Fatal("opened invalid family socket"); return nil, nil })
		if got.Status != "unknown" || got.LatencyMS != nil {
			t.Fatal("invalid family fabricated success")
		}
	}
}
