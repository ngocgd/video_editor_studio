package httpx

import (
	"context"
	"net/http"
)

type requestCtxKey int

const requestKey requestCtxKey = 0

// WithRequestMiddleware stores the raw *http.Request in its own context so
// strict-server handlers (which the oapi-codegen strict wrapper calls with
// only a typed request object, not the *http.Request) can still read
// RemoteAddr/UserAgent/headers for audit logging and rate limiting.
func WithRequestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestKey, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestFromCtx returns the *http.Request stored by WithRequestMiddleware.
// It never returns nil for a request that went through the middleware
// chain; callers outside that chain (e.g. tests) must construct one.
func RequestFromCtx(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey).(*http.Request)
	return r
}
