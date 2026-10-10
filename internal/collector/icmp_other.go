//go:build !linux

package collector

import "net"

func openICMPSocket() (net.PacketConn, error) {
	return nil, errUnsupported
}
