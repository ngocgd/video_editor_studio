package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/minio/minio-go/v7"
)

// checksumHeader asks the object store to verify the upload against a
// SHA-256 and to keep it with the object, so a later HEAD can prove the
// stored bytes are still the ones that were written.
const checksumHeader = "X-Amz-Checksum-Sha256"

// PutFileChecksummed uploads a local file under key as a single PUT
// carrying its SHA-256, which the store verifies and keeps as the
// object's x-amz-checksum-sha256. Render segments use it so a cache hit
// can be checked against assets.sha256 without reading the object.
func (c *Internal) PutFileChecksummed(ctx context.Context, key, path, contentType string) (Stored, error) {
	f, err := os.Open(path) //nolint:gosec // a step's own output inside its temp dir
	if err != nil {
		return Stored{}, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return Stored{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Stored{}, err
	}
	sum := h.Sum(nil)
	info, err := c.PutObject(ctx, c.Bucket, key, f, size, minio.PutObjectOptions{
		ContentType:      contentType,
		DisableMultipart: true,
		UserMetadata:     map[string]string{checksumHeader: base64.StdEncoding.EncodeToString(sum)},
	})
	if err != nil {
		return Stored{}, fmt.Errorf("storage: put %s: %w", key, err)
	}
	return Stored{Size: size, SHA256Hex: hex.EncodeToString(sum), VersionID: info.VersionID}, nil
}

// ObjectSHA256 returns the SHA-256 the store kept for an object version
// (hex), or "" when it has none. A missing object is ErrObjectNotFound.
func (c *Internal) ObjectSHA256(ctx context.Context, key, versionID string) (string, error) {
	info, err := c.StatObject(ctx, c.Bucket, key, minio.StatObjectOptions{VersionID: versionID, Checksum: true})
	if err != nil {
		if code := minio.ToErrorResponse(err).Code; code == "NoSuchKey" || code == "NoSuchVersion" {
			return "", ErrObjectNotFound
		}
		return "", err
	}
	if info.ChecksumSHA256 == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(info.ChecksumSHA256)
	if err != nil {
		return "", nil //nolint:nilerr // an unreadable checksum is treated as none
	}
	return hex.EncodeToString(raw), nil
}

// RemoveVersion deletes one object version (all versions' latest when
// versionID is empty). A missing object is not an error.
func (c *Internal) RemoveVersion(ctx context.Context, key, versionID string) error {
	err := c.RemoveObject(ctx, c.Bucket, key, minio.RemoveObjectOptions{VersionID: versionID})
	if err != nil && minio.ToErrorResponse(err).Code != "NoSuchKey" {
		return err
	}
	return nil
}
