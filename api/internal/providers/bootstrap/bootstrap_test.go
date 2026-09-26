package bootstrap

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"loomtale/api/internal/providers/llm/claudecli"
)

// The claude-cli adapter must not use a response-header timeout: a long
// generation would otherwise fail before the sidecar answers.
func TestSidecarHTTPClientHasNoResponseHeaderTimeout(t *testing.T) {
	transport, ok := SidecarHTTPClient().Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected an *http.Transport")
	}
	if transport.ResponseHeaderTimeout != 0 {
		t.Fatalf("ResponseHeaderTimeout = %v, want none", transport.ResponseHeaderTimeout)
	}
	if transport.DialContext == nil {
		t.Fatal("expected a bounded dialer")
	}
}

// Build gives the claude-cli adapter the sidecar client, not the
// short-header-timeout client the hosted APIs use.
func TestBuildUsesSidecarClientForClaudeCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bearer")
	if err := os.WriteFile(path, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, _, err := Build(Config{
		AppMode:               "local",
		AllowedProviderHosts:  []string{"ollama"},
		OllamaURL:             "http://ollama:11434",
		LLMCLIURL:             "http://llm-cli:8090",
		LLMCLIBearerTokenPath: path,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reg.Providers["claude-cli"].(*claudecli.Provider)
	if !ok {
		t.Fatal("claude-cli adapter not built")
	}
	transport, ok := p.Client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected an *http.Transport")
	}
	if transport.ResponseHeaderTimeout != 0 {
		t.Fatalf("claude-cli client ResponseHeaderTimeout = %v, want none", transport.ResponseHeaderTimeout)
	}
}
