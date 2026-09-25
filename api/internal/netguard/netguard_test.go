package netguard

import (
	"net"
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
	}
	for _, c := range cases {
		got := isBlockedIP(net.ParseIP(c.ip))
		if got != c.blocked {
			t.Errorf("isBlockedIP(%s) = %v, want %v", c.ip, got, c.blocked)
		}
	}
}

func TestDialControlBlocksMetadataHostname(t *testing.T) {
	control := dialControl(Config{AllowedHosts: []string{"metadata.google.internal"}})
	if err := control("tcp", "metadata.google.internal:80", nil); err == nil {
		t.Fatal("expected metadata hostname to be blocked at dial time")
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
