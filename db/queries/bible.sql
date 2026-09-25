-- name: GetStoryBible :one
SELECT * FROM story_bibles WHERE tenant_id = @tenant_id AND series_id = @series_id;

-- name: CreateStoryBible :one
INSERT INTO story_bibles (id, tenant_id, series_id, sections)
VALUES (@id, @tenant_id, @series_id, @sections)
RETURNING *;

-- name: UpdateStoryBibleSections :one
-- The caller reads-modifies-writes the whole `sections` jsonb map (one
-- section at a time) after checking the per-section version it already
-- fetched, since Postgres has no per-key jsonb CAS.
UPDATE story_bibles
SET sections = @sections,
    updated_at = now()
WHERE tenant_id = @tenant_id AND series_id = @series_id
RETURNING *;
