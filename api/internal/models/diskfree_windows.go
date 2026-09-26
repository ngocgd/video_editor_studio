//go:build windows

package models

import "errors"

// diskFree is not implemented on Windows: model downloads only ever run
// inside the Linux worker or CLI container.
func diskFree(string) (int64, error) {
	return 0, errors.New("models: disk pre-flight is only supported on Linux")
}
