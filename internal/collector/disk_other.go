//go:build !linux

package collector

func diskUsage(_ string, _ bool) (float64, error) { return 0, errUnsupported }
