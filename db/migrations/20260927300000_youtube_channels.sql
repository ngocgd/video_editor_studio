-- +goose Up
-- YouTube channels connected through Google OAuth, the short-lived OAuth
-- handshake state, and the Data API quota ledger.

-- One row per channel a tenant has connected. The refresh token is never
-- stored here: it lives in the envelope-encrypted secrets table with
-- kind='youtube_refresh' and owner_ref=<this row's id>. A disconnected
-- channel keeps its row (later publications reference it) but its secret
-- is deleted and its status becomes 'disconnected'.
CREATE TABLE youtube_channels (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    youtube_channel_id text NOT NULL CHECK (youtube_channel_id <> ''),
    title text NOT NULL,
    thumbnail_url text NOT NULL DEFAULT '',
    -- Scopes Google actually granted (a user can untick some on consent).
    scopes text[] NOT NULL DEFAULT '{}',
    -- Manual toggle: the Google API project passed YouTube's API audit, so
    -- uploads may be scheduled or public. Until then every upload is
    -- locked private by YouTube itself.
    api_project_audited boolean NOT NULL DEFAULT false,
    audit_form_date date,
    audit_note text NOT NULL DEFAULT '' CHECK (length(audit_note) <= 2000),
    -- channels.list part=status longUploadsStatus; 'unknown' until read.
    long_uploads_status text NOT NULL DEFAULT 'unknown'
        CHECK (long_uploads_status IN ('allowed', 'eligible', 'disallowed', 'unknown')),
    eligibility_checked_at timestamptz,
    -- Cleared when thumbnails.set answers 403 (channel not verified).
    custom_thumbnails_ok boolean NOT NULL DEFAULT true,
    -- reconnect_needed: Google rejected the refresh token (revoked, or
    -- expired because the OAuth app is still in testing mode).
    status text NOT NULL DEFAULT 'connected'
        CHECK (status IN ('connected', 'reconnect_needed', 'disconnected')),
    connected_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, youtube_channel_id)
);

-- OAuth authorization-code handshakes in flight. The row is keyed by the
-- sha256 of the state value (the raw state only ever exists in the
-- redirect), is bound to the session that started it, is consumed by a
-- single DELETE ... RETURNING and expires after 10 minutes.
CREATE TABLE youtube_oauth_states (
    state_hash bytea PRIMARY KEY CHECK (length(state_hash) = 32),
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    code_verifier text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX youtube_oauth_states_expires_at ON youtube_oauth_states (expires_at);

-- Data API units spent per Google Cloud project, per Pacific-time day
-- (YouTube resets quota at midnight America/Los_Angeles), per bucket.
-- Not tenant-scoped: the daily pool belongs to the Google project.
CREATE TABLE quota_ledger (
    project text NOT NULL,
    pt_date date NOT NULL,
    bucket text NOT NULL,
    units integer NOT NULL DEFAULT 0 CHECK (units >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project, pt_date, bucket)
);

-- +goose Down
DROP TABLE quota_ledger;
DROP TABLE youtube_oauth_states;
DROP TABLE youtube_channels;
