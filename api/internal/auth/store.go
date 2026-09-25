package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/csrf"
	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// Store persists sessions through gen.Queries. It holds no pool of its
// own: callers pass a *gen.Queries scoped to the pool for reads, or to a
// transaction when a session change must commit atomically with an audit
// log entry.
type Store struct{}

// NewToken generates a fresh random session token and its SHA-256 (the
// only thing ever stored).
func NewToken() (token string, hash []byte) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("auth: crypto/rand unavailable: " + err.Error())
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, sum[:]
}

// Created is returned by Create/Rotate: the plaintext values to send to
// the client (cookie + CSRF header) plus the persisted row.
type Created struct {
	Token     string
	CSRFToken string
	Session   gen.Session
}

// Create inserts a brand-new session row for userID, optionally with an
// initial active tenant.
func (Store) Create(ctx context.Context, q *gen.Queries, userID uuid.UUID, activeTenantID *uuid.UUID) (Created, error) {
	token, tokenHash := NewToken()
	csrfToken, csrfHash := csrf.NewToken()
	now := time.Now().UTC()

	row, err := q.CreateSession(ctx, gen.CreateSessionParams{
		ID:             idconv.ToPg(idconv.NewV7()),
		UserID:         idconv.ToPg(userID),
		ActiveTenantID: idconv.ToPgPtr(activeTenantID),
		TokenHash:      tokenHash,
		CsrfTokenHash:  csrfHash,
		ExpiresAt:      idconv.ToPgTimestamptz(now.Add(AbsoluteLifetime)),
	})
	if err != nil {
		return Created{}, err
	}
	return Created{Token: token, CSRFToken: csrfToken, Session: row}, nil
}

// Rotate replaces sessionID's token, CSRF token and active tenant in
// place, used for session-fixation defence on tenant switch (and
// available for login-time rotation of a pre-existing session).
func (Store) Rotate(ctx context.Context, q *gen.Queries, sessionID uuid.UUID, activeTenantID *uuid.UUID) (Created, error) {
	token, tokenHash := NewToken()
	csrfToken, csrfHash := csrf.NewToken()
	now := time.Now().UTC()

	row, err := q.RotateSession(ctx, gen.RotateSessionParams{
		ID:             idconv.ToPg(sessionID),
		TokenHash:      tokenHash,
		CsrfTokenHash:  csrfHash,
		ActiveTenantID: idconv.ToPgPtr(activeTenantID),
		ExpiresAt:      idconv.ToPgTimestamptz(now.Add(AbsoluteLifetime)),
	})
	if err != nil {
		return Created{}, err
	}
	return Created{Token: token, CSRFToken: csrfToken, Session: row}, nil
}

// Lookup finds the session for a plaintext token, applying the idle
// timeout in application code (the DB row only enforces the absolute
// expires_at). ok is false for a missing, expired-absolute, revoked, or
// idle-expired session.
func (Store) Lookup(ctx context.Context, q *gen.Queries, token string) (gen.Session, bool, error) {
	if token == "" {
		return gen.Session{}, false, nil
	}
	sum := sha256.Sum256([]byte(token))
	row, err := q.GetSessionByTokenHash(ctx, sum[:])
	if err != nil {
		return gen.Session{}, false, nil //nolint:nilerr // "not found" is a normal outcome, not a transport error
	}
	if time.Since(row.LastSeenAt.Time) > IdleLifetime {
		return gen.Session{}, false, nil
	}
	return row, true, nil
}

// Touch updates last_seen_at, but only if more than TouchInterval has
// elapsed since it was last written (perf budget: at most once per 5
// minutes per session).
func (Store) Touch(ctx context.Context, q *gen.Queries, sessionID uuid.UUID, lastSeenAt time.Time) error {
	if time.Since(lastSeenAt) < TouchInterval {
		return nil
	}
	return q.TouchSessionLastSeen(ctx, gen.TouchSessionLastSeenParams{
		ID:         idconv.ToPg(sessionID),
		LastSeenAt: idconv.ToPgTimestamptz(time.Now().UTC()),
	})
}

// RefreshCSRF issues a new CSRF token for sessionID without touching the
// session's own token (cookie stays valid).
func (Store) RefreshCSRF(ctx context.Context, q *gen.Queries, sessionID uuid.UUID) (string, error) {
	csrfToken, csrfHash := csrf.NewToken()
	if err := q.UpdateSessionCSRF(ctx, gen.UpdateSessionCSRFParams{
		ID:            idconv.ToPg(sessionID),
		CsrfTokenHash: csrfHash,
	}); err != nil {
		return "", err
	}
	return csrfToken, nil
}

// Delete removes a session row (logout).
func (Store) Delete(ctx context.Context, q *gen.Queries, sessionID uuid.UUID) error {
	return q.DeleteSession(ctx, idconv.ToPg(sessionID))
}
