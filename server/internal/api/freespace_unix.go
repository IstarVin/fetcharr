//go:build unix

package api

import "syscall"

func toInt64[T ~int32 | ~uint32 | ~int64 | ~uint64](v T) int64 { return int64(v) }

// freeBytes returns the bytes available to unprivileged users on the
// filesystem holding path, or 0 when it cannot be determined.
func freeBytes(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return toInt64(st.Bavail) * toInt64(st.Bsize)
}
