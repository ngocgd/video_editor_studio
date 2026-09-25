package httpx

import (
	"net"
	"net/http"
	"strings"
)

// defaultTrustedProxyCIDRs covers loopback and the RFC1918 ranges Docker
// uses for its bridge networks; Caddy (the only thing ever directly
// connecting to the API) always lands in one of these.
var defaultTrustedProxyCIDRs = []string{
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
}

// RealIP rewrites r.RemoteAddr from X-Forwarded-For/X-Real-IP, but only
// when the immediate peer (the actual TCP connection, before any
// rewriting) is a trusted proxy. Unlike chi's middleware.RealIP, an
// untrusted peer's forwarded-for headers are never honored, closing the
// IP-spoofing hole those headers otherwise open for every rate limiter
// and audit log entry keyed by client IP.
func RealIP(trustedCIDRs []string) func(http.Handler) http.Handler {
	if len(trustedCIDRs) == 0 {
		trustedCIDRs = defaultTrustedProxyCIDRs
	}
	nets := make([]*net.IPNet, 0, len(trustedCIDRs))
	for _, cidr := range trustedCIDRs {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			nets = append(nets, n)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if peerTrusted(r.RemoteAddr, nets) {
				if ip := forwardedFor(r); ip != "" {
					r.RemoteAddr = ip
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func peerTrusted(remoteAddr string, nets []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func forwardedFor(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// The leftmost entry is the original client, per RFC 7239's usual
		// convention; it is only trusted here because the immediate peer
		// (the proxy that set/forwarded this header) was already verified.
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if net.ParseIP(first) != nil {
			return first
		}
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" && net.ParseIP(xrip) != nil {
		return xrip
	}
	return ""
}
