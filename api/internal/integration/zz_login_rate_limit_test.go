//go:build integration

// This file is named to sort alphabetically last among this package's test
// files: Go runs a package's tests in the order they appear across its
// compiled test files, which (for `go test`, absent explicit ordering) is
// the lexical order of the source file names. TestLoginRateLimitReturns429
// below deliberately exhausts the server's per-IP login-rate-limit bucket
// (every test in this package logs in from the same client IP against the
// same live server, since there is no way to make a real server see
// per-test-isolated source IPs), which would otherwise starve every other
// test's login calls of tokens for the rest of the run.
package integration

import (
	"net/http"
	"testing"
)

func TestLoginRateLimitReturns429(t *testing.T) {
	skipIfAPIUnreachable(t)
	email := uniqueEmail("ratelimit")
	body := map[string]string{"email": email, "password": "whatever-wrong-password"}

	var last *http.Response
	// The per-account bucket (10/hour) is the tightest of the three login
	// buckets for a single unique email hammered from one IP, so it is
	// guaranteed to trip within this many attempts regardless of the
	// other two buckets' exact capacity.
	for i := 0; i < 15; i++ {
		last = doUnauthenticated(t, http.MethodPost, "/auth/login", body)
		if last.StatusCode == http.StatusTooManyRequests {
			return
		}
		_ = last.Body.Close()
	}
	t.Fatalf("expected a 429 within 15 attempts, last status was %d", last.StatusCode)
}
