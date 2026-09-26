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

	// RateLimitPerMinute is the general per-client-IP request budget
	// (burst and refill per minute). Only the integration test stacks
	// raise it, because the whole suite reaches the API from one IP.
	RateLimitPerMinute int `env:"API_RATE_LIMIT_PER_MINUTE" envDefault:"100"`

	// MediaRateLimitPerMinute is the separate per-client-IP budget of the
	// asset variant redirects. A storyboard page loads one per image tile
	// and audio clip (hundreds per episode), each a single indexed lookup
	// and a local URL signature, so they get their own larger bucket
	// instead of starving the general one, which login also draws from.
	MediaRateLimitPerMinute int `env:"API_MEDIA_RATE_LIMIT_PER_MINUTE" envDefault:"1200"`

	// AppMode gates the claude-cli provider (local-only per the
	// contract): "saas" disables it entirely, any other value (default
	// "local") leaves it available.
	AppMode string `env:"APP_MODE" envDefault:"local"`

	// AllowedProviderHosts is netguard's allowlist for user-configurable
	// provider endpoints (Ollama today; ComfyUI is dialed only from the
	// worker, not this process). Defaults to the compose-internal Ollama
	// hostname so a default install validates out of the box; an
	// operator pointing OLLAMA_URL somewhere else must also update this.
	AllowedProviderHosts []string `env:"ALLOWED_PROVIDER_HOSTS" envSeparator:"," envDefault:"ollama"`

	OllamaURL   string `env:"OLLAMA_URL" envDefault:"http://ollama:11434"`
	OllamaModel string `env:"OLLAMA_MODEL" envDefault:""`

	LLMCLIURL             string `env:"LLMCLI_URL" envDefault:"http://llm-cli:8090"`
	LLMCLIBearerTokenPath string `env:"LLMCLI_BEARER_TOKEN_PATH" envDefault:""`

	// AnthropicAPIKeyPath/GeminiAPIKeyPath, when set, wire a single
	// operator-provisioned key as the process-wide adapter; per-tenant
	// BYOK key resolution (phase 6+) supersedes this by constructing a
	// tenant-scoped registry.Registry instead.
	AnthropicAPIKeyPath string `env:"ANTHROPIC_API_KEY_PATH" envDefault:""`
	AnthropicModel      string `env:"ANTHROPIC_MODEL" envDefault:"claude-sonnet-5"`
	GeminiAPIKeyPath    string `env:"GEMINI_API_KEY_PATH" envDefault:""`
	GeminiModel         string `env:"GEMINI_MODEL" envDefault:"gemini-2.5-flash"`

	// PinCharacters adds a series' pinned character profiles to every
	// story LLM request; false is the rollback switch for the pinning.
	PinCharacters bool `env:"STORY_PIN_CHARACTERS" envDefault:"true"`
}
