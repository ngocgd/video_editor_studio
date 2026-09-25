package storage

import (
	"context"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Internal is the in-network client: full app credentials, used by the API
// and workers to read/write objects directly (GetObject with Range,
// PutObject multipart) and to issue internal presigned URLs whose TTL
// tracks the calling step's own timeout. It is never exposed to browsers.
type Internal struct {
	*minio.Client
	Bucket string
}

// New creates the Internal client from cfg.
func New(cfg Config) (*Internal, error) {
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, err
	}
	return &Internal{Client: c, Bucket: cfg.Bucket}, nil
}

// Ping implements health.Pinger by checking the configured bucket exists.
func (c *Internal) Ping(ctx context.Context) error {
	_, err := c.BucketExists(ctx, c.Bucket)
	return err
}

// PresignGet issues an internal presigned GET URL for key, valid for ttl.
// Callers size ttl to the calling step's own timeout plus a margin (the
// FFmpeg and Python workers add 10 minutes), never to a fixed constant.
func (c *Internal) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := c.PresignedGetObject(ctx, c.Bucket, key, ttl, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
