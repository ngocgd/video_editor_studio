package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/minio/minio-go/v7"
)

// IsTenantKey reports whether key lies under the tenant's own prefix.
func IsTenantKey(tenantID, key string) bool {
	return strings.HasPrefix(key, "t/"+tenantID+"/")
}

// RemoveAllVersions deletes every version (and delete marker) of exactly
// key, freeing its space in the versioned bucket; a plain delete would
// only add a marker. A missing key is not an error.
func (c *Internal) RemoveAllVersions(ctx context.Context, key string) error {
	var errs []error
	for obj := range c.ListObjects(ctx, c.Bucket, minio.ListObjectsOptions{Prefix: key, WithVersions: true}) {
		if obj.Err != nil {
			return fmt.Errorf("storage: list versions of %s: %w", key, obj.Err)
		}
		if obj.Key != key {
			continue
		}
		if err := c.RemoveVersion(ctx, key, obj.VersionID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
