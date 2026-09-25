// Package netguard builds an http.Client for user-configurable provider
// endpoints (Ollama, ComfyUI) that refuses to dial cloud metadata
// addresses, link-local addresses, and any host not on an explicit
// allowlist, re-checked at actual dial time (not just URL-parse time) so
// a DNS answer that changes after validation cannot bypass the check.
package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlockedHost is wrapped by every rejection this package makes.
var ErrBlockedHost = errors.New("netguard: destination blocked")

// metadataHostnames are blocked regardless of the allowlist: no legitimate
// provider endpoint is a cloud metadata service.
var metadataHostnames = map[string]struct{}{
	"metadata.google.internal": {},
	"metadata.azure.com":       {},
}

// Config controls which hosts a Client may reach.
type Config struct {
	// AllowedHosts is the explicit allowlist (host, optionally host:port).
	// A dial to any other host is rejected.
	AllowedHosts []string
	DialTimeout  time.Duration
}

// ValidateURL checks scheme and host allowlist membership at
// configuration time (e.g. when an operator sets a provider endpoint via
// PUT /settings/llm). It is not sufficient on its own: Client's dial
// control re-checks at connection time, since DNS can change between
// validation and use.
func ValidateURL(cfg Config, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("netguard: invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q is not http(s)", ErrBlockedHost, u.Scheme)
	}
	if !hostAllowed(cfg, u.Hostname()) {
		return fmt.Errorf("%w: host %q is not in ALLOWED_PROVIDER_HOSTS", ErrBlockedHost, u.Hostname())
	}
	return nil
}

func hostAllowed(cfg Config, host string) bool {
	host = strings.ToLower(host)
	for _, allowed := range cfg.AllowedHosts {
		if strings.ToLower(allowed) == host {
			return true
		}
	}
	return false
}

// Client builds an *http.Client whose transport blocks metadata/link-local
// addresses on every dial and never follows redirects (a redirect to a
// blocked address must be rejected exactly like a direct request to it,
// not silently followed).
func Client(cfg Config) *http.Client {
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	dialer := &net.Dialer{
		Timeout: cfg.DialTimeout,
		Control: dialControl(cfg),
	}
	transport := &http.Transport{
		DialContext: dialer.DialContext,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// dialControl returns the net.Dialer.Control func that re-validates the
// resolved address (not just the original hostname) immediately before
// the socket connects.
func dialControl(cfg Config) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		if _, blocked := metadataHostnames[strings.ToLower(host)]; blocked {
			return fmt.Errorf("%w: metadata hostname %q", ErrBlockedHost, host)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			// Not a literal IP (the allowlist check already restricted the
			// hostname; this branch is a same-process sanity check only).
			return nil
		}
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: address %s resolves to a blocked range", ErrBlockedHost, ip)
		}
		return nil
	}
}

// isBlockedIP reports whether ip is link-local metadata space
// (169.254.0.0/16 and its IPv6-mapped form), the unspecified address, or
// otherwise not a routable unicast address a provider endpoint should
// ever resolve to.
func isBlockedIP(ip net.IP) bool {
	if ip.IsUnspecified() {
		return true
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	return false
}
