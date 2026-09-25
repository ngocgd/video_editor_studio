package csrf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var testPepper = []byte("test-pepper-0123456789abcdef0123456789abcdef")

func TestDeriveIsStableAndVerifies(t *testing.T) {
	token := Derive(testPepper, "session-token-1")
	if !Verify(testPepper, "session-token-1", token) {
		t.Fatal("expected a derived token to verify against its own session token")
	}
	if Derive(testPepper, "session-token-1") != token {
		t.Fatal("expected Derive to be deterministic for the same inputs")
	}
}

func TestDeriveDiffersPerSession(t *testing.T) {
	a := Derive(testPepper, "session-a")
	b := Derive(testPepper, "session-b")
	if a == b {
		t.Fatal("expected different sessions to derive different tokens")
	}
}

func TestVerifyRejectsWrongToken(t *testing.T) {
	if Verify(testPepper, "session-token-1", "not-the-right-token") {
		t.Fatal("expected verification to fail for a mismatched token")
	}
}

func TestVerifyRejectsEmpty(t *testing.T) {
	token := Derive(testPepper, "session-token-1")
	if Verify(testPepper, "", token) {
		t.Fatal("expected verification to fail for an empty session token")
	}
	if Verify(testPepper, "session-token-1", "") {
		t.Fatal("expected verification to fail for an empty presented token")
	}
}

func TestCheckOriginAllowlist(t *testing.T) {
	allowed := map[string]bool{"https://app.example.com": true}

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("Origin", "https://app.example.com")
	if !CheckOrigin(r, allowed) {
		t.Fatal("expected allowed origin to pass")
	}

	r2 := httptest.NewRequest(http.MethodPost, "/x", nil)
	r2.Header.Set("Origin", "https://evil.example.com")
	if CheckOrigin(r2, allowed) {
		t.Fatal("expected disallowed origin to fail")
	}

	r3 := httptest.NewRequest(http.MethodPost, "/x", nil)
	if CheckOrigin(r3, allowed) {
		t.Fatal("expected a request with no Origin/Referer to fail")
	}
}

func TestCheckOriginFallsBackToReferer(t *testing.T) {
	allowed := map[string]bool{"https://app.example.com": true}
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("Referer", "https://app.example.com/some/page")
	if !CheckOrigin(r, allowed) {
		t.Fatal("expected Referer fallback to pass for an allowed origin")
	}
}

func TestMiddlewareAllowsSafeMethodsUnconditionally(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	rejected := false
	reject := func(w http.ResponseWriter, r *http.Request) { rejected = true }

	lookup := func(r *http.Request) (string, bool) { return "", false }
	mw := Middleware(testPepper, lookup, nil, reject)(next)

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !called || rejected {
		t.Fatal("expected GET to pass through without a CSRF check")
	}
}

func TestMiddlewareRejectsBadOriginOnUnsafeMethod(t *testing.T) {
	sessionToken := "session-token-1"
	token := Derive(testPepper, sessionToken)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached")
	})
	rejected := false
	reject := func(w http.ResponseWriter, r *http.Request) { rejected = true }

	lookup := func(r *http.Request) (string, bool) { return sessionToken, true }
	allowed := map[string]bool{"https://app.example.com": true}
	mw := Middleware(testPepper, lookup, allowed, reject)(next)

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	r.Header.Set(HeaderName, token)
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !rejected {
		t.Fatal("expected a bad-origin unsafe request to be rejected")
	}
}

func TestMiddlewareRejectsMissingCsrfToken(t *testing.T) {
	sessionToken := "session-token-1"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached")
	})
	rejected := false
	reject := func(w http.ResponseWriter, r *http.Request) { rejected = true }

	lookup := func(r *http.Request) (string, bool) { return sessionToken, true }
	allowed := map[string]bool{"https://app.example.com": true}
	mw := Middleware(testPepper, lookup, allowed, reject)(next)

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("Origin", "https://app.example.com")
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !rejected {
		t.Fatal("expected a request missing X-CSRF-Token to be rejected")
	}
}

func TestMiddlewarePassesValidRequest(t *testing.T) {
	sessionToken := "session-token-1"
	token := Derive(testPepper, sessionToken)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	reject := func(w http.ResponseWriter, r *http.Request) { t.Fatal("should not reject") }

	lookup := func(r *http.Request) (string, bool) { return sessionToken, true }
	allowed := map[string]bool{"https://app.example.com": true}
	mw := Middleware(testPepper, lookup, allowed, reject)(next)

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set(HeaderName, token)
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Fatal("expected a valid request to reach the handler")
	}
}

func TestMiddlewareRejectsForeignSessionToken(t *testing.T) {
	// A CSRF token derived for a different session must not validate here:
	// proves the token is bound to the specific session, not just "any
	// token this server ever issued".
	otherToken := Derive(testPepper, "some-other-session")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached")
	})
	rejected := false
	reject := func(w http.ResponseWriter, r *http.Request) { rejected = true }

	lookup := func(r *http.Request) (string, bool) { return "session-token-1", true }
	allowed := map[string]bool{"https://app.example.com": true}
	mw := Middleware(testPepper, lookup, allowed, reject)(next)

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set(HeaderName, otherToken)
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !rejected {
		t.Fatal("expected a token derived for a different session to be rejected")
	}
}
