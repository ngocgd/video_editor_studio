package main

import "time"

// config holds the llmcli sidecar's settings, loaded via caarlos0/env.
type config struct {
	Addr string `env:"LLMCLI_ADDR" envDefault:":8090"`

	Binary        string        `env:"LLMCLI_BINARY" envDefault:"claude"`
	PinnedVersion string        `env:"LLMCLI_PINNED_VERSION" envDefault:""`
	Model         string        `env:"LLMCLI_MODEL,required"`
	SystemPrompt  string        `env:"LLMCLI_SYSTEM_PROMPT,required"`
	WorkRoot      string        `env:"LLMCLI_WORK_ROOT" envDefault:"/tmp"`
	MaxConcurrent int           `env:"LLMCLI_MAX_CONCURRENT" envDefault:"2"`
	Timeout       time.Duration `env:"LLMCLI_TIMEOUT" envDefault:"10m"`

	// OAuthTokenPath points at the mounted compose secret from `claude
	// setup-token` (see deploy/compose.yml's llm-cli service). Only this
	// file is mounted into the sidecar; no other secret ever is. Not
	// required in HostFallback mode: the host's own `claude` login is
	// used instead (this process never reads that credential file).
	OAuthTokenPath string `env:"CLAUDE_OAUTH_TOKEN_PATH" envDefault:""`
	// BearerTokenPath points at a compose secret shared only between
	// this sidecar and the worker, authenticating POST /v1/run.
	BearerTokenPath string `env:"LLMCLI_BEARER_TOKEN_PATH,required"`

	// HostFallback runs this same binary as a host-side process (the
	// validated fallback for when the container-based OAuth-token auth
	// fails): it must bind to loopback only, and never reads
	// CLAUDE_OAUTH_TOKEN_PATH — the spawned `claude` uses whatever login
	// the host user already has (this process never touches that
	// credential file itself).
	HostFallback bool `env:"LLMCLI_HOST_FALLBACK" envDefault:"false"`
}
