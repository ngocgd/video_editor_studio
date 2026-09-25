// Package csrf implements a signed-token defence: the token is derived
// deterministically as HMAC-SHA256(pepper, "csrf|"+sessionToken), never
// stored or rotated. The client echoes it back on every unsafe request via
// X-CSRF-Token, and the Origin/Referer header is checked against an
// allowlist as defence in depth.
//
// Deriving instead of storing means GET /auth/csrf has no side effect (a
// previous design stored a random token per session and rotated it on
// every GET, which is a GET with a write side effect: a second tab, or a
// cross-site top-level navigation that Lax still sends the cookie on,
// would silently invalidate the SPA's token and lock it out with 403s).
package csrf

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
)

// HeaderName is the request header carrying the CSRF token.
const HeaderName = "X-CSRF-Token"

// Derive computes the CSRF token for a session token, given the
// process-wide pepper (never exposed to clients; callers pass the loaded
// envelope KEK bytes, domain-separated by the "csrf|" prefix so this use
// can never collide with the KEK's own AES-GCM usage).
func Derive(pepper []byte, sessionToken string) string {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte("csrf|" + sessionToken))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether token is the correct CSRF token for
// sessionToken, using a constant-time comparison.
func Verify(pepper []byte, sessionToken, token string) bool {
	if token == "" || sessionToken == "" {
		return false
	}
	expected := Derive(pepper, sessionToken)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(token)) == 1
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// CheckOrigin reports whether r's Origin (falling back to Referer) header
// matches one of the allowed origins. A request with neither header is
// rejected: browsers always send Origin on unsafe cross-origin requests,
// and same-origin fetches from the SPA always send it too.
func CheckOrigin(r *http.Request, allowedOrigins map[string]bool) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}
	if origin == "" {
		return false
	}
	return allowedOrigins[origin]
}

// SessionLookup returns the current request's session token (the
// plaintext cookie value, not a hash) and whether one is present. Kept as
// a narrow function type so this package does not need to import auth.
type SessionLookup func(r *http.Request) (sessionToken string, authenticated bool)

// Middleware enforces the signed-token check and Origin allowlist on
// every unsafe-method request. Safe methods (GET/HEAD/OPTIONS) and
// requests with no session (handled/rejected by the RBAC layer
// downstream) pass through unchecked.
func Middleware(pepper []byte, lookup SessionLookup, allowedOrigins map[string]bool, onReject http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isUnsafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			sessionToken, authenticated := lookup(r)
			if !authenticated {
				// No session at all: let the RBAC layer downstream produce
				// the 401 for routes that require one; public unsafe
				// routes (login) are exempt from CSRF by design since no
				// session/cookie exists yet to sign against.
				next.ServeHTTP(w, r)
				return
			}
			if !CheckOrigin(r, allowedOrigins) {
				onReject(w, r)
				return
			}
			if !Verify(pepper, sessionToken, r.Header.Get(HeaderName)) {
				onReject(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
