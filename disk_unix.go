//go:build !windows

package main

import "syscall"

func diskUsage(path string) (total, used uint64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	total = stat.Blocks * uint64(stat.Bsize)
	avail := stat.Bavail * uint64(stat.Bsize)
	if total > avail {
		used = total - avail
	}
	return total, used, nil
}
