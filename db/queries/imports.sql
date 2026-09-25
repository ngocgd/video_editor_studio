-- name: CreateImport :one
INSERT INTO imports (id, tenant_id, series_id, asset_id, created_by)
VALUES (@id, @tenant_id, @series_id, @asset_id, @created_by)
RETURNING *;

-- name: GetImportByID :one
SELECT * FROM imports WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListImports :many
SELECT * FROM imports
WHERE tenant_id = @tenant_id AND id > @cursor
ORDER BY id
LIMIT @page_limit;

-- name: UpdateImportPreview :one
UPDATE imports
SET encoding = @encoding,
    split_preset = @split_preset,
    chapters = @chapters,
    status = 'preview',
    error_msg = NULL,
    updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: MarkImportCommitted :one
UPDATE imports
SET status = 'committed', series_id = @series_id, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: MarkImportFailed :exec
UPDATE imports SET status = 'failed', error_msg = @error_msg, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id;
