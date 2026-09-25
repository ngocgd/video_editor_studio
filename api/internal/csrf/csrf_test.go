package csrf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewTokenVerifies(t *testing.T) {
	token, hash := NewToken()
	if !Verify(token, hash) {
		t.Fatal("expected a freshly generated token to verify against its own hash")
	}
}

func TestVerifyRejectsWrongToken(t *testing.T) {
	_, hash := NewToken()
	if Verify("some-other-token", hash) {
		t.Fatal("expected verification to fail for a mismatched token")
	}
}

func TestVerifyRejectsEmpty(t *testing.T) {
	_, hash := NewToken()
	if Verify("", hash) {
		t.Fatal("expected verification to fail for an empty token")
	}
	if Verify("x", nil) {
		t.Fatal("expected verification to fail for an empty stored hash")
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

	lookup := func(r *http.Request) ([]byte, bool) { return nil, false }
	mw := Middleware(lookup, nil, reject)(next)

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !called || rejected {
		t.Fatal("expected GET to pass through without a CSRF check")
	}
}

func TestMiddlewareRejectsBadOriginOnUnsafeMethod(t *testing.T) {
	token, hash := NewToken()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached")
	})
	rejected := false
	reject := func(w http.ResponseWriter, r *http.Request) { rejected = true }

	lookup := func(r *http.Request) ([]byte, bool) { return hash, true }
	allowed := map[string]bool{"https://app.example.com": true}
	mw := Middleware(lookup, allowed, reject)(next)

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	r.Header.Set(HeaderName, token)
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !rejected {
		t.Fatal("expected a bad-origin unsafe request to be rejected")
	}
}

func TestMiddlewareRejectsMissingCsrfToken(t *testing.T) {
	_, hash := NewToken()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached")
	})
	rejected := false
	reject := func(w http.ResponseWriter, r *http.Request) { rejected = true }

	lookup := func(r *http.Request) ([]byte, bool) { return hash, true }
	allowed := map[string]bool{"https://app.example.com": true}
	mw := Middleware(lookup, allowed, reject)(next)

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("Origin", "https://app.example.com")
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !rejected {
		t.Fatal("expected a request missing X-CSRF-Token to be rejected")
	}
}

func TestMiddlewarePassesValidRequest(t *testing.T) {
	token, hash := NewToken()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	reject := func(w http.ResponseWriter, r *http.Request) { t.Fatal("should not reject") }

	lookup := func(r *http.Request) ([]byte, bool) { return hash, true }
	allowed := map[string]bool{"https://app.example.com": true}
	mw := Middleware(lookup, allowed, reject)(next)

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set(HeaderName, token)
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Fatal("expected a valid request to reach the handler")
	}
}
