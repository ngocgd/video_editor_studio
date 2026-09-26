//go:build !linux

package diskguard

import "errors"

// freeBytes is only implemented for Linux, where the api and workers run;
// elsewhere a Watermark needs an explicit FreeBytes function.
func freeBytes(string) (uint64, error) {
	return 0, errors.New("diskguard: free space is only measured on linux")
}
