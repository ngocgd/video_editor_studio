package main

import (
	"net/http"
	"os"
	"strings"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/netguard"
	"loomtale/api/internal/providers/llm/anthropic"
	"loomtale/api/internal/providers/llm/claudecli"
	"loomtale/api/internal/providers/llm/gemini"
	"loomtale/api/internal/providers/llm/ollama"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/secretstr"
)

// buildRegistry constructs every LLM adapter this process can build from
// static config/secrets. A provider whose prerequisite (API key file,
// bearer token file) is absent is simply omitted from Providers, per the
// contract's "before phase 9 every engine returns an honest ... unless
// its weights/key exist" principle applied to LLM adapters too.
func buildRegistry(cfg config, queries *dbgen.Queries) (*registry.Registry, *registry.Store) {
	providers := map[string]llm.Provider{}

	guard := netguard.Client(netguard.Config{AllowedHosts: cfg.AllowedProviderHosts})
	if cfg.OllamaModel != "" {
		providers[registry.ProviderOllama] = ollama.New(cfg.OllamaURL, cfg.OllamaModel, guard)
	}

	if token := readSecretOrEmpty(cfg.LLMCLIBearerTokenPath); token != "" {
		saasMode := strings.EqualFold(cfg.AppMode, "saas")
		providers["claude-cli"] = claudecli.New(cfg.LLMCLIURL, secretstr.String(token), http.DefaultClient, saasMode)
	}

	if key := readSecretOrEmpty(cfg.AnthropicAPIKeyPath); key != "" {
		providers["anthropic-api"] = anthropic.New(secretstr.String(key), cfg.AnthropicModel, http.DefaultClient)
	}

	if key := readSecretOrEmpty(cfg.GeminiAPIKeyPath); key != "" {
		providers["gemini-api"] = gemini.New(secretstr.String(key), cfg.GeminiModel, http.DefaultClient)
	}

	store := &registry.Store{Queries: queries, FallbackDefault: "claude-cli"}
	return &registry.Registry{Store: store, Providers: providers, OllamaModel: cfg.OllamaModel}, store
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
