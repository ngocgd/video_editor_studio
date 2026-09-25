// Package settingsapi implements the /settings/llm slice of the
// generated strict server interface. It is a standalone package: per the
// plan's "api/cmd/* wiring is owned by phase 3" rule, this phase exports
// the constructor only; wiring it into cmd/api's composite server
// happens at merge.
package settingsapi

import (
	"context"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/providers/workerstatus"
	"loomtale/api/internal/tenant"
)

// SettingsAPI implements the settings/llm slice of gen.StrictServerInterface.
type SettingsAPI struct {
	Registry *registry.Registry
	// Store is narrowed to settingsStore (registry.Store's Get/Put
	// methods) so this package is testable against a fake without a
	// database; Queries below carries the one DB dependency audit
	// logging still needs.
	Store   settingsStore
	Queries *dbgen.Queries
	// Secrets reports whether a given provider name has a configured API
	// key (kind="llm_api_key" in the envelope-encrypted secrets table),
	// without ever returning the key itself.
	Secrets SecretsChecker
	// WorkerStatus supplies Ollama's real availability (the process that
	// actually calls it, per the phase review): this process's own
	// Registry.Providers only ever contains claude-cli/anthropic-api/
	// gemini-api, never ollama (that adapter lives in cmd/worker).
	WorkerStatus workerStatusReader
	// TestRateLimit bounds POST /settings/llm/test per tenant (decision:
	// 5/min), since it spends real provider tokens/quota synchronously.
	TestRateLimit RateLimiter
}

// settingsStore is the subset of *registry.Store this package depends
// on.
type settingsStore interface {
	Get(ctx context.Context, tenantID uuid.UUID) (registry.Settings, error)
	Put(ctx context.Context, tenantID uuid.UUID, s registry.Settings) error
}

// RateLimiter is the subset of *ratelimit.DBBucket TestLLMSettings
// depends on.
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

var _ settingsStore = (*registry.Store)(nil)

// workerStatusReader is narrowed to an interface (mirroring
// pipelineapi's own workerStatusReader) so this package does not import
// a database-backed concrete type into its test surface.
type workerStatusReader interface {
	Get(ctx context.Context) (workerstatus.Status, error)
}

// SecretsChecker abstracts the envelope-encrypted secrets lookup so this
// package does not depend on the phase 2 secrets package directly beyond
// this one method.
type SecretsChecker interface {
	Configured(ctx context.Context, tenantID, provider string) (bool, error)
}

func toActionOverrides(m map[string]string) gen.LLMActionOverrides {
	out := gen.LLMActionOverrides{}
	if v, ok := m[string(registry.ActionOutline)]; ok {
		out.Outline = &v
	}
	if v, ok := m[string(registry.ActionDraft)]; ok {
		out.Draft = &v
	}
	if v, ok := m[string(registry.ActionRewrite)]; ok {
		out.Rewrite = &v
	}
	if v, ok := m[string(registry.ActionTranslate)]; ok {
		out.Translate = &v
	}
	if v, ok := m[string(registry.ActionSceneSplit)]; ok {
		out.SceneSplit = &v
	}
	if v, ok := m[string(registry.ActionSummary)]; ok {
		out.Summary = &v
	}
	return out
}

func fromActionOverrides(o gen.LLMActionOverrides) map[string]string {
	out := map[string]string{}
	set := func(key string, v *string) {
		if v != nil && *v != "" {
			out[key] = *v
		}
	}
	set(string(registry.ActionOutline), o.Outline)
	set(string(registry.ActionDraft), o.Draft)
	set(string(registry.ActionRewrite), o.Rewrite)
	set(string(registry.ActionTranslate), o.Translate)
	set(string(registry.ActionSceneSplit), o.SceneSplit)
	set(string(registry.ActionSummary), o.Summary)
	return out
}

// providerStatuses reports every known provider name (registry.KnownProviders,
// not just the ones this process happens to have adapters for), plus any
// extra name a tenant's own settings reference. Availability for
// ollama comes from WorkerStatus (the worker is the process that
// actually calls it); every other provider's availability comes from
// this process's own Registry.Providers, since those adapters really do
// live and get called here.
func (h *SettingsAPI) providerStatuses(ctx context.Context, tenantID string, s registry.Settings) []gen.ProviderStatus {
	names := map[string]struct{}{}
	for _, name := range registry.KnownProviders {
		names[name] = struct{}{}
	}
	names[s.Default] = struct{}{}
	for _, v := range s.Overrides {
		names[v] = struct{}{}
	}

	var workerProviders map[string]workerstatus.ProviderInfo
	var workerFresh bool
	if h.WorkerStatus != nil {
		if ws, err := h.WorkerStatus.Get(ctx); err == nil {
			workerProviders = ws.Providers
			workerFresh = ws.Fresh
		}
	}

	statuses := make([]gen.ProviderStatus, 0, len(names))
	for name := range names {
		status := gen.ProviderStatus{Name: name}
		if name == registry.ProviderOllama {
			status.Available = workerFresh && workerProviders[registry.ProviderOllama].Available
			if !status.Available {
				reason := "worker_offline or ollama has no model configured (see OLLAMA_MODEL)"
				if workerFresh {
					if info, ok := workerProviders[registry.ProviderOllama]; ok && info.DisabledReason != "" {
						reason = info.DisabledReason
					}
				}
				status.DisabledReason = &reason
			}
		} else {
			_, available := h.Registry.Providers[name]
			status.Available = available
			if !available {
				reason := "no adapter constructed for this provider (missing API key, disabled in saas mode, or self-check failed)"
				status.DisabledReason = &reason
			}
		}
		if h.Secrets != nil {
			if configured, err := h.Secrets.Configured(ctx, tenantID, name); err == nil {
				status.Configured = &configured
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func tenantIDFromCtx(ctx context.Context) string {
	return tenant.MustFromCtx(ctx).ID.String()
}
