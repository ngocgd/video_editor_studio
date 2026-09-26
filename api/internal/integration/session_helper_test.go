//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

const httpTimeout = 10 * time.Second

// sessionCookieName mirrors auth.CookieName. The integration suite reaches
// the API over plain HTTP on the compose network (api:8080, not through
// TLS), so it tracks the cookie by hand instead of using net/http's
// cookiejar: the jar correctly refuses to resend a Secure-flagged cookie
// over a non-https connection (RFC 6265), the same way a real browser
// would off of localhost's well-known secure-context exception. This is a
// test-harness concession, not a product behavior change.
const sessionCookieName = "__Host-lt_sess"

// session wraps an authenticated HTTP session against the running API,
// carrying its own session cookie and CSRF token.
type session struct {
	t         *testing.T
	client    *http.Client
	cookie    string
	csrfToken string
}

func login(t *testing.T, email, password string) *session {
	t.Helper()
	resetLoginIPBucket(t)
	client := &http.Client{Timeout: httpTimeout}

	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequest(http.MethodPost, baseURL()+"/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", originHeader())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: got status %d", resp.StatusCode)
	}
	var out struct {
		CsrfToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("login response set no session cookie")
	}
	return &session{t: t, client: client, cookie: cookie, csrfToken: out.CsrfToken}
}

func originHeader() string {
	// The compose stack's API_BASE_URL points at the Caddy-fronted origin
	// used by CSRF's Origin allowlist (deploy/compose.yml ALLOWED_ORIGINS).
	return "http://127.0.0.1:8080"
}

// doUnauthenticated sends a request with no session cookie (e.g. a login
// attempt), still through the same Origin header CSRF's allowlist needs.
func doUnauthenticated(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: httpTimeout}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, baseURL()+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", originHeader())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func (s *session) do(method, path string, body any) *http.Response {
	s.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, baseURL()+path, reader)
	if err != nil {
		s.t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: s.cookie})
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", originHeader())
		req.Header.Set("X-CSRF-Token", s.csrfToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func requireStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s %s: got status %d, want %d", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want)
	}
}

// requireProblem asserts both the status and the problem+json title, so a
// 403 from CSRF and a 403 from RBAC (both currently used by the API) are
// never confused for each other by a test that only checks the status
// code: a regression that made CSRF always reject would otherwise still
// pass a test that expected "any 403".
func requireProblem(t *testing.T, resp *http.Response, wantStatus int, wantTitle string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: got status %d, want %d", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, wantStatus)
	}
	var problem struct {
		Title string `json:"title"`
	}
	decodeJSON(t, resp, &problem)
	if problem.Title != wantTitle {
		t.Fatalf("%s %s: got problem title %q, want %q", resp.Request.Method, resp.Request.URL.Path, problem.Title, wantTitle)
	}
}

func skipIfAPIUnreachable(t *testing.T) {
	t.Helper()
	resp, err := http.Get(baseURL() + "/healthz")
	if err != nil {
		missingEnv(t, "API_BASE_URL", "API not reachable at "+baseURL()+": "+err.Error())
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		missingEnv(t, "API_BASE_URL", "API healthz returned an unexpected status")
	}
}

// resetLoginIPBucket clears the per-IP login bucket (20/hour) before a
// fixture login. Every test in this package logs in from the same client
// IP, so the suite as a whole would otherwise exhaust that bucket and fail
// unrelated tests with 429. Only the IP bucket is cleared: the per-user
// and per-account buckets still apply, and TestLoginRateLimitReturns429
// still proves the limiter trips.
func resetLoginIPBucket(t *testing.T) {
	t.Helper()
	if _, err := ownerPool(t).Exec(context.Background(), `DELETE FROM rate_limit_buckets WHERE bucket_key LIKE 'login:ip:%'`); err != nil {
		t.Fatalf("reset login ip bucket: %v", err)
	}
}
