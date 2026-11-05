//go:build windows

package api

func diskUsageMB(path string) (totalMB, usedMB int64) {
	return 0, 0
}
