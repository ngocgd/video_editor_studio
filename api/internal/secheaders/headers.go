// Package secheaders sets the response security headers, matching the
// values Caddy sends for the SPA so the API and the SPA agree byte for
// byte. The CSP is enforcing from day one: there is no
// Content-Security-Policy-Report-Only escape hatch.
package secheaders

import (
	"fmt"
	"net/http"
	"strings"
)

// Config controls the two headers whose value depends on the deployment
// (media origin allowlisted into the CSP, HSTS only over https).
type Config struct {
	MediaOrigin string
	PublicURL   string
}

// CSP builds the Content-Security-Policy header value. Its shape matches
// deploy/caddy/Caddyfile directive-for-directive (Caddy interpolates
// {$MEDIA_ORIGIN} from the same env var), so the API and the SPA always
// send byte-identical headers. MediaOrigin may be "" (a trailing space
// before the next directive's semicolon is valid CSP syntax either way).
func (c Config) CSP() string {
	directives := []string{
		"default-src 'self'",
		"script-src 'self'",
		"object-src 'none'",
		"img-src 'self' blob: data: " + c.MediaOrigin,
		"media-src 'self' blob: " + c.MediaOrigin,
		"connect-src 'self' " + c.MediaOrigin,
		"font-src 'self'",
		// Radix (floating-ui positioning), sonner (runtime style tag) and
		// TipTap/ProseMirror write inline styles; script execution stays
		// locked to 'self' with Trusted Types, so the residual risk is CSS
		// injection only. Revisit if the shell is ever server-rendered
		// with nonces.
		"style-src 'self' 'unsafe-inline'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"require-trusted-types-for 'script'",
		"trusted-types default dompurify",
	}
	return strings.Join(directives, "; ")
}

func (c Config) isHTTPS() bool {
	return strings.HasPrefix(c.PublicURL, "https://")
}

// Middleware sets the security headers on every response.
func (c Config) Middleware(next http.Handler) http.Handler {
	csp := c.CSP()
	hsts := c.isHTTPS()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if hsts {
			h.Set("Strict-Transport-Security", fmt.Sprintf("max-age=%d; includeSubDomains", 31536000))
		}
		next.ServeHTTP(w, r)
	})
}
