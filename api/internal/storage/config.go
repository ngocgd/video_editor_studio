// Package storage wraps two MinIO clients with different trust levels:
// Internal (in-network, full app credentials, used by the API and
// workers to read/write objects directly and to issue step-scoped
// internal presigned URLs) and Browser (signs against the
// publicly-reachable endpoint, short-lived, POST-policy uploads and
// GET downloads only, never allowed to touch the workers-only render/
// prefix).
package storage

// Config configures the Internal client, reached over the compose network.
type Config struct {
	Endpoint  string `env:"MINIO_ENDPOINT,required"`
	AccessKey string `env:"MINIO_APP_ACCESS_KEY,required"`
	SecretKey string `env:"MINIO_APP_SECRET_KEY,required"`
	Bucket    string `env:"MINIO_BUCKET,required"`
	UseSSL    bool   `env:"MINIO_USE_SSL" envDefault:"false"`
	Region    string `env:"MINIO_REGION" envDefault:"us-east-1"`
}

// BrowserConfig configures the Browser client, whose Endpoint must be the
// host the browser itself will connect to: MinIO's SigV4 presigned URLs
// sign the Host header, so a URL signed against the in-network endpoint
// would fail verification when hit from outside the compose network.
type BrowserConfig struct {
	PublicEndpoint string `env:"MINIO_PUBLIC_ENDPOINT" envDefault:"127.0.0.1:9000"`
	AccessKey      string `env:"MINIO_APP_ACCESS_KEY,required"`
	SecretKey      string `env:"MINIO_APP_SECRET_KEY,required"`
	Bucket         string `env:"MINIO_BUCKET,required"`
	UseSSL         bool   `env:"MINIO_PUBLIC_USE_SSL" envDefault:"false"`
	// Region must be set explicitly: PublicEndpoint is only reachable from
	// the browser, never from inside the api container itself, so
	// minio-go must never be allowed to auto-detect it with a live
	// GetBucketLocation call against PublicEndpoint (which would just
	// hang/fail from inside the container).
	Region string `env:"MINIO_REGION" envDefault:"us-east-1"`
}
