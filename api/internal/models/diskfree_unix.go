//go:build !windows

package models

import "syscall"

// diskFree reports the bytes available to an unprivileged writer on the
// filesystem holding dir.
func diskFree(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil //nolint:gosec,unconvert // block counts fit in int64 on any real disk
}
