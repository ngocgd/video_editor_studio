-- name: GetStoryBible :one
SELECT * FROM story_bibles WHERE tenant_id = @tenant_id AND series_id = @series_id;

-- name: CreateStoryBible :one
INSERT INTO story_bibles (id, tenant_id, series_id, sections)
VALUES (@id, @tenant_id, @series_id, @sections)
RETURNING *;

-- name: UpdateStoryBibleSection :one
-- Replaces one section only if it is still at @expected_version (0 for a
-- section that does not exist yet). Other sections are left untouched, so
-- concurrent edits of different sections both land, and a second edit of
-- the same section finds no row and is reported as a conflict.
UPDATE story_bibles
SET sections = jsonb_set(sections, ARRAY[@section::text], @doc::jsonb),
    updated_at = now()
WHERE tenant_id = @tenant_id AND series_id = @series_id
  AND COALESCE((sections -> @section::text ->> 'version')::int, 0) = @expected_version::int
RETURNING *;

-- name: SeedStoryBibleSection :exec
-- Writes a generated section unless a person has written that section:
-- regenerating the bible never overwrites user edits.
UPDATE story_bibles
SET sections = jsonb_set(sections, ARRAY[@section::text], jsonb_build_object(
        'content', @content::text,
        'origin', 'model',
        'tainted', false,
        'version', COALESCE((sections -> @section::text ->> 'version')::int, 0) + 1)),
    updated_at = now()
WHERE tenant_id = @tenant_id AND series_id = @series_id
  AND COALESCE(sections -> @section::text ->> 'origin', '') <> 'user';
