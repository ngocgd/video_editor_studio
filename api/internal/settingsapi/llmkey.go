package settingsapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/tenant"
)

// errSecretsWriteNotConfigured is returned when this process was wired
// without a SecretsWrite dependency (should not happen outside tests).
var errSecretsWriteNotConfigured = errors.New("settingsapi: SecretsWrite not configured")

// SecretsWriter is the write half of the envelope-encrypted secrets
// dependency (SecretsChecker above is the read half); narrowed to an
// interface so this package's test surface stays database-free.
type SecretsWriter interface {
	PutLLMAPIKey(ctx context.Context, tenantID uuid.UUID, provider, plaintext string) error
}

// ClaudeCLIStatusChecker abstracts the llm-cli sidecar's status surface.
type ClaudeCLIStatusChecker interface {
	// Status reports the sidecar's reachability. installed is true when
	// the sidecar answered at all (even if currently unhealthy);
	// authenticated is true only on a healthy response. detail explains
	// an unhealthy or unreachable state; version is empty (the sidecar's
	// /healthz surface does not report one).
	Status(ctx context.Context) (installed, authenticated bool, detail string, err error)
}

// PutLLMApiKey implements gen.StrictServerInterface: writes a BYOK
// provider API key, envelope-encrypted, write-only (never read back).
func (h *SettingsAPI) PutLLMApiKey(ctx context.Context, request gen.PutLLMApiKeyRequestObject) (gen.PutLLMApiKeyResponseObject, error) {
	info := tenant.MustFromCtx(ctx)

	if !registry.IsKnownProvider(request.Provider) {
		detail := "unknown provider"
		return gen.PutLLMApiKey400ApplicationProblemPlusJSONResponse{Title: "invalid provider", Status: http.StatusBadRequest, Detail: &detail}, nil
	}
	if request.Body == nil || request.Body.ApiKey == "" {
		detail := "apiKey is required"
		return gen.PutLLMApiKey400ApplicationProblemPlusJSONResponse{Title: "invalid request", Status: http.StatusBadRequest, Detail: &detail}, nil
	}
	if h.SecretsWrite == nil {
		return nil, errSecretsWriteNotConfigured
	}

	if err := h.SecretsWrite.PutLLMAPIKey(ctx, info.ID, request.Provider, request.Body.ApiKey); err != nil {
		return nil, err
	}
	h.recordSettingsAudit(ctx, "llm_settings.key_update")
	return gen.PutLLMApiKey204Response{}, nil
}

// GetClaudeCliStatus implements gen.StrictServerInterface. When the
// sidecar is unreachable this degrades gracefully to installed:false with
// an explanatory detail, never an error, since the sidecar is
// host-dependent per the phase 4 notes.
func (h *SettingsAPI) GetClaudeCliStatus(ctx context.Context, _ gen.GetClaudeCliStatusRequestObject) (gen.GetClaudeCliStatusResponseObject, error) {
	if h.ClaudeCLI == nil {
		detail := "llm-cli sidecar not configured"
		return gen.GetClaudeCliStatus200JSONResponse{Installed: false, Authenticated: false, ToolsDisabled: true, Detail: &detail}, nil
	}
	installed, authenticated, detail, err := h.ClaudeCLI.Status(ctx)
	if err != nil {
		reachDetail := "llm-cli sidecar not reachable"
		return gen.GetClaudeCliStatus200JSONResponse{Installed: false, Authenticated: false, ToolsDisabled: true, Detail: &reachDetail}, nil
	}
	resp := gen.GetClaudeCliStatus200JSONResponse{Installed: installed, Authenticated: authenticated, ToolsDisabled: true}
	if detail != "" {
		resp.Detail = &detail
	}
	return resp, nil
}
