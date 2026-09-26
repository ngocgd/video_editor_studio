-- name: ListTakes :many
SELECT t.*, a.variants, a.duration_ms AS asset_duration_ms, a.mime FROM scene_takes t
JOIN assets a ON a.id = t.asset_id AND a.tenant_id = @tenant_id
WHERE t.tenant_id = @tenant_id AND t.scene_id = @scene_id
ORDER BY t.kind, t.created_at, t.id;

-- name: GetTake :one
SELECT * FROM scene_takes WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetSelectedTake :one
SELECT * FROM scene_takes WHERE tenant_id = @tenant_id AND scene_id = @scene_id AND kind = @kind AND selected;

-- name: InsertTake :one
INSERT INTO scene_takes (id, tenant_id, scene_id, kind, asset_id, params, input_hash, selected, step_id)
VALUES (@id, @tenant_id, @scene_id, @kind, @asset_id, @params, @input_hash, false, @step_id)
RETURNING *;

-- name: UnselectTakes :exec
UPDATE scene_takes SET selected = false
WHERE tenant_id = @tenant_id AND scene_id = @scene_id AND kind = @kind AND selected;

-- name: MarkTakeSelected :one
UPDATE scene_takes SET selected = true
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;
