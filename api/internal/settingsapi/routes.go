package settingsapi

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/tenant"
)

// testCallTimeout bounds a synchronous /settings/llm/test call: how long
// the api waits for the worker to pick up and finish the probe step.
const testCallTimeout = 30 * time.Second

// GetLLMSettings implements gen.StrictServerInterface.
func (h *SettingsAPI) GetLLMSettings(ctx context.Context, _ gen.GetLLMSettingsRequestObject) (gen.GetLLMSettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	s, err := h.Store.Get(ctx, info.ID)
	if err != nil {
		return nil, err
	}
	return gen.GetLLMSettings200JSONResponse(gen.LLMSettings{
		Default:   s.Default,
		Overrides: toActionOverrides(s.Overrides),
		Providers: h.providerStatuses(ctx, tenantIDFromCtx(ctx), s),
	}), nil
}

// PutLLMSettings implements gen.StrictServerInterface. AC8: the very next
// resolved call (Registry.Resolve) sees the new provider, since
// registry.Store reads straight from llm_settings with no cache.
// Validated against registry.KnownProviders (the fixed provider-name
// set), not against which adapters this process happens to have
// constructed right now: Ollama, for instance, is always a valid
// selection even though its adapter lives in cmd/worker, not here.
func (h *SettingsAPI) PutLLMSettings(ctx context.Context, request gen.PutLLMSettingsRequestObject) (gen.PutLLMSettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	body := request.Body

	overrides := map[string]string{}
	if body.Overrides != nil {
		overrides = fromActionOverrides(*body.Overrides)
	}
	s := registry.Settings{Default: body.Default, Overrides: overrides}

	if !registry.IsKnownProvider(s.Default) {
		detail := fmt.Sprintf("unknown provider %q", s.Default)
		return gen.PutLLMSettings400ApplicationProblemPlusJSONResponse{
			Title: "invalid default provider", Status: 400, Detail: &detail,
		}, nil
	}
	for action, provider := range overrides {
		if !registry.IsKnownProvider(provider) {
			detail := fmt.Sprintf("unknown provider %q for action %q", provider, action)
			return gen.PutLLMSettings400ApplicationProblemPlusJSONResponse{
				Title: "invalid override provider", Status: 400, Detail: &detail,
			}, nil
		}
	}

	if err := h.Store.Put(ctx, info.ID, s); err != nil {
		return nil, err
	}
	h.recordSettingsAudit(ctx, "llm_settings.update")
	return gen.PutLLMSettings200JSONResponse(gen.LLMSettings{
		Default:   s.Default,
		Overrides: toActionOverrides(s.Overrides),
		Providers: h.providerStatuses(ctx, tenantIDFromCtx(ctx), s),
	}), nil
}

// TestLLMSettings implements gen.StrictServerInterface: probes the
// requested (or current default) provider with a fixed one-word prompt,
// never tenant content. The probe runs as a pipeline step in the worker,
// the process that runs every real LLM action, so the answer matches
// what the writer will see. A provider the worker reports unavailable
// fails fast with its reason instead of waiting on a step that cannot
// run. Rate limited per tenant, bounded by testCallTimeout, and
// audit-logged.
func (h *SettingsAPI) TestLLMSettings(ctx context.Context, request gen.TestLLMSettingsRequestObject) (gen.TestLLMSettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)

	if h.TestRateLimit != nil {
		allowed, err := h.TestRateLimit.Allow(ctx, info.ID.String())
		if err != nil {
			return nil, err
		}
		if !allowed {
			detail := "too many /settings/llm/test calls; try again shortly"
			return gen.TestLLMSettings429ApplicationProblemPlusJSONResponse{
				Title: "rate limited", Status: 429, Detail: &detail,
			}, nil
		}
	}

	providerName := ""
	if request.Body != nil && request.Body.Provider != nil {
		providerName = *request.Body.Provider
	}
	if providerName == "" {
		s, err := h.Store.Get(ctx, info.ID)
		if err != nil {
			return nil, err
		}
		providerName = s.Default
	}

	h.recordSettingsAudit(ctx, "llm_settings.test")

	fail := func(detail string) (gen.TestLLMSettingsResponseObject, error) {
		return gen.TestLLMSettings200JSONResponse{Provider: providerName, Ok: false, Detail: &detail}, nil
	}
	if !registry.IsKnownProvider(providerName) {
		return fail("unknown provider")
	}
	configured, _ := h.keyConfigured(ctx, info.ID.String(), providerName)
	if available, reason := h.availability(providerName, h.workerView(ctx), configured); !available {
		return fail(reason)
	}
	if h.Probe == nil {
		return fail("provider probe not configured")
	}

	testCtx, cancel := context.WithTimeout(ctx, testCallTimeout)
	defer cancel()
	sess, _ := authpkg.FromCtx(ctx)
	var createdBy *uuid.UUID
	if sess.UserID != uuid.Nil {
		createdBy = &sess.UserID
	}
	started := time.Now()
	ok, detail, err := h.Probe.Check(testCtx, info.ID, createdBy, providerName)
	if err != nil {
		return nil, err
	}
	// The round trip is reported for a reached provider either way, so the
	// settings card can show how slow a failing provider is too.
	latencyMs := int(time.Since(started).Milliseconds())
	result := gen.TestLLMSettings200JSONResponse{Provider: providerName, Ok: ok, LatencyMs: &latencyMs}
	if !ok {
		result.Detail = &detail
	}
	return result, nil
}
