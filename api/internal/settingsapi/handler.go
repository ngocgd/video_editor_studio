// Package settingsapi implements the /settings/llm slice of the
// generated strict server interface. It is a standalone package: per the
// plan's "api/cmd/* wiring is owned by phase 3" rule, this phase exports
// the constructor only; wiring it into cmd/api's composite server
// happens at merge.
package settingsapi

import (
	"context"

	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/tenant"
)

// SettingsAPI implements the settings/llm slice of gen.StrictServerInterface.
type SettingsAPI struct {
	Registry *registry.Registry
	Store    *registry.Store
	// Secrets reports whether a given provider name has a configured API
	// key (kind="llm_api_key" in the envelope-encrypted secrets table),
	// without ever returning the key itself.
	Secrets SecretsChecker
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

// providerStatuses lists every known provider (from h.Registry.Providers,
// which only contains successfully constructed adapters) plus whichever
// named providers the caller's settings reference but are not
// constructed, so a misconfigured override is visible rather than
// silently absent from the response.
func (h *SettingsAPI) providerStatuses(ctx context.Context, tenantID string, s registry.Settings) []gen.ProviderStatus {
	names := map[string]struct{}{}
	for name := range h.Registry.Providers {
		names[name] = struct{}{}
	}
	names[s.Default] = struct{}{}
	for _, v := range s.Overrides {
		names[v] = struct{}{}
	}

	statuses := make([]gen.ProviderStatus, 0, len(names))
	for name := range names {
		_, available := h.Registry.Providers[name]
		status := gen.ProviderStatus{Name: name, Available: available}
		if h.Secrets != nil {
			if configured, err := h.Secrets.Configured(ctx, tenantID, name); err == nil {
				status.Configured = &configured
			}
		}
		if !available {
			reason := "no adapter constructed for this provider (missing API key or self-check failed)"
			status.DisabledReason = &reason
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func tenantIDFromCtx(ctx context.Context) string {
	return tenant.MustFromCtx(ctx).ID.String()
}
