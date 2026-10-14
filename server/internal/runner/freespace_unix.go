//go:build unix

package runner

import "syscall"

func toUint64[T ~int32 | ~uint32 | ~int64 | ~uint64](v T) uint64 { return uint64(v) }

// statfsFree returns the bytes available to unprivileged users on the
// filesystem holding path.
func statfsFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return toUint64(st.Bavail) * toUint64(st.Bsize), nil
}
