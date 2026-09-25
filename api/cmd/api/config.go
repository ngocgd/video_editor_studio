package main

import "time"

// config holds the API process settings, loaded via caarlos0/env.
type config struct {
	Addr            string        `env:"API_ADDR" envDefault:":8080"`
	LogLevel        string        `env:"API_LOG_LEVEL" envDefault:"info"`
	ShutdownTimeout time.Duration `env:"API_SHUTDOWN_TIMEOUT" envDefault:"25s"`
	DatabaseURL     string        `env:"DATABASE_URL,required"`
	Version         string        `env:"API_VERSION" envDefault:"dev"`

	// MasterKeyPath points at the mounted envelope-encryption KEK secret;
	// the process refuses to start if it is missing or malformed.
	MasterKeyPath string `env:"MASTER_KEY_PATH" envDefault:"/run/secrets/master_key"`

	// AllowedOrigins is the CSRF/CORS-style Origin allowlist, comma
	// separated (e.g. the SPA's own origin plus any preview deploys).
	AllowedOrigins []string `env:"ALLOWED_ORIGINS" envSeparator:"," envDefault:"http://127.0.0.1:8080"`

	// PublicURL, when set to an https URL, turns on HSTS; MediaOrigin is
	// interpolated into the CSP's img-src/media-src/connect-src.
	PublicURL   string `env:"PUBLIC_URL" envDefault:""`
	MediaOrigin string `env:"MEDIA_ORIGIN" envDefault:""`

	// TrustedProxyCIDRs lists the networks allowed to set
	// X-Forwarded-For/X-Real-IP; empty uses httpx's loopback+RFC1918
	// default, which covers Caddy's compose network out of the box.
	TrustedProxyCIDRs []string `env:"TRUSTED_PROXY_CIDRS" envSeparator:","`

	// ArgonMaxConcurrency bounds concurrent argon2id hashing (each call
	// allocates up to 64MiB); see api/internal/auth.HashLimiter.
	ArgonMaxConcurrency int `env:"ARGON2_MAX_CONCURRENCY" envDefault:"4"`
}
