package httpx

import (
	"net"
	"net/http"
)

// ClientIP returns the caller's IP with any port stripped. It must run
// after chi's RealIP middleware, which rewrites r.RemoteAddr from a
// trusted proxy's X-Forwarded-For/X-Real-IP so this never trusts a
// client-supplied header directly.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
