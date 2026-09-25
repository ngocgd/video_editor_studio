package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

func isNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

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

// Created is returned by Create/Rotate: the plaintext token to send to
// the client (the CSRF token is derived from it, not stored — see package
// csrf) plus the persisted row.
type Created struct {
	Token   string
	Session gen.Session
}

// Create inserts a brand-new session row for userID, optionally with an
// initial active tenant. Any session(s) already open for this user are
// revoked first, so a fresh login always fully supersedes whatever was
// there before (e.g. a device that was never logged out) rather than
// accumulating alongside it.
func (Store) Create(ctx context.Context, q *gen.Queries, userID uuid.UUID, activeTenantID *uuid.UUID) (Created, error) {
	if err := q.DeleteSessionsForUser(ctx, idconv.ToPg(userID)); err != nil {
		return Created{}, err
	}

	token, tokenHash := NewToken()
	now := time.Now().UTC()

	row, err := q.CreateSession(ctx, gen.CreateSessionParams{
		ID:             idconv.ToPg(idconv.NewV7()),
		UserID:         idconv.ToPg(userID),
		ActiveTenantID: idconv.ToPgPtr(activeTenantID),
		TokenHash:      tokenHash,
		ExpiresAt:      idconv.ToPgTimestamptz(now.Add(AbsoluteLifetime)),
	})
	if err != nil {
		return Created{}, err
	}

	// Best-effort housekeeping, piggybacked on the login path so neither
	// table grows unbounded without needing a separate scheduled job;
	// errors here never fail the login itself.
	if err := q.DeleteExpiredSessions(ctx); err != nil {
		slog.ErrorContext(ctx, "expired session cleanup failed", "error", err)
	}
	if err := q.DeleteStaleRateLimitBuckets(ctx); err != nil {
		slog.ErrorContext(ctx, "stale rate limit bucket cleanup failed", "error", err)
	}

	return Created{Token: token, Session: row}, nil
}

// Rotate replaces sessionID's token and active tenant in place, used for
// session-fixation defence on tenant switch. expires_at is left
// untouched by the underlying query (see RotateSession's doc comment).
func (Store) Rotate(ctx context.Context, q *gen.Queries, sessionID uuid.UUID, activeTenantID *uuid.UUID) (Created, error) {
	token, tokenHash := NewToken()

	row, err := q.RotateSession(ctx, gen.RotateSessionParams{
		ID:             idconv.ToPg(sessionID),
		TokenHash:      tokenHash,
		ActiveTenantID: idconv.ToPgPtr(activeTenantID),
	})
	if err != nil {
		return Created{}, err
	}
	return Created{Token: token, Session: row}, nil
}

// Lookup finds the session for a plaintext token, applying the idle
// timeout in application code (the DB row only enforces the absolute
// expires_at). ok is false for a missing, expired-absolute, revoked, or
// idle-expired session. A genuine DB error is returned rather than
// swallowed, so an outage surfaces as a 500 instead of looking like every
// session logged out at once.
func (Store) Lookup(ctx context.Context, q *gen.Queries, token string) (gen.Session, bool, error) {
	if token == "" {
		return gen.Session{}, false, nil
	}
	sum := sha256.Sum256([]byte(token))
	row, err := q.GetSessionByTokenHash(ctx, sum[:])
	if err != nil {
		if isNotFound(err) {
			return gen.Session{}, false, nil
		}
		return gen.Session{}, false, err
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

// Delete removes a session row (logout).
func (Store) Delete(ctx context.Context, q *gen.Queries, sessionID uuid.UUID) error {
	return q.DeleteSession(ctx, idconv.ToPg(sessionID))
}
