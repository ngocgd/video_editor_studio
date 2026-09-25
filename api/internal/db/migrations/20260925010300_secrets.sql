-- +goose Up
-- Envelope-encrypted secrets (OAuth tokens, provider API keys, ...). Only
-- ciphertext ever lands in a column; see api/internal/crypto/envelope.
CREATE TABLE secrets (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    kind text NOT NULL,
    owner_ref text NOT NULL,
    key_id text NOT NULL,
    wrapped_dek bytea NOT NULL,
    nonce bytea NOT NULL,
    ciphertext bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX secrets_tenant_kind_owner_key ON secrets (tenant_id, kind, owner_ref);

-- +goose Down
DROP TABLE secrets;
