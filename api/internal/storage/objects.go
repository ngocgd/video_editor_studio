package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

// Stored is what a server-side write learned about the object it wrote.
type Stored struct {
	Size      int64
	SHA256Hex string
	VersionID string
}

// PutBytes writes data under key from inside the network (a step's own
// output: generated images, derived variants, peaks). The checksum is
// computed here from the bytes actually written.
func (c *Internal) PutBytes(ctx context.Context, key string, data []byte, contentType string) (Stored, error) {
	sum := sha256.Sum256(data)
	info, err := c.PutObject(ctx, c.Bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return Stored{}, fmt.Errorf("storage: put %s: %w", key, err)
	}
	return Stored{Size: int64(len(data)), SHA256Hex: hex.EncodeToString(sum[:]), VersionID: info.VersionID}, nil
}

// PresignPut issues an internal presigned PUT URL for a worker (the
// Python TTS/align engines) to upload a step's output directly. The TTL
// tracks the calling step's own timeout, never a fixed constant.
func (c *Internal) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := c.PresignedPutObject(ctx, c.Bucket, key, ttl)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// Stat returns the size and current version of key, after a worker
// uploaded it through a presigned PUT.
func (c *Internal) Stat(ctx context.Context, key string) (Stored, error) {
	info, err := c.StatObject(ctx, c.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return Stored{}, ErrObjectNotFound
		}
		return Stored{}, err
	}
	return Stored{Size: info.Size, VersionID: info.VersionID}, nil
}

// ReadAll reads a whole object (pinned to versionID when non-empty), up
// to maxBytes; a larger object is an error rather than a truncated read.
func (c *Internal) ReadAll(ctx context.Context, key, versionID string, maxBytes int64) ([]byte, error) {
	obj, err := c.GetObject(ctx, c.Bucket, key, minio.GetObjectOptions{VersionID: versionID})
	if err != nil {
		return nil, err
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(io.LimitReader(obj, maxBytes+1))
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("storage: %s is larger than %d bytes", key, maxBytes)
	}
	return data, nil
}

// DownloadTo copies an object (pinned to versionID when non-empty) into
// the local file path, for a step that hands it to ffmpeg or a ComfyUI
// upload.
func (c *Internal) DownloadTo(ctx context.Context, key, versionID, path string) error {
	return c.FGetObject(ctx, c.Bucket, key, path, minio.GetObjectOptions{VersionID: versionID})
}

// DerivedKey builds the key of a derivative of an asset (an image
// variant, waveform peaks). It lives under its own derived/ prefix, not
// below the asset's key: MinIO cannot hold an object and another object
// "inside" it at the same time.
func DerivedKey(tenantID string, assetID uuid.UUID, name string) string {
	return "t/" + tenantID + "/derived/" + assetID.String() + "/" + name
}
