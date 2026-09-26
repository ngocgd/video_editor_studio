package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exhausting the dedicated bucket leaves the general one untouched, and
// the other way round.
func TestSplitMiddlewareKeepsTheBucketsApart(t *testing.T) {
	fixed := time.Now()
	general, media := NewMemory(2, 1), NewMemory(3, 1)
	general.now = func() time.Time { return fixed }
	media.now = func() time.Time { return fixed }
	h := SplitMiddleware(general, media, func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/media/") },
		func(*http.Request) string { return "ip" },
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) },
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	status := func(path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}
	for i := 0; i < 3; i++ {
		if got := status("/media/x"); got != http.StatusNoContent {
			t.Fatalf("media request %d: %d", i, got)
		}
	}
	if got := status("/media/x"); got != http.StatusTooManyRequests {
		t.Fatalf("media request over budget: %d", got)
	}
	for i := 0; i < 2; i++ {
		if got := status("/api/login"); got != http.StatusNoContent {
			t.Fatalf("general request %d after the media budget ran out: %d", i, got)
		}
	}
	if got := status("/api/login"); got != http.StatusTooManyRequests {
		t.Fatalf("general request over budget: %d", got)
	}
}
