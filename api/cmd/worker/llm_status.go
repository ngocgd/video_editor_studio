package main

import (
	"context"
	"time"

	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/llm/claudecli"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/providers/workerstatus"
)

// cliProbeTimeout keeps one claude-cli /healthz probe well inside the
// heartbeat interval.
const cliProbeTimeout = 2 * time.Second

const claudeCLIName = "claude-cli"

// cliStatusProber is the status half of the claude-cli adapter.
type cliStatusProber interface {
	Status(ctx context.Context) (claudecli.CLIStatus, error)
}

// llmProviderStatus reports the availability of every LLM adapter this
// worker built from static config. The worker runs every llm.* step and
// is the only process on llm_net, so its view is the one Settings > LLM
// shows. Ollama is left out: its availability follows GPU residency and
// the residency manager reports it.
func llmProviderStatus(ctx context.Context, providers map[string]llm.Provider, saasMode bool) map[string]workerstatus.ProviderInfo {
	out := map[string]workerstatus.ProviderInfo{}
	for name, p := range providers {
		if name == registry.ProviderOllama {
			continue
		}
		prober, ok := p.(cliStatusProber)
		if !ok {
			out[name] = workerstatus.ProviderInfo{Available: true}
			continue
		}
		out[name] = probeCLI(ctx, prober)
	}
	if _, ok := out[claudeCLIName]; !ok {
		reason := "the worker has no llm-cli bearer token configured (LLMCLI_BEARER_TOKEN_PATH)"
		if saasMode {
			reason = "claude-cli is local-only and disabled when APP_MODE=saas"
		}
		out[claudeCLIName] = workerstatus.ProviderInfo{
			DisabledReason: reason,
			CLI:            &workerstatus.CLIInfo{Detail: reason},
		}
	}
	return out
}

// probeCLI turns one sidecar /healthz probe into a provider status: the
// provider is available only when the sidecar answered healthy.
func probeCLI(ctx context.Context, prober cliStatusProber) workerstatus.ProviderInfo {
	probeCtx, cancel := context.WithTimeout(ctx, cliProbeTimeout)
	defer cancel()
	status, err := prober.Status(probeCtx)
	if err != nil {
		reason := "llm-cli sidecar not reachable (start it with COMPOSE_PROFILES=claude-cli)"
		return workerstatus.ProviderInfo{DisabledReason: reason, CLI: &workerstatus.CLIInfo{Detail: reason}}
	}
	info := workerstatus.ProviderInfo{
		Available: status.Installed && status.Authenticated,
		CLI: &workerstatus.CLIInfo{
			Installed:     status.Installed,
			Authenticated: status.Authenticated,
			Version:       status.Version,
			Detail:        status.Detail,
		},
	}
	if !info.Available {
		info.DisabledReason = "llm-cli sidecar is unhealthy"
		if status.Detail != "" {
			info.DisabledReason += ": " + status.Detail
		}
	}
	return info
}
