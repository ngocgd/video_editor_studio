package settingsapi

import (
	"context"
	"fmt"

	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/tenant"
)

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
func (h *SettingsAPI) PutLLMSettings(ctx context.Context, request gen.PutLLMSettingsRequestObject) (gen.PutLLMSettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	body := request.Body

	overrides := map[string]string{}
	if body.Overrides != nil {
		overrides = fromActionOverrides(*body.Overrides)
	}
	s := registry.Settings{Default: body.Default, Overrides: overrides}

	if _, ok := h.Registry.Providers[s.Default]; !ok {
		detail := fmt.Sprintf("unknown or unconfigured provider %q", s.Default)
		return gen.PutLLMSettings400ApplicationProblemPlusJSONResponse{
			Title: "invalid default provider", Status: 400, Detail: &detail,
		}, nil
	}
	for action, provider := range overrides {
		if _, ok := h.Registry.Providers[provider]; !ok {
			detail := fmt.Sprintf("unknown or unconfigured provider %q for action %q", provider, action)
			return gen.PutLLMSettings400ApplicationProblemPlusJSONResponse{
				Title: "invalid override provider", Status: 400, Detail: &detail,
			}, nil
		}
	}

	if err := h.Store.Put(ctx, info.ID, s); err != nil {
		return nil, err
	}
	return gen.PutLLMSettings200JSONResponse(gen.LLMSettings{
		Default:   s.Default,
		Overrides: toActionOverrides(s.Overrides),
		Providers: h.providerStatuses(ctx, tenantIDFromCtx(ctx), s),
	}), nil
}

// TestLLMSettings implements gen.StrictServerInterface: runs a minimal,
// fixed 1-token prompt against the requested (or current default)
// provider and reports whether it succeeded, with no tenant-authored
// content ever sent (the test prompt is a fixed server string, exactly
// like every other System value).
func (h *SettingsAPI) TestLLMSettings(ctx context.Context, request gen.TestLLMSettingsRequestObject) (gen.TestLLMSettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)

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

	provider, ok := h.Registry.Providers[providerName]
	if !ok {
		detail := "provider not configured"
		return gen.TestLLMSettings200JSONResponse{Provider: providerName, Ok: false, Detail: &detail}, nil
	}

	_, err := provider.Generate(ctx, llm.Request{
		Messages:  []llm.Message{{Role: "user", Text: "Reply with a single word: OK."}},
		MaxTokens: 8,
	})
	if err != nil {
		detail := err.Error()
		return gen.TestLLMSettings200JSONResponse{Provider: providerName, Ok: false, Detail: &detail}, nil
	}
	return gen.TestLLMSettings200JSONResponse{Provider: providerName, Ok: true}, nil
}
