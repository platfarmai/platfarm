//go:build !linux

package api

import "errors"

func diskFreeBytes(dir string) (int64, error) {
	return 0, errors.New("disk space detection is not supported on this platform")
}
