-- name: CreateUser :one
INSERT INTO users (id, email, password_hash)
VALUES (@id, lower(@email), @password_hash)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = lower(@email);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- name: UpdateUserPasswordHash :exec
UPDATE users SET password_hash = @password_hash, updated_at = now() WHERE id = @id;

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, active_tenant_id, token_hash, expires_at)
VALUES (@id, @user_id, @active_tenant_id, @token_hash, @expires_at)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions
WHERE token_hash = @token_hash AND revoked_at IS NULL AND expires_at > now();

-- name: TouchSessionLastSeen :exec
UPDATE sessions SET last_seen_at = @last_seen_at WHERE id = @id;

-- name: RotateSession :one
-- Used on tenant switch (session-fixation defence: a session that briefly
-- saw tenant A's data gets a fresh token before it can act on tenant B's).
-- expires_at is deliberately left untouched: rotating must not extend the
-- 7-day absolute lifetime, or repeatedly switching tenants would keep a
-- session alive forever.
UPDATE sessions
SET token_hash = @token_hash,
    active_tenant_id = @active_tenant_id,
    last_seen_at = now()
WHERE id = @id
RETURNING *;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = @id;

-- name: DeleteSessionsForUser :exec
-- Called on login so a fresh login revokes any session(s) left over from
-- before (e.g. a device that was never logged out), not just the new one.
DELETE FROM sessions WHERE user_id = @user_id;

-- name: DeleteExpiredSessions :exec
-- Best-effort housekeeping, called opportunistically (not on a schedule)
-- so the table does not grow unbounded; safe to run concurrently.
DELETE FROM sessions WHERE expires_at < now();

-- name: DeleteStaleRateLimitBuckets :exec
-- Best-effort housekeeping for the same reason as DeleteExpiredSessions.
DELETE FROM rate_limit_buckets WHERE updated_at < now() - interval '7 days';
