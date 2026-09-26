//go:build !windows

package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockStaging takes an exclusive flock on path, waiting until it is free
// or ctx ends. flock locks belong to the open file description, so two
// pulls in the same worker process exclude each other as surely as a
// worker and the operator CLI do, and the kernel drops the lock if the
// holder dies. The lock file itself is left in place: removing it would
// let a waiter lock an unlinked inode while a newcomer locks a new one.
func lockStaging(ctx context.Context, path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	fd := int(fh.Fd())
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(fd, syscall.LOCK_UN)
				_ = fh.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = fh.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = fh.Close()
			return nil, ctx.Err()
		case <-time.After(stagingLockPoll):
		}
	}
}
