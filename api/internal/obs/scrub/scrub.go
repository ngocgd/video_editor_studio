// Package scrub redacts secrets and capability URLs before they reach a
// log sink. It is used both as an slog.HandlerOptions.ReplaceAttr and
// exported standalone for non-slog output (FFmpeg stderr, step logs).
package scrub

import (
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

// keyPattern matches attribute keys that must never be logged, regardless
// of value shape: tokens, secrets, passwords, cookies, auth headers and
// anything ending in "key".
var keyPattern = regexp.MustCompile(`(?i)token|secret|password|cookie|authorization|key`)

const redacted = "[REDACTED]"

// sensitiveQueryParams are capability-granting query parameters: their
// mere presence in a logged URL is equivalent to logging a bearer
// credential, so the whole query string is stripped rather than just the
// one parameter.
var sensitiveQueryParams = []string{"X-Amz-Signature", "X-Amz-Credential", "upload_id", "upload_protocol"}

// ReplaceAttr is an slog.HandlerOptions.ReplaceAttr that drops the value of
// any attribute whose key looks secret-shaped, and scrubs capability URLs
// out of every string value (attribute keys are checked case-insensitively
// against keyPattern; values are always passed through URL as a defence in
// depth in case a secret lands under an innocuous key).
func ReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	if keyPattern.MatchString(a.Key) {
		a.Value = slog.StringValue(redacted)
		return a
	}
	if a.Value.Kind() == slog.KindString {
		a.Value = slog.StringValue(URL(a.Value.String()))
	}
	return a
}

// URL rewrites s to strip any sensitive query parameter if s parses as a
// URL containing one; otherwise s is returned unchanged. It is exported so
// FFmpeg stderr and worker step logs, which are not slog.Attr values, can
// scrub capability URLs the same way.
func URL(s string) string {
	if !strings.Contains(s, "://") && !strings.HasPrefix(s, "//") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.RawQuery == "" {
		return s
	}
	q := u.Query()
	stripped := false
	for _, p := range sensitiveQueryParams {
		if q.Has(p) {
			stripped = true
			break
		}
	}
	if !stripped {
		return s
	}
	u.RawQuery = ""
	return u.String() + "?" + redacted
}
