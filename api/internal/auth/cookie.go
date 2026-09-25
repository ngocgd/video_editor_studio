package auth

import (
	"net/http"
	"time"
)

// CookieName is prefixed __Host- (RFC 6265bis): the browser enforces
// Secure, Path=/ and no Domain attribute for it, which rules out a whole
// class of subdomain cookie-fixation and downgrade attacks.
const CookieName = "__Host-lt_sess"

const (
	// IdleLifetime is the rolling inactivity timeout.
	IdleLifetime = 12 * time.Hour
	// AbsoluteLifetime is the hard cap regardless of activity.
	AbsoluteLifetime = 7 * 24 * time.Hour
	// TouchInterval bounds how often last_seen_at is written.
	TouchInterval = 5 * time.Minute
)

// SetCookie writes the session cookie for token, valid until expiresAt.
// Secure is always set: localhost counts as a secure context for
// cookies, and the deployment target is always reached over https.
func SetCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
}

// ClearCookie expires the session cookie immediately (logout).
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// TokenFromRequest reads the session token from the request cookie, or ""
// if absent.
func TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
