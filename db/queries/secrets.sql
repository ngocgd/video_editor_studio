-- name: UpsertSecret :exec
INSERT INTO secrets (id, tenant_id, kind, owner_ref, key_id, wrapped_dek, nonce, ciphertext)
VALUES (@id, @tenant_id, @kind, @owner_ref, @key_id, @wrapped_dek, @nonce, @ciphertext)
ON CONFLICT (tenant_id, kind, owner_ref) DO UPDATE SET
    key_id = EXCLUDED.key_id,
    wrapped_dek = EXCLUDED.wrapped_dek,
    nonce = EXCLUDED.nonce,
    ciphertext = EXCLUDED.ciphertext,
    updated_at = now();

-- name: GetSecret :one
SELECT * FROM secrets WHERE tenant_id = @tenant_id AND kind = @kind AND owner_ref = @owner_ref;
