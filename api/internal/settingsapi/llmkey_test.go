package settingsapi

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/providers/workerstatus"
)

type fakeSecretsWriter struct {
	calls     int
	lastKey   string
	returnErr error
}

func (f *fakeSecretsWriter) PutLLMAPIKey(_ context.Context, _ uuid.UUID, _, plaintext string) error {
	f.calls++
	f.lastKey = plaintext
	return f.returnErr
}

func TestPutLLMApiKeyRejectsUnknownProvider(t *testing.T) {
	writer := &fakeSecretsWriter{}
	h := &SettingsAPI{SecretsWrite: writer}
	resp, err := h.PutLLMApiKey(ctxWithTenant(), gen.PutLLMApiKeyRequestObject{
		Provider: "not-a-real-provider",
		Body:     &gen.LLMApiKeyRequest{ApiKey: "sk-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.PutLLMApiKey400ApplicationProblemPlusJSONResponse); !ok {
		t.Fatalf("expected 400, got %#v", resp)
	}
	if writer.calls != 0 {
		t.Fatal("SecretsWrite must not be called for an unknown provider")
	}
}

func TestPutLLMApiKeyRejectsEmptyKey(t *testing.T) {
	writer := &fakeSecretsWriter{}
	h := &SettingsAPI{SecretsWrite: writer}
	resp, err := h.PutLLMApiKey(ctxWithTenant(), gen.PutLLMApiKeyRequestObject{
		Provider: "anthropic-api",
		Body:     &gen.LLMApiKeyRequest{ApiKey: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.PutLLMApiKey400ApplicationProblemPlusJSONResponse); !ok {
		t.Fatalf("expected 400, got %#v", resp)
	}
	if writer.calls != 0 {
		t.Fatal("SecretsWrite must not be called for an empty key")
	}
}

func TestPutLLMApiKeyStoresKeyAndReturns204(t *testing.T) {
	writer := &fakeSecretsWriter{}
	h := &SettingsAPI{SecretsWrite: writer, Queries: nil}
	resp, err := h.PutLLMApiKey(ctxWithTenant(), gen.PutLLMApiKeyRequestObject{
		Provider: "anthropic-api",
		Body:     &gen.LLMApiKeyRequest{ApiKey: "sk-super-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.PutLLMApiKey204Response); !ok {
		t.Fatalf("expected 204, got %#v", resp)
	}
	if writer.calls != 1 || writer.lastKey != "sk-super-secret" {
		t.Fatalf("expected SecretsWrite.PutLLMAPIKey to be called once with the key, got calls=%d lastKey=%q", writer.calls, writer.lastKey)
	}
}

// TestPutLLMApiKeyResponseNeverContainsTheKey is the write-only
// regression test: nothing in any possible response object carries the
// plaintext or ciphertext back to the caller.
func TestPutLLMApiKeyResponseNeverContainsTheKey(t *testing.T) {
	writer := &fakeSecretsWriter{}
	h := &SettingsAPI{SecretsWrite: writer}
	resp, err := h.PutLLMApiKey(ctxWithTenant(), gen.PutLLMApiKeyRequestObject{
		Provider: "anthropic-api",
		Body:     &gen.LLMApiKeyRequest{ApiKey: "sk-super-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.PutLLMApiKey204Response); !ok {
		t.Fatalf("expected an empty 204 response body, got %#v", resp)
	}
}

func cliWorker(fresh bool, info workerstatus.ProviderInfo) fakeWorkerStatus {
	return fakeWorkerStatus{status: workerstatus.Status{
		Fresh:     fresh,
		Providers: map[string]workerstatus.ProviderInfo{"claude-cli": info},
	}}
}

// The claude CLI status comes from the worker's heartbeat: the api has
// no network path to the llm-cli sidecar.
func TestGetClaudeCliStatusHealthyFromWorker(t *testing.T) {
	h := &SettingsAPI{WorkerStatus: cliWorker(true, workerstatus.ProviderInfo{
		Available: true,
		CLI:       &workerstatus.CLIInfo{Installed: true, Authenticated: true, Version: "2.1.282 (Claude Code)"},
	})}
	resp, err := h.GetClaudeCliStatus(ctxWithTenant(), gen.GetClaudeCliStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := resp.(gen.GetClaudeCliStatus200JSONResponse)
	if !ok {
		t.Fatalf("expected 200, got %#v", resp)
	}
	if !status.Installed || !status.Authenticated || !status.ToolsDisabled {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Version == nil || *status.Version != "2.1.282 (Claude Code)" {
		t.Fatalf("version = %v", status.Version)
	}
}

func TestGetClaudeCliStatusReportsUnhealthySidecar(t *testing.T) {
	h := &SettingsAPI{WorkerStatus: cliWorker(true, workerstatus.ProviderInfo{
		CLI: &workerstatus.CLIInfo{Installed: true, Detail: "no CLAUDE_CODE_OAUTH_TOKEN configured"},
	})}
	resp, _ := h.GetClaudeCliStatus(ctxWithTenant(), gen.GetClaudeCliStatusRequestObject{})
	status := resp.(gen.GetClaudeCliStatus200JSONResponse)
	if !status.Installed || status.Authenticated || status.Detail == nil || *status.Detail == "" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestGetClaudeCliStatusDegradesGracefullyWhenWorkerOffline(t *testing.T) {
	for name, h := range map[string]*SettingsAPI{
		"stale heartbeat": {WorkerStatus: cliWorker(false, workerstatus.ProviderInfo{Available: true, CLI: &workerstatus.CLIInfo{Installed: true, Authenticated: true}})},
		"no status":       {WorkerStatus: fakeWorkerStatus{err: workerstatus.ErrNoStatus}},
		"not wired":       {},
	} {
		resp, err := h.GetClaudeCliStatus(ctxWithTenant(), gen.GetClaudeCliStatusRequestObject{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		status, ok := resp.(gen.GetClaudeCliStatus200JSONResponse)
		if !ok || status.Installed || status.Authenticated || status.Detail == nil {
			t.Fatalf("%s: expected installed:false with a detail, got %#v", name, resp)
		}
	}
}
