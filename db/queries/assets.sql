-- name: CreateAsset :one
INSERT INTO assets (id, tenant_id, kind, storage_key, mime, created_by)
VALUES (@id, @tenant_id, @kind, @storage_key, @mime, @created_by)
RETURNING *;

-- name: GetAssetByID :one
SELECT * FROM assets WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetAssetByStorageKey :one
-- Used to check ownership of a key before signing or finalizing it.
SELECT * FROM assets WHERE tenant_id = @tenant_id AND storage_key = @storage_key;

-- name: ListAssets :many
SELECT * FROM assets
WHERE tenant_id = @tenant_id AND id > @cursor
ORDER BY id
LIMIT @page_limit;

-- name: MarkAssetReady :one
UPDATE assets
SET status = 'ready',
    bytes = @bytes,
    sha256 = @sha256,
    mime = @mime,
    width = @width,
    height = @height,
    duration_ms = @duration_ms,
    updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: MarkAssetFailed :exec
UPDATE assets SET status = 'failed', updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id;
