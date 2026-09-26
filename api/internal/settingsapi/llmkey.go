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

// GetClaudeCliStatus implements gen.StrictServerInterface from the
// worker's heartbeat: the llm-cli sidecar is on llm_net, which only the
// worker joins, so the worker probes its /healthz and reports the
// result. It never errors: an offline worker or a missing sidecar is
// reported as installed:false with the reason.
func (h *SettingsAPI) GetClaudeCliStatus(ctx context.Context, _ gen.GetClaudeCliStatusRequestObject) (gen.GetClaudeCliStatusResponseObject, error) {
	resp := gen.GetClaudeCliStatus200JSONResponse{ToolsDisabled: true}
	w := h.workerView(ctx)
	if !w.fresh {
		detail := "worker offline: the claude CLI status is reported by the worker"
		resp.Detail = &detail
		return resp, nil
	}
	info, ok := w.providers[claudeCLIProvider]
	if !ok || info.CLI == nil {
		detail := "the worker did not report the llm-cli sidecar"
		resp.Detail = &detail
		return resp, nil
	}
	resp.Installed = info.CLI.Installed
	resp.Authenticated = info.CLI.Authenticated
	if info.CLI.Version != "" {
		version := info.CLI.Version
		resp.Version = &version
	}
	if info.CLI.Detail != "" {
		detail := info.CLI.Detail
		resp.Detail = &detail
	}
	return resp, nil
}

// claudeCLIProvider is the provider name the worker reports the sidecar under.
const claudeCLIProvider = "claude-cli"
