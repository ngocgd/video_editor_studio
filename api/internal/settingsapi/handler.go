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
	// WorkerStatus supplies every provider's availability and the claude
	// CLI status: the worker runs every llm.* step and is the only
	// process on llm_net, so this process's own adapters say nothing
	// about what will actually work.
	WorkerStatus workerStatusReader
	// TestRateLimit bounds POST /settings/llm/test per tenant (decision:
	// 5/min), since it spends real provider tokens/quota synchronously.
	TestRateLimit RateLimiter
	// SecretsWrite persists a BYOK provider API key (PutLLMApiKey). A nil
	// value fails PutLLMApiKey with an internal error rather than
	// panicking; every real wiring (cmd/api/main.go) sets it.
	SecretsWrite SecretsWriter
	// Probe runs the Test button's probe in the worker (see llmcheck).
	// A nil value reports the probe as unavailable.
	Probe ProviderProber
}

// ProviderProber runs one provider probe on behalf of a tenant.
type ProviderProber interface {
	Check(ctx context.Context, tenantID uuid.UUID, createdBy *uuid.UUID, provider string) (ok bool, detail string, err error)
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

// workerView is the worker's latest heartbeat, reduced to what the
// availability rules need.
type workerView struct {
	fresh     bool
	providers map[string]workerstatus.ProviderInfo
}

func (h *SettingsAPI) workerView(ctx context.Context) workerView {
	if h.WorkerStatus == nil {
		return workerView{}
	}
	ws, err := h.WorkerStatus.Get(ctx)
	if err != nil {
		return workerView{}
	}
	return workerView{fresh: ws.Fresh, providers: ws.Providers}
}

// availability decides whether the worker can run name for this tenant:
// either the worker reports the provider available from its own config
// (and, for claude-cli, a healthy sidecar), or the provider takes a
// tenant key and the tenant has stored one (the worker builds that
// adapter per call from the same secrets). The reason explains a "no".
func (h *SettingsAPI) availability(name string, w workerView, keyConfigured bool) (bool, string) {
	if !w.fresh {
		return false, "worker offline: no recent worker heartbeat, so no LLM step can run"
	}
	info, reported := w.providers[name]
	if reported && info.Available {
		return true, ""
	}
	if keyConfigured && h.takesTenantKey(name) {
		return true, ""
	}
	switch {
	case reported && info.DisabledReason != "":
		return false, info.DisabledReason
	case name == registry.ProviderOllama:
		return false, "ollama has no model configured (see OLLAMA_MODEL) or the GPU worker is off"
	case h.takesTenantKey(name):
		return false, "no API key configured for this provider"
	default:
		return false, "provider not configured on the worker"
	}
}

// takesTenantKey reports whether name can run on a tenant's own key.
func (h *SettingsAPI) takesTenantKey(name string) bool {
	if h.Registry == nil {
		return false
	}
	_, ok := h.Registry.Factories[name]
	return ok
}

// keyConfigured reports whether the tenant stored a key for name.
func (h *SettingsAPI) keyConfigured(ctx context.Context, tenantID, name string) (configured, known bool) {
	if h.Secrets == nil {
		return false, false
	}
	configured, err := h.Secrets.Configured(ctx, tenantID, name)
	if err != nil {
		return false, false
	}
	return configured, true
}

// providerStatuses reports every known provider name (registry.KnownProviders,
// not just the ones this process happens to have adapters for), plus any
// extra name a tenant's own settings reference, with availability as the
// worker sees it (see availability).
func (h *SettingsAPI) providerStatuses(ctx context.Context, tenantID string, s registry.Settings) []gen.ProviderStatus {
	names := map[string]struct{}{}
	for _, name := range registry.KnownProviders {
		names[name] = struct{}{}
	}
	names[s.Default] = struct{}{}
	for _, v := range s.Overrides {
		names[v] = struct{}{}
	}

	w := h.workerView(ctx)
	statuses := make([]gen.ProviderStatus, 0, len(names))
	for name := range names {
		status := gen.ProviderStatus{Name: name}
		configured, known := h.keyConfigured(ctx, tenantID, name)
		if known {
			status.Configured = &configured
		}
		available, reason := h.availability(name, w, configured)
		status.Available = available
		if !available {
			status.DisabledReason = &reason
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func tenantIDFromCtx(ctx context.Context) string {
	return tenant.MustFromCtx(ctx).ID.String()
}
