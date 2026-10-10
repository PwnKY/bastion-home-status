//go:build linux

package collector

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/icmp"
)

// No public network dependency: checks identifier rewriting and checksum handling
// on both non-raw Linux ping socket families. Loopback is used only by this kernel
// test, not accepted as a deployed probe target.
func TestLinuxUnprivilegedPingSocket(t *testing.T) {
	for _, entry := range []struct {
		name string
		ip   netip.Addr
		open func() (net.PacketConn, error)
	}{
		{"IPv4", netip.MustParseAddr("127.0.0.1"), openICMP4Socket},
		{"IPv6", netip.MustParseAddr("::1"), openICMPSocket},
	} {
		t.Run(entry.name, func(t *testing.T) {
			conn, err := entry.open()
			if err != nil {
				t.Skip("Linux ping socket unavailable; deployment preflight must verify capability")
			}
			defer conn.Close()
			local, ok := conn.LocalAddr().(*net.UDPAddr)
			if !ok || local.Port == 0 {
				t.Fatal("missing kernel ping identifier", conn.LocalAddr())
			}
			if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			payload := []byte("loopback-kernel-check")
			_, requestType, _ := icmpFamily(entry.ip)
			raw, err := (&icmp.Message{Type: requestType, Body: &icmp.Echo{ID: local.Port, Seq: 4321, Data: payload}}).Marshal(nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.WriteTo(raw, &net.UDPAddr{IP: net.IP(entry.ip.AsSlice())}); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 256)
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				t.Fatal(err)
			}
			if !echoMatches(buf[:n], peer, entry.ip, local.Port, 4321, payload) {
				t.Fatal("kernel Echo type/source/identifier/sequence/payload mismatch")
			}
		})
	}
}
