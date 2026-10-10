//go:build !linux

package collector

import "net"

func openICMP4Socket() (net.PacketConn, error) {
	return nil, errUnsupported
}

func openICMPSocket() (net.PacketConn, error) {
	return nil, errUnsupported
}
