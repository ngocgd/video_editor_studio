package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
)

// ReadRange fills buf with the object bytes starting at offset, using one
// fresh ranged GET per call. Long streaming consumers (the YouTube upload)
// call it once per chunk so no single request or URL has to outlive a
// multi-hour transfer, and nothing is spooled to a temp file. It returns
// an error unless exactly len(buf) bytes were read.
func (c *Internal) ReadRange(ctx context.Context, key string, offset int64, buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(offset, offset+int64(len(buf))-1); err != nil {
		return fmt.Errorf("storage: range %d+%d: %w", offset, len(buf), err)
	}
	obj, err := c.GetObject(ctx, c.Bucket, key, opts)
	if err != nil {
		return fmt.Errorf("storage: get %s range: %w", key, err)
	}
	defer func() { _ = obj.Close() }()
	if _, err := io.ReadFull(obj, buf); err != nil {
		return fmt.Errorf("storage: read %s at %d: %w", key, offset, err)
	}
	return nil
}
