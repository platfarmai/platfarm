//go:build !windows

package api

import "syscall"

func diskUsageMB(path string) (totalMB, usedMB int64) {
	st := new(syscall.Statfs_t)
	if err := syscall.Statfs(path, st); err != nil {
		return 0, 0
	}
	bsize := uint64(st.Bsize)
	totalMB = int64(st.Blocks * bsize / 1024 / 1024)
	usedMB = int64((st.Blocks - st.Bfree) * bsize / 1024 / 1024)
	return totalMB, usedMB
}
