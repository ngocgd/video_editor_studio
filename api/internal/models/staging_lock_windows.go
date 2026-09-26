//go:build windows

package models

import (
	"context"
	"sync"
)

// stagingLocks serialises pulls of one file within this process. Model
// downloads only ever run inside the Linux worker or CLI container,
// where lockStaging also excludes other processes; this variant only
// keeps the package usable in Windows unit tests.
var (
	stagingLocksMu sync.Mutex
	stagingLocks   = map[string]chan struct{}{}
)

func lockStaging(ctx context.Context, path string) (func(), error) {
	stagingLocksMu.Lock()
	ch, ok := stagingLocks[path]
	if !ok {
		ch = make(chan struct{}, 1)
		stagingLocks[path] = ch
	}
	stagingLocksMu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
