-- name: CreateSeries :one
INSERT INTO series (
    id, tenant_id, title, genre, target_languages,
    target_episode_minutes, planned_episode_count, style_notes, created_by
)
VALUES (
    @id, @tenant_id, @title, @genre, @target_languages,
    @target_episode_minutes, @planned_episode_count, @style_notes, @created_by
)
RETURNING *;

-- name: GetSeriesByID :one
SELECT * FROM series WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListSeries :many
SELECT * FROM series
WHERE tenant_id = @tenant_id AND id > @cursor
ORDER BY id
LIMIT @page_limit;

-- name: UpdateSeries :one
UPDATE series
SET title = @title,
    genre = @genre,
    target_languages = @target_languages,
    target_episode_minutes = @target_episode_minutes,
    planned_episode_count = @planned_episode_count,
    style_notes = @style_notes,
    status = @status,
    updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: DeleteSeries :exec
DELETE FROM series WHERE tenant_id = @tenant_id AND id = @id;
