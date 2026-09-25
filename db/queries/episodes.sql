-- name: CreateEpisode :one
INSERT INTO episodes (id, tenant_id, series_id, idx, title, outline, status, source_import_chapter_index)
VALUES (@id, @tenant_id, @series_id, @idx, @title, @outline, @status, @source_import_chapter_index)
RETURNING *;

-- name: GetEpisodeByID :one
SELECT * FROM episodes WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListEpisodesBySeries :many
SELECT * FROM episodes
WHERE tenant_id = @tenant_id AND series_id = @series_id AND idx > @cursor
ORDER BY idx
LIMIT @page_limit;

-- name: ListEpisodesWithDraftStatus :many
-- One query for the episode list: word count and draft presence per
-- language are aggregated here instead of a per-row N+1 lookup.
SELECT
    e.*,
    COALESCE(
        jsonb_object_agg(d.lang, jsonb_build_object('wordCount', d.word_count, 'version', d.version))
            FILTER (WHERE d.id IS NOT NULL),
        '{}'::jsonb
    ) AS drafts
FROM episodes e
LEFT JOIN episode_drafts d ON d.episode_id = e.id
WHERE e.tenant_id = @tenant_id AND e.series_id = @series_id AND e.idx > @cursor
GROUP BY e.id
ORDER BY e.idx
LIMIT @page_limit;

-- name: UpdateEpisodeOutline :one
UPDATE episodes
SET outline = @outline,
    status = @status,
    updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: UpdateEpisodeMeta :one
UPDATE episodes
SET title = @title,
    status = @status,
    updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: DeleteEpisode :exec
DELETE FROM episodes WHERE tenant_id = @tenant_id AND id = @id;

-- name: NextEpisodeIdx :one
SELECT COALESCE(MAX(idx), 0) + 1 AS next_idx FROM episodes
WHERE tenant_id = @tenant_id AND series_id = @series_id;
