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

// sensitiveQueryParams are capability-granting query parameters (compared
// case-insensitively: S3-style presigned URLs are not consistent about
// casing across clients/SDKs). Their mere presence in a logged URL is
// equivalent to logging a bearer credential, so the whole query string is
// stripped rather than just the one parameter.
var sensitiveQueryParams = map[string]bool{
	"x-amz-signature":  true,
	"x-amz-credential": true,
	"upload_id":        true,
	"upload_protocol":  true,
}

// urlPattern finds a URL anywhere inside a larger string (e.g. an FFmpeg
// line like `Opening 'https://...?X-Amz-Signature=...' for reading`), not
// just when the whole string is one: capability URLs are not always
// logged as a bare value.
var urlPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s'"<>]+`)

// ReplaceAttr is an slog.HandlerOptions.ReplaceAttr that drops the value
// of any attribute whose key looks secret-shaped, and scrubs capability
// URLs out of every string value (attribute keys are checked
// case-insensitively against keyPattern; values are always passed through
// URL as a defence in depth in case a secret lands under an innocuous
// key). An error-typed value (slog.KindAny; the common case is
// "error", err passed straight to slog) is scrubbed the same way as a
// string, since err.Error() often embeds a URL verbatim (e.g. minio-go
// wraps a *url.Error around the exact request URL it failed on).
func ReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	if keyPattern.MatchString(a.Key) {
		a.Value = slog.StringValue(redacted)
		return a
	}
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(URL(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(URL(err.Error()))
		}
	}
	return a
}

// URL scrubs every capability URL found anywhere within s, replacing each
// one's query string with a redaction marker; text with no such URL is
// returned unchanged. It is exported so FFmpeg stderr and worker step
// logs, which are not slog.Attr values, can scrub capability URLs the
// same way.
func URL(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	return urlPattern.ReplaceAllStringFunc(s, scrubOneURL)
}

func scrubOneURL(candidate string) string {
	u, err := url.Parse(candidate)
	if err != nil || u.RawQuery == "" {
		return candidate
	}
	stripped := false
	for key := range u.Query() {
		if sensitiveQueryParams[strings.ToLower(key)] {
			stripped = true
			break
		}
	}
	if !stripped {
		return candidate
	}
	u.RawQuery = ""
	return u.String() + "?" + redacted
}
