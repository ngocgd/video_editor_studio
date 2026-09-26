//go:build linux

package diskguard

import "syscall"

// freeBytes returns the bytes available to an unprivileged writer on the
// filesystem holding path.
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil //nolint:gosec // Bsize is a positive block size
}
