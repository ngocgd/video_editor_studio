// Package storage wraps the MinIO client used by the API and the health
// readiness check.
package storage

import (
	"context"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config holds the connection settings for the object store, loaded per
// package via caarlos0/env by the caller.
type Config struct {
	Endpoint  string `env:"MINIO_ENDPOINT,required"`
	AccessKey string `env:"MINIO_APP_ACCESS_KEY,required"`
	SecretKey string `env:"MINIO_APP_SECRET_KEY,required"`
	Bucket    string `env:"MINIO_BUCKET,required"`
	UseSSL    bool   `env:"MINIO_USE_SSL" envDefault:"false"`
}

// Client wraps *minio.Client so it satisfies health.Pinger.
type Client struct {
	*minio.Client
	Bucket string
}

// New creates a MinIO client from cfg.
func New(cfg Config) (*Client, error) {
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, err
	}
	return &Client{Client: c, Bucket: cfg.Bucket}, nil
}

// Ping implements health.Pinger by checking the configured bucket exists.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.BucketExists(ctx, c.Bucket)
	return err
}
