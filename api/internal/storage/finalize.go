package storage

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"

	"github.com/minio/minio-go/v7"
)

// FinalizeInfo is what Finalize learns about an uploaded object without
// ever reading the object body beyond the sniff range: size and checksum
// come from a HEAD (StatObject), the sniffed MIME from a single 512B
// range GET. sha256 uploads its own SHA-256 as an x-amz-checksum-sha256
// trailer/field (workers via minio-go PutObject options, browsers via
// SubtleCrypto in the presigned POST form); if the uploader did not
// supply one, SHA256Hex is "" and the asset still finalizes (the
// checksum is provenance, not a correctness gate).
type FinalizeInfo struct {
	Size        int64
	SHA256Hex   string
	SniffedMIME string
}

// ErrObjectNotFound is returned by Finalize when the key does not exist
// (upload never completed, or the client is probing a key it does not
// own — the caller maps this to a problem+json 422/404 as appropriate).
var ErrObjectNotFound = errors.New("storage: object not found")

// Finalize performs the HEAD and 512B sniff GET that verify an uploaded
// object before an asset is marked ready. It never reads the object body
// beyond sniffBytes.
func (c *Internal) Finalize(ctx context.Context, key string) (FinalizeInfo, error) {
	info, err := c.StatObject(ctx, c.Bucket, key, minio.StatObjectOptions{Checksum: true})
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" {
			return FinalizeInfo{}, ErrObjectNotFound
		}
		return FinalizeInfo{}, err
	}

	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(0, sniffBytes-1); err != nil {
		return FinalizeInfo{}, err
	}
	obj, err := c.GetObject(ctx, c.Bucket, key, opts)
	if err != nil {
		return FinalizeInfo{}, err
	}
	defer func() { _ = obj.Close() }()

	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(obj, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return FinalizeInfo{}, err
	}

	var sha256Hex string
	if info.ChecksumSHA256 != "" {
		raw, decErr := base64.StdEncoding.DecodeString(info.ChecksumSHA256)
		if decErr == nil {
			sha256Hex = hex.EncodeToString(raw)
		}
	}

	return FinalizeInfo{
		Size:        info.Size,
		SHA256Hex:   sha256Hex,
		SniffedMIME: SniffMIME(head[:n]),
	}, nil
}
