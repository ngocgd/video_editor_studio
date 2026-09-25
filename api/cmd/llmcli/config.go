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
	// file is mounted into the sidecar; no other secret ever is.
	OAuthTokenPath string `env:"CLAUDE_OAUTH_TOKEN_PATH,required"`
	// BearerTokenPath points at a compose secret shared only between
	// this sidecar and the worker, authenticating POST /v1/run.
	BearerTokenPath string `env:"LLMCLI_BEARER_TOKEN_PATH,required"`
}
