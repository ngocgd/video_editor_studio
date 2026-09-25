package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/netguard"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/llm/anthropic"
	"loomtale/api/internal/providers/llm/claudecli"
	"loomtale/api/internal/providers/llm/gemini"
	"loomtale/api/internal/providers/llm/ollama"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/secretstr"
)

// providerDialTimeout/providerTLSHandshakeTimeout/providerResponseHeaderTimeout
// bound how long a request can hang before any response starts arriving;
// Client.Timeout itself is deliberately left unset on the returned
// client so a legitimate multi-minute streamed generation is never cut
// off mid-stream (a hung connection is caught by these instead).
const (
	providerDialTimeout           = 5 * time.Second
	providerTLSHandshakeTimeout   = 5 * time.Second
	providerResponseHeaderTimeout = 15 * time.Second
)

// timeoutHTTPClient builds an *http.Client with dial/TLS/response-header
// timeouts for a fixed, non-user-configurable host (anthropic-api,
// gemini-api): unlike netguard.Client, no allowlist applies here since
// these hosts are hardcoded in the adapter, not operator-supplied.
func timeoutHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: providerDialTimeout}
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   providerTLSHandshakeTimeout,
			ResponseHeaderTimeout: providerResponseHeaderTimeout,
		},
	}
}

// buildRegistry constructs every LLM adapter this process can build from
// static config/secrets. A provider whose prerequisite (API key file,
// bearer token file) is absent is simply omitted from Providers, per the
// contract's "before phase 9 every engine returns an honest ... unless
// its weights/key exist" principle applied to LLM adapters too.
func buildRegistry(cfg config, queries *dbgen.Queries) (*registry.Registry, *registry.Store, error) {
	providers := map[string]llm.Provider{}
	saasMode := strings.EqualFold(cfg.AppMode, "saas")

	guardCfg := netguard.Config{AllowedHosts: cfg.AllowedProviderHosts, DialTimeout: providerDialTimeout}
	if err := netguard.ValidateURL(guardCfg, cfg.OllamaURL); err != nil {
		// Fail fast rather than silently never constructing Ollama: an
		// operator who set OLLAMA_URL but forgot to add its host to
		// ALLOWED_PROVIDER_HOSTS should see why immediately, not a
		// generic "provider not configured" later.
		return nil, nil, fmt.Errorf("cmd/api: OLLAMA_URL is not netguard-allowed: %w", err)
	}
	guard := netguard.Client(guardCfg)
	if cfg.OllamaModel != "" {
		providers[registry.ProviderOllama] = ollama.New(cfg.OllamaURL, cfg.OllamaModel, guard)
	}

	// claude-cli is local-only by contract: never construct it at all in
	// SaaS mode (not just refuse at call time), so it never appears as
	// "available" in settings either.
	if !saasMode {
		if token := readSecretOrEmpty(cfg.LLMCLIBearerTokenPath); token != "" {
			providers["claude-cli"] = claudecli.New(cfg.LLMCLIURL, secretstr.String(token), timeoutHTTPClient(), saasMode)
		}
	}

	// Anthropic/gemini keys here are a single operator-provisioned,
	// process-wide credential (accepted only for APP_MODE=local per the
	// phase review: in saas every tenant would otherwise spend the same
	// operator key with no per-tenant attribution). Per-tenant BYOK
	// construction is phase 6's job, through SettingsAPI.Secrets.
	if !saasMode {
		if key := readSecretOrEmpty(cfg.AnthropicAPIKeyPath); key != "" {
			p := anthropic.New(secretstr.String(key), cfg.AnthropicModel, timeoutHTTPClient())
			p.Pricing = anthropicPricingFor(cfg.AnthropicModel)
			providers["anthropic-api"] = p
		}
		if key := readSecretOrEmpty(cfg.GeminiAPIKeyPath); key != "" {
			p := gemini.New(secretstr.String(key), cfg.GeminiModel, timeoutHTTPClient())
			p.Pricing = geminiPricingFor(cfg.GeminiModel)
			providers["gemini-api"] = p
		}
	}

	store := &registry.Store{Queries: queries, FallbackDefault: "claude-cli"}
	return &registry.Registry{Store: store, Providers: providers, OllamaModel: cfg.OllamaModel}, store, nil
}

// anthropicPricingFor/geminiPricingFor hold per-million-token USD prices
// so CostUSD is never silently reported as 0. Prices drift over time;
// an unrecognized model falls back to the configured model's nearest
// published tier rather than zero, so cost tracking degrades to
// approximate instead of blind.
func anthropicPricingFor(model string) anthropic.Pricing {
	switch {
	case strings.Contains(model, "opus"):
		return anthropic.Pricing{InPerMTok: 15.00, OutPerMTok: 75.00}
	case strings.Contains(model, "haiku"):
		return anthropic.Pricing{InPerMTok: 0.80, OutPerMTok: 4.00}
	default: // sonnet tier, the configured default
		return anthropic.Pricing{InPerMTok: 3.00, OutPerMTok: 15.00}
	}
}

func geminiPricingFor(model string) gemini.Pricing {
	switch {
	case strings.Contains(model, "pro"):
		return gemini.Pricing{InPerMTok: 1.25, OutPerMTok: 5.00}
	default: // flash tier, the configured default
		return gemini.Pricing{InPerMTok: 0.30, OutPerMTok: 2.50}
	}
}

func readSecretOrEmpty(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
