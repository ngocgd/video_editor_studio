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
INSERT INTO sessions (id, user_id, active_tenant_id, token_hash, csrf_token_hash, expires_at)
VALUES (@id, @user_id, @active_tenant_id, @token_hash, @csrf_token_hash, @expires_at)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions
WHERE token_hash = @token_hash AND revoked_at IS NULL AND expires_at > now();

-- name: TouchSessionLastSeen :exec
UPDATE sessions SET last_seen_at = @last_seen_at WHERE id = @id;

-- name: RotateSession :one
-- Used on login (session fixation defence) and on tenant switch.
UPDATE sessions
SET token_hash = @token_hash,
    csrf_token_hash = @csrf_token_hash,
    active_tenant_id = @active_tenant_id,
    expires_at = @expires_at,
    last_seen_at = now()
WHERE id = @id
RETURNING *;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = @id;

-- name: UpdateSessionCSRF :exec
-- Refreshes only the CSRF token, leaving the session's own token_hash (and
-- therefore the client's cookie) untouched.
UPDATE sessions SET csrf_token_hash = @csrf_token_hash WHERE id = @id;
