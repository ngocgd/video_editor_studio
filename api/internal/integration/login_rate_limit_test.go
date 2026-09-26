//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// TestLoginRateLimitReturns429 deliberately exhausts login buckets from the
// suite's single client IP. isolateLoginIPBudget refunds the IP bucket when
// the test ends, so it can run in any order relative to the tests that log
// in, and the per-account bucket it trips belongs to a unique email.
func TestLoginRateLimitReturns429(t *testing.T) {
	skipIfAPIUnreachable(t)
	isolateLoginIPBudget(t)
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
