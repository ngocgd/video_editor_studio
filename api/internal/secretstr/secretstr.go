// Package secretstr holds a string that must never be logged: its
// String(), GoString() and slog.LogValue() all redact, so a stray %v, %s
// or structured log field never leaks it. Call Reveal() explicitly at the
// one call site that needs the plaintext (e.g. passing a password to
// argon2, or a KEK to AES-GCM).
package secretstr

import "log/slog"

// String wraps a secret value so accidental logging/printing redacts it.
type String string

const redacted = "[REDACTED]"

// Reveal returns the underlying plaintext. Never pass the result to a
// logger; pass it only to the code that consumes the secret.
func (s String) Reveal() string { return string(s) }

// String implements fmt.Stringer.
func (s String) String() string { return redacted }

// GoString implements fmt.GoStringer (covers %#v).
func (s String) GoString() string { return redacted }

// LogValue implements slog.LogValuer so slog never prints the plaintext
// even if a String ends up as a log attribute value.
func (s String) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON redacts on accidental JSON encoding (e.g. a debug dump).
func (s String) MarshalJSON() ([]byte, error) {
	return []byte(`"` + redacted + `"`), nil
}
