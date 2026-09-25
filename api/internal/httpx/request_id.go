package httpx

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey int

const requestIDKey contextKey = iota

// RequestIDMiddleware assigns a UUID request id to each request, echoes it
// in the X-Request-Id response header, and stores it in the context. A
// client-supplied X-Request-Id is only honored (for correlating a request
// across a caller's own logs) if it actually parses as a UUID; anything
// else is replaced with a freshly generated one rather than echoed
// unvalidated into every subsequent log line.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if _, err := uuid.Parse(id); err != nil {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestID returns the request id stored in ctx, or "" if absent.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
