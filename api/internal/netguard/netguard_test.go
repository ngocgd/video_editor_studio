package netguard

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateURLAllowsListedHost(t *testing.T) {
	cfg := Config{AllowedHosts: []string{"ollama.internal"}}
	if err := ValidateURL(cfg, "http://ollama.internal:11434/api/chat"); err != nil {
		t.Fatalf("expected allowed host to pass, got %v", err)
	}
}

func TestValidateURLBlocksUnlistedHost(t *testing.T) {
	cfg := Config{AllowedHosts: []string{"ollama.internal"}}
	if err := ValidateURL(cfg, "http://evil.example.com/"); err == nil {
		t.Fatal("expected unlisted host to be blocked")
	}
}

func TestValidateURLBlocksNonHTTPScheme(t *testing.T) {
	cfg := Config{AllowedHosts: []string{"ollama.internal"}}
	if err := ValidateURL(cfg, "file:///etc/passwd"); err == nil {
		t.Fatal("expected non-http(s) scheme to be blocked")
	}
}

func TestIsBlockedIPMetadataAndLinkLocal(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"169.254.169.254", true}, // cloud metadata
		{"169.254.0.1", true},
		{"0.0.0.0", true},
		{"127.0.0.1", false},
		{"10.0.0.5", false},
		{"8.8.8.8", false},
		{"fd00:ec2::254", true}, // AWS IPv6 IMDS
		{"::1", false},
	}
	for _, c := range cases {
		got := isBlockedIP(net.ParseIP(c.ip))
		if got != c.blocked {
			t.Errorf("isBlockedIP(%s) = %v, want %v", c.ip, got, c.blocked)
		}
	}
}

// TestDialControlBlocksMetadataHostname exercises dialControl's own
// hostname branch directly (a defense-in-depth path: in real use,
// net.Dialer.Control only ever sees a resolved IP, which is why
// Client's DialContext also enforces the allowlist pre-resolution — see
// TestClientDialContextBlocksUnlistedHostBeforeResolution below for the
// end-to-end proof of that).
func TestDialControlBlocksMetadataHostname(t *testing.T) {
	control := dialControl(Config{AllowedHosts: []string{"metadata.google.internal"}})
	if err := control("tcp", "metadata.google.internal:80", nil); err == nil {
		t.Fatal("expected metadata hostname to be blocked at dial time")
	}
}

// TestClientDialContextBlocksUnlistedHostBeforeResolution is the real
// enforcement point: Client's DialContext must reject a non-allowlisted
// host before any DNS lookup happens, not rely on dialControl (which
// only ever sees the already-resolved address).
func TestClientDialContextBlocksUnlistedHostBeforeResolution(t *testing.T) {
	client := Client(Config{AllowedHosts: []string{"127.0.0.1"}})
	_, err := client.Get("http://evil.invalid.example/")
	if err == nil {
		t.Fatal("expected the request to a non-allowlisted host to fail")
	}
	if !errors.Is(err, ErrBlockedHost) {
		t.Fatalf("expected ErrBlockedHost, got %v", err)
	}
}

func TestClientAllowsAllowlistedHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := Client(Config{AllowedHosts: []string{"127.0.0.1"}})
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestDialControlBlocksResolvedLinkLocalAddress(t *testing.T) {
	control := dialControl(Config{})
	if err := control("tcp", "169.254.169.254:80", nil); err == nil {
		t.Fatal("expected link-local resolved address to be blocked at dial time")
	}
}

func TestDialControlAllowsOrdinaryAddress(t *testing.T) {
	control := dialControl(Config{})
	if err := control("tcp", "10.0.0.5:11434", nil); err != nil {
		t.Fatalf("expected ordinary address to be allowed, got %v", err)
	}
}
