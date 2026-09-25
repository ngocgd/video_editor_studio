package scrub

import "regexp"

// secretPatterns matches common bearer-token and API-key shapes that can
// appear embedded in free text (an error message, a step's captured
// stderr) rather than as a structured value under a suspiciously-named
// key (which ReplaceAttr already handles) or inside a URL's query string
// (which URL already handles). Each pattern keeps a short, clearly
// non-secret prefix so a redacted value is still useful for debugging
// ("which kind of credential leaked") without exposing the credential
// itself.
var secretPatterns = []*regexp.Regexp{
	// Authorization: Bearer <token> / bare "Bearer <token>" in log text.
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	// Common API key prefixes (OpenAI/Anthropic/Stripe-style sk-/pk-/rk-
	// live/test keys, GitHub tokens, generic long "key=" assignments).
	regexp.MustCompile(`\b(sk|pk|rk)-[A-Za-z0-9]{8,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|secret|token|password)["'=:\s]{1,3}[A-Za-z0-9._~+/=-]{12,}`),
}

const secretRedacted = "[REDACTED]"

// Secrets redacts every recognised bearer-token or API-key shape found
// anywhere within s; text with no such match is returned unchanged.
func Secrets(s string) string {
	for _, pattern := range secretPatterns {
		s = pattern.ReplaceAllString(s, secretRedacted)
	}
	return s
}

// Text scrubs both capability URLs and bearer-token/API-key shaped
// secrets out of s. This is the function anything persisting free-form
// text a handler produced (a step's error message, its captured log
// output) should call, since either category of leak is realistic there.
func Text(s string) string {
	return Secrets(URL(s))
}
