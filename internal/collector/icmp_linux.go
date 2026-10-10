//go:build linux

package collector

import (
	"net"

	"golang.org/x/net/icmp"
)

func openICMPSocket() (net.PacketConn, error) {
	return icmp.ListenPacket("udp6", "::")
}
