//go:build linux

package collector

import (
	"net"

	"golang.org/x/net/icmp"
)

func openICMP4Socket() (net.PacketConn, error) {
	return icmp.ListenPacket("udp4", "0.0.0.0")
}

func openICMPSocket() (net.PacketConn, error) {
	return icmp.ListenPacket("udp6", "::")
}
