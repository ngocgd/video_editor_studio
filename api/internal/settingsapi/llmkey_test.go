package settingsapi

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"loomtale/api/internal/httpapi/gen"
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

type fakeClaudeCLIStatus struct {
	installed, authenticated bool
	detail                   string
	err                      error
}

func (f fakeClaudeCLIStatus) Status(context.Context) (bool, bool, string, error) {
	return f.installed, f.authenticated, f.detail, f.err
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

func TestGetClaudeCliStatusHealthy(t *testing.T) {
	h := &SettingsAPI{ClaudeCLI: fakeClaudeCLIStatus{installed: true, authenticated: true}}
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
}

func TestGetClaudeCliStatusDegradesGracefullyWhenUnreachable(t *testing.T) {
	h := &SettingsAPI{ClaudeCLI: fakeClaudeCLIStatus{err: context.DeadlineExceeded}}
	resp, err := h.GetClaudeCliStatus(ctxWithTenant(), gen.GetClaudeCliStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := resp.(gen.GetClaudeCliStatus200JSONResponse)
	if !ok {
		t.Fatalf("expected 200, got %#v", resp)
	}
	if status.Installed || status.Authenticated {
		t.Fatalf("expected installed:false, authenticated:false when unreachable, got %+v", status)
	}
	if status.Detail == nil || *status.Detail == "" {
		t.Fatal("expected a detail explaining the sidecar is not reachable")
	}
}

func TestGetClaudeCliStatusNilCheckerDegradesGracefully(t *testing.T) {
	h := &SettingsAPI{}
	resp, err := h.GetClaudeCliStatus(ctxWithTenant(), gen.GetClaudeCliStatusRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := resp.(gen.GetClaudeCliStatus200JSONResponse)
	if !ok || status.Installed {
		t.Fatalf("expected installed:false with no ClaudeCLI dependency configured, got %#v", resp)
	}
}
