-- +goose Up
CREATE TABLE tenants (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    email text NOT NULL,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- Application code lower()s email before every read/write, so a plain
-- unique index (rather than a citext column) is enough for case-insensitive
-- uniqueness without adding an extension.
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE memberships (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id)
);
CREATE INDEX memberships_user_id_idx ON memberships (user_id);

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    active_tenant_id uuid REFERENCES tenants (id) ON DELETE SET NULL,
    token_hash bytea NOT NULL,
    -- No csrf_token_hash column: the CSRF token is derived deterministically
    -- from (server pepper, session token) — see api/internal/csrf — so it
    -- never needs storing, rotating, or a GET endpoint with a write
    -- side effect to recover it.
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    -- Absolute lifetime (7d); idle lifetime (12h) is enforced in application
    -- code from last_seen_at so it does not need its own column.
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

CREATE TABLE rate_limit_buckets (
    bucket_key text PRIMARY KEY,
    tokens real NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Columns only in this phase; quota.Check is wired into pipeline.Enqueue in
-- a later phase and stays unlimited (NULL) by default for local dev.
CREATE TABLE tenant_quotas (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    max_renders_per_day integer,
    max_storage_bytes bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE tenant_quotas;
DROP TABLE rate_limit_buckets;
DROP TABLE sessions;
DROP TABLE memberships;
DROP TABLE users;
DROP TABLE tenants;
