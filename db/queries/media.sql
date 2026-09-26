-- name: CreateDerivedAsset :one
-- An asset produced server-side (a generated image, a voice track, a
-- subtitle alignment, waveform peaks): the bytes are already in object
-- storage, so it is created ready.
INSERT INTO assets (id, tenant_id, kind, storage_key, mime, bytes, sha256, width, height, duration_ms,
                    storage_version_id, status)
VALUES (@id, @tenant_id, @kind, @storage_key, @mime, @bytes, @sha256, @width, @height, @duration_ms,
        @storage_version_id, 'ready')
RETURNING *;

-- name: SetAssetVariants :one
-- Merges new derivative keys (image variants, waveform peaks) into the
-- asset's variants map without dropping keys another step wrote.
UPDATE assets SET variants = variants || @variants::jsonb, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: SetAssetDimensions :exec
UPDATE assets SET width = @width, height = @height, duration_ms = @duration_ms, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListAssetsMissingDerivatives :many
-- Backfill: ready images without image variants and ready audio without
-- waveform peaks.
SELECT * FROM assets
WHERE tenant_id = @tenant_id AND status = 'ready'
  AND ((kind = 'image' AND NOT (variants ? 'webp')) OR (kind = 'audio' AND NOT (variants ? 'peaks')))
ORDER BY id
LIMIT @page_limit;
