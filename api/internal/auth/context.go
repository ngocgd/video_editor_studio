package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Session is the request-scoped view of the caller's session, attached to
// context by Middleware after a successful cookie lookup.
type Session struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	ActiveTenantID *uuid.UUID
	ExpiresAt      time.Time
}

type contextKey int

const sessionKey contextKey = iota

// WithSession returns a context carrying sess.
func WithSession(ctx context.Context, sess Session) context.Context {
	return context.WithValue(ctx, sessionKey, sess)
}

// FromCtx returns the session attached by Middleware, if any.
func FromCtx(ctx context.Context) (Session, bool) {
	sess, ok := ctx.Value(sessionKey).(Session)
	return sess, ok
}
