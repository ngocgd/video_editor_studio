package storage

import (
	"context"
	"errors"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MaxUploadTTL bounds every browser presigned URL/policy.
const MaxUploadTTL = 10 * time.Minute

// Browser signs against the publicly-reachable endpoint for direct
// browser uploads/downloads. Every URL it issues expires within
// MaxUploadTTL; it refuses to sign a POST for a key under the tenant's
// render/ prefix, which only workers may write to via Internal.
type Browser struct {
	client *minio.Client
	bucket string
}

// NewBrowser creates the Browser client from cfg.
func NewBrowser(cfg BrowserConfig) (*Browser, error) {
	c, err := minio.New(cfg.PublicEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, err
	}
	return &Browser{client: c, bucket: cfg.Bucket}, nil
}

// ErrRenderPrefixForbidden is returned by PresignPost for any key under
// the tenant's workers-only render/ prefix.
var ErrRenderPrefixForbidden = errors.New("storage: browser uploads are not permitted under the render/ prefix")

// PostPolicy is a browser-ready presigned POST: the client submits a
// multipart form to UploadURL with exactly these Fields plus the file.
type PostPolicy struct {
	UploadURL string
	Fields    map[string]string
	ExpiresAt time.Time
}

// PresignPost builds a presigned POST policy pinned to the exact key (not
// a prefix: minio's SetKey emits an "eq" condition), the declared content
// type, and a byte-size range. It refuses any key under render/.
func (b *Browser) PresignPost(ctx context.Context, tenantID, key, contentType string, minBytes, maxBytes int64) (PostPolicy, error) {
	if IsRenderKey(tenantID, key) {
		return PostPolicy{}, ErrRenderPrefixForbidden
	}

	expires := time.Now().UTC().Add(MaxUploadTTL)
	policy := minio.NewPostPolicy()
	if err := policy.SetBucket(b.bucket); err != nil {
		return PostPolicy{}, err
	}
	if err := policy.SetKey(key); err != nil {
		return PostPolicy{}, err
	}
	if err := policy.SetContentType(contentType); err != nil {
		return PostPolicy{}, err
	}
	if err := policy.SetContentLengthRange(minBytes, maxBytes); err != nil {
		return PostPolicy{}, err
	}
	if err := policy.SetExpires(expires); err != nil {
		return PostPolicy{}, err
	}

	uploadURL, formData, err := b.client.PresignedPostPolicy(ctx, policy)
	if err != nil {
		return PostPolicy{}, err
	}
	return PostPolicy{UploadURL: uploadURL.String(), Fields: formData, ExpiresAt: expires}, nil
}

// PresignGet issues a browser download URL for a ready asset, after the
// caller has already checked the asset's ownership. URLs expire after
// MaxUploadTTL.
//
// The architecture calls for flooring the signing time to a 5-minute
// boundary so repeated calls within the window return a byte-identical
// URL (letting the browser's HTTP cache treat it as the same resource).
// minio-go's Presign/PresignedGetObject always signs at time.Now() with no
// caller-supplied clock, so that flooring is not implemented here; doing
// it correctly needs a hand-rolled SigV4 signer, tracked as a follow-up
// rather than faked.
// versionID pins the exact object version an asset was finalized against
// (see storage.FinalizeInfo); pass "" to read the current version.
func (b *Browser) PresignGet(ctx context.Context, key, versionID string) (string, error) {
	u, err := b.client.PresignedGetObject(ctx, b.bucket, key, MaxUploadTTL, versionQueryParam(versionID))
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
