//go:build linux

package collector

import (
	"errors"
	"syscall"
)

func diskUsage(path string, separate bool) (float64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, err
	}
	if separate {
		var target, root syscall.Stat_t
		if syscall.Stat(path, &target) != nil || syscall.Stat("/", &root) != nil || target.Dev == root.Dev {
			return 0, errMissingMount
		}
	}
	used := fs.Blocks - fs.Bfree
	available := fs.Bavail
	if used+available == 0 {
		return 0, errors.New("empty filesystem statistics")
	}
	return float64(used) * 100 / float64(used+available), nil
}
