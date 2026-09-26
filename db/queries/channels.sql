-- name: UpsertYouTubeChannel :one
-- Connect or reconnect: a channel already known to the tenant keeps its id
-- (and so its secret owner_ref and any publications) and becomes connected.
INSERT INTO youtube_channels (
    id, tenant_id, youtube_channel_id, title, thumbnail_url, scopes,
    long_uploads_status, eligibility_checked_at, connected_by
) VALUES (
    @id, @tenant_id, @youtube_channel_id, @title, @thumbnail_url, @scopes,
    @long_uploads_status, now(), @connected_by
)
ON CONFLICT (tenant_id, youtube_channel_id) DO UPDATE SET
    title = EXCLUDED.title,
    thumbnail_url = EXCLUDED.thumbnail_url,
    scopes = EXCLUDED.scopes,
    long_uploads_status = EXCLUDED.long_uploads_status,
    eligibility_checked_at = now(),
    connected_by = EXCLUDED.connected_by,
    status = 'connected',
    updated_at = now()
RETURNING *;

-- name: ListYouTubeChannels :many
SELECT * FROM youtube_channels
WHERE tenant_id = @tenant_id
ORDER BY (status = 'disconnected'), title, id;

-- name: GetYouTubeChannel :one
SELECT * FROM youtube_channels WHERE tenant_id = @tenant_id AND id = @id;

-- name: SetYouTubeChannelStatus :one
UPDATE youtube_channels SET status = @status, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: UpdateYouTubeChannelAudit :one
UPDATE youtube_channels SET
    api_project_audited = @api_project_audited,
    audit_form_date = @audit_form_date,
    audit_note = @audit_note,
    updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: UpdateYouTubeChannelEligibility :one
UPDATE youtube_channels SET
    long_uploads_status = @long_uploads_status,
    eligibility_checked_at = now(),
    updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: CreateYouTubeOAuthState :exec
INSERT INTO youtube_oauth_states (state_hash, tenant_id, user_id, session_id, code_verifier, expires_at)
VALUES (@state_hash, @tenant_id, @user_id, @session_id, @code_verifier, @expires_at);

-- name: ConsumeYouTubeOAuthState :one
-- Single use: the row is deleted whether or not it is still valid, and is
-- only returned when it belongs to this session and has not expired.
WITH gone AS (
    DELETE FROM youtube_oauth_states WHERE state_hash = @state_hash
    RETURNING *
)
SELECT * FROM gone
WHERE tenant_id = @tenant_id AND session_id = @session_id AND expires_at > now();

-- name: DeleteExpiredYouTubeOAuthStates :exec
-- lint-tenant-queries:allow: housekeeping of expired handshakes across all tenants; returns nothing
DELETE FROM youtube_oauth_states WHERE expires_at <= now();
