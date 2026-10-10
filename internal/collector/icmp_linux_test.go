//go:build linux

package collector

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
)

// No public network dependency: checks kernel identifier rewriting and checksum
// handling of the same non-raw Linux ping socket used by the deployed adapter.
func TestLinuxUnprivilegedPingSocket(t *testing.T) {
	conn, err := openICMPSocket()
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
	raw, err := (&icmp.Message{Type: ipv6.ICMPTypeEchoRequest, Body: &icmp.Echo{ID: local.Port, Seq: 4321, Data: payload}}).Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteTo(raw, &net.UDPAddr{IP: net.IPv6loopback}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := icmp.ParseMessage(58, buf[:n])
	if err != nil || msg.Type != ipv6.ICMPTypeEchoReply {
		t.Fatal("invalid kernel echo", msg, err)
	}
	echo := msg.Body.(*icmp.Echo)
	if echo.ID != local.Port || echo.Seq != 4321 || string(echo.Data) != string(payload) {
		t.Fatal("kernel transaction mismatch")
	}
}
