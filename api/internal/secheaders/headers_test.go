package secheaders

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSPContainsRequiredDirectives(t *testing.T) {
	csp := Config{}.CSP()
	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"object-src 'none'",
		"style-src 'self' 'unsafe-inline'",
		"frame-ancestors 'none'",
		"require-trusted-types-for 'script'",
		"trusted-types default dompurify",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing directive %q: %s", want, csp)
		}
	}
}

func TestMiddlewareSetsHeadersAndNeverReportOnly(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := Config{}.Middleware(next)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Header().Get("Content-Security-Policy") == "" {
		t.Error("expected an enforcing Content-Security-Policy header")
	}
	if w.Header().Get("Content-Security-Policy-Report-Only") != "" {
		t.Error("must never send Content-Security-Policy-Report-Only")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("expected X-Content-Type-Options: nosniff")
	}
}

func TestHSTSOnlyWhenPublicURLIsHTTPS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	httpCfg := Config{PublicURL: "http://127.0.0.1:8080"}
	w1 := httptest.NewRecorder()
	httpCfg.Middleware(next).ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/", nil))
	if w1.Header().Get("Strict-Transport-Security") != "" {
		t.Error("expected no HSTS header for an http PublicURL")
	}

	httpsCfg := Config{PublicURL: "https://app.example.com"}
	w2 := httptest.NewRecorder()
	httpsCfg.Middleware(next).ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/", nil))
	if w2.Header().Get("Strict-Transport-Security") == "" {
		t.Error("expected an HSTS header for an https PublicURL")
	}
}
