package collector

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/PwnKY/bastion-home-status/internal/status"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// Only explicit public IPv6 literals are accepted: no DNS, zones, shell, proxy,
// private/Tailscale peers, raw-socket fallback, or configurable packet budgets.
func icmpTarget(target string) (netip.Addr, error) {
	ip, err := netip.ParseAddr(target)
	if err != nil || !ip.Is6() || ip.Is4In6() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return netip.Addr{}, errors.New("invalid public IPv6 ICMP target")
	}
	return ip, nil
}

// IPv4 is deliberately limited to explicit RFC1918 LAN targets. In particular,
// CGNAT/Tailscale peer addresses, public targets, loopback and link-local are rejected.
func icmp4Target(target string) (netip.Addr, error) {
	ip, err := netip.ParseAddr(target)
	if err != nil || !ip.Is4() || ip.Zone() != "" || !ip.IsGlobalUnicast() || !ip.IsPrivate() {
		return netip.Addr{}, errors.New("invalid private IPv4 ICMP target")
	}
	return ip, nil
}

func icmpCheckTarget(c Check) (netip.Addr, error) {
	switch c.Kind {
	case "icmp4":
		return icmp4Target(c.Target)
	case "icmp6":
		return icmpTarget(c.Target)
	default:
		return netip.Addr{}, errors.New("invalid ICMP family")
	}
}

func icmpFamily(ip netip.Addr) (int, icmp.Type, icmp.Type) {
	if ip.Is4() {
		return 1, ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply
	}
	return 58, ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply
}

func icmpProbe(ctx context.Context, c Check) status.Observation {
	if c.Kind == "icmp4" {
		return icmpProbeWith(ctx, c, openICMP4Socket)
	}
	return icmpProbeWith(ctx, c, openICMPSocket)
}

func icmpProbeWith(ctx context.Context, c Check, open func() (net.PacketConn, error)) status.Observation {
	ip, err := icmpCheckTarget(c)
	if err != nil || ctx.Err() != nil {
		return unknown("probe_error")
	}
	conn, err := open()
	if err != nil {
		// Local inability to open a ping socket is not target downtime.
		return unknown("icmp_unavailable")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if conn.SetDeadline(deadline) != nil {
		return unknown("probe_error")
	}
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || local.Port < 1 || local.Port > 65535 {
		return unknown("icmp_unavailable")
	}
	// Linux ping sockets rewrite the identifier to their bound port.
	payload := make([]byte, 24)
	if _, err := rand.Read(payload); err != nil {
		return unknown("probe_error")
	}
	seq := int(payload[0])<<8 | int(payload[1])
	_, requestType, _ := icmpFamily(ip)
	request, err := (&icmp.Message{Type: requestType, Code: 0, Body: &icmp.Echo{ID: local.Port, Seq: seq, Data: payload}}).Marshal(nil)
	if err != nil {
		return unknown("probe_error")
	}
	began := time.Now()
	n, err := conn.WriteTo(request, &net.UDPAddr{IP: net.IP(ip.AsSlice())})
	if err != nil {
		return icmpFailure(ctx, err)
	}
	if n != len(request) {
		return unknown("probe_error")
	}
	buf := make([]byte, 256)
	// Ignore foreign/late packets, but bound both time and receive processing.
	for reads := 0; reads < 16; reads++ {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return icmpFailure(ctx, err)
		}
		if echoMatches(buf[:n], peer, ip, local.Port, seq, payload) {
			if ctx.Err() != nil {
				return unknown("probe_error")
			}
			ms := float64(time.Since(began).Nanoseconds()) / 1e6
			result := healthy()
			result.LatencyMS = &ms
			return result
		}
	}
	return unknown("probe_error")
}

func icmpFailure(ctx context.Context, err error) status.Observation {
	if d, ok := ctx.Deadline(); ctx.Err() != nil || ok && !time.Now().Before(d) {
		return unknown("probe_error")
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return unknown("icmp_unavailable")
	}
	return status.Observation{Status: "down", Code: "icmp_error", PathMode: "unknown"}
}

func echoMatches(raw []byte, peer net.Addr, target netip.Addr, id, seq int, payload []byte) bool {
	addr, ok := peer.(*net.UDPAddr)
	if !ok || !addr.IP.Equal(net.IP(target.AsSlice())) || addr.Zone != "" {
		return false
	}
	protocol, _, replyType := icmpFamily(target)
	message, err := icmp.ParseMessage(protocol, raw)
	if err != nil || message.Type != replyType || message.Code != 0 {
		return false
	}
	echo, ok := message.Body.(*icmp.Echo)
	return ok && echo.ID == id && echo.Seq == seq && bytes.Equal(echo.Data, payload)
}
