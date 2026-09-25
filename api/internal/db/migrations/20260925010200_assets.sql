-- +goose Up
CREATE TABLE assets (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('image', 'audio', 'video', 'document')),
    storage_key text NOT NULL,
    -- Set at finalize time from the bucket's own object version (bucket
    -- versioning is enabled by deploy/minio-init.sh). Every read after
    -- finalize pins this exact version, so a client that still holds an
    -- unexpired presigned POST for this key (valid up to 10 minutes after
    -- issue) cannot make a "ready" asset silently start serving different,
    -- unverified bytes by re-uploading to the same key: that upload lands
    -- as a new, unreferenced version instead of mutating the one this row
    -- points at.
    storage_version_id text,
    mime text NOT NULL,
    bytes bigint,
    sha256 text,
    width integer,
    height integer,
    duration_ms integer,
    variants jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed')),
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX assets_storage_key_key ON assets (storage_key);
-- Every list/cursor query filters and orders on this pair.
CREATE INDEX assets_tenant_id_id_idx ON assets (tenant_id, id);

-- +goose Down
DROP TABLE assets;
