// Package csrf implements a synchronizer-token defence: the token's SHA-256
// is stored on the session row (set at login/rotation by package auth), the
// client echoes the plaintext token back on every unsafe request via
// X-CSRF-Token, and the Origin/Referer header is checked against an
// allowlist as defence in depth.
package csrf

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
)

// HeaderName is the request header carrying the plaintext CSRF token.
const HeaderName = "X-CSRF-Token"

// NewToken generates a fresh random CSRF token (plaintext, sent to the
// client) and its SHA-256 (stored on the session row).
func NewToken() (token string, hash []byte) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("csrf: crypto/rand unavailable: " + err.Error())
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, sum[:]
}

// Hash returns the SHA-256 of a plaintext token, for comparing an incoming
// header value against a stored hash.
func Hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Verify reports whether token hashes to storedHash, using a
// constant-time comparison.
func Verify(token string, storedHash []byte) bool {
	if token == "" || len(storedHash) == 0 {
		return false
	}
	got := Hash(token)
	return subtle.ConstantTimeCompare(got, storedHash) == 1
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

// StoredHash is implemented by the session accessor package (auth) so this
// package does not need to depend on it: the middleware receives the
// current session's stored CSRF hash through this narrow interface.
type SessionLookup func(r *http.Request) (storedHash []byte, authenticated bool)

// Middleware enforces the synchronizer token and Origin allowlist on every
// unsafe-method request. Safe methods (GET/HEAD/OPTIONS) and requests with
// no session (handled/rejected by the RBAC layer instead) pass through
// unchecked.
func Middleware(lookup SessionLookup, allowedOrigins map[string]bool, onReject http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isUnsafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			storedHash, authenticated := lookup(r)
			if !authenticated {
				// No session at all: let the RBAC layer downstream produce
				// the 401 for routes that require one; public unsafe
				// routes (login) are exempt from CSRF by design since no
				// session/cookie exists yet to fixate.
				next.ServeHTTP(w, r)
				return
			}
			if !CheckOrigin(r, allowedOrigins) {
				onReject(w, r)
				return
			}
			if !Verify(r.Header.Get(HeaderName), storedHash) {
				onReject(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
