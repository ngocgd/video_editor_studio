-- name: GetStoryboardSettings :one
SELECT * FROM series_storyboard_settings WHERE tenant_id = @tenant_id AND series_id = @series_id;

-- name: UpsertStoryboardSettings :one
INSERT INTO series_storyboard_settings (series_id, tenant_id, image_style_id, cadence_min_s, cadence_max_s, segment_gap_ms)
VALUES (@series_id, @tenant_id, @image_style_id, @cadence_min_s, @cadence_max_s, @segment_gap_ms)
ON CONFLICT (series_id) DO UPDATE
SET image_style_id = EXCLUDED.image_style_id, cadence_min_s = EXCLUDED.cadence_min_s,
    cadence_max_s = EXCLUDED.cadence_max_s, segment_gap_ms = EXCLUDED.segment_gap_ms, updated_at = now()
WHERE series_storyboard_settings.tenant_id = @tenant_id
RETURNING *;

-- name: GetScene :one
SELECT * FROM scenes WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListScenes :many
SELECT * FROM scenes WHERE tenant_id = @tenant_id AND episode_id = @episode_id AND lang = @lang ORDER BY idx;

-- name: GetScenesByIDs :many
SELECT * FROM scenes WHERE tenant_id = @tenant_id AND id = ANY(@ids::uuid[]) ORDER BY idx;

-- name: InsertScene :one
INSERT INTO scenes (id, tenant_id, episode_id, lang, idx, paragraph_ids, narration, segments, image_prompt,
                    character_ids, image_style_id, duration_ms, text_hash, tainted)
VALUES (@id, @tenant_id, @episode_id, @lang, @idx, @paragraph_ids, @narration, @segments, @image_prompt,
        @character_ids, @image_style_id, @duration_ms, @text_hash, @tainted)
RETURNING *;

-- name: ResplitKeepScene :one
-- A re-split kept this scene because its narration hash is unchanged:
-- only its position and paragraph ids move, so every take stays valid.
UPDATE scenes
SET idx = @idx, paragraph_ids = @paragraph_ids, tainted = @tainted, version = version + 1, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: DeleteScenesExcept :many
-- Drops the scenes a re-split did not keep (their takes cascade).
DELETE FROM scenes
WHERE tenant_id = @tenant_id AND episode_id = @episode_id AND lang = @lang AND NOT (id = ANY(@keep_ids::uuid[]))
RETURNING id;

-- name: UpdateSceneEdit :one
-- Optimistic concurrency: a stale expected_version updates nothing.
UPDATE scenes
SET narration = @narration, segments = @segments, image_prompt = @image_prompt, character_ids = @character_ids,
    motion_preset = @motion_preset, image_style_id = @image_style_id, text_hash = @text_hash,
    duration_ms = CASE WHEN duration_measured THEN duration_ms ELSE @estimated_duration_ms END,
    version = version + 1, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND version = @expected_version
RETURNING *;

-- name: SetSceneMeasuredDuration :one
UPDATE scenes
SET duration_ms = @duration_ms, duration_measured = true, version = version + 1, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: TouchScene :one
UPDATE scenes SET version = version + 1, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: SceneRollup :many
-- The storyboard's one query per episode: every scene with the latest
-- pipeline step of each per-scene kind and the selected take of each
-- kind (with its asset), read through LATERAL joins instead of one query
-- per scene.
SELECT s.*,
    img.j AS image_step, voi.j AS voice_step, ali.j AS align_step,
    timg.j AS image_take, tvoi.j AS voice_take, tali.j AS align_take
FROM scenes s
LEFT JOIN LATERAL (
    SELECT jsonb_build_object('id', p.id, 'runId', p.run_id, 'status', p.status, 'progress', p.progress,
                              'errorCode', p.error_code, 'errorMsg', p.error_msg, 'priority', p.priority) AS j
    FROM pipeline_steps p
    WHERE p.tenant_id = @tenant_id AND p.scope_kind = 'scene' AND p.scope_id = s.id AND p.kind = 'image.generate'
    ORDER BY p.id DESC LIMIT 1
) img ON true
LEFT JOIN LATERAL (
    SELECT jsonb_build_object('id', p.id, 'runId', p.run_id, 'status', p.status, 'progress', p.progress,
                              'errorCode', p.error_code, 'errorMsg', p.error_msg, 'priority', p.priority) AS j
    FROM pipeline_steps p
    WHERE p.tenant_id = @tenant_id AND p.scope_kind = 'scene' AND p.scope_id = s.id AND p.kind = 'voice.synthesize'
    ORDER BY p.id DESC LIMIT 1
) voi ON true
LEFT JOIN LATERAL (
    SELECT jsonb_build_object('id', p.id, 'runId', p.run_id, 'status', p.status, 'progress', p.progress,
                              'errorCode', p.error_code, 'errorMsg', p.error_msg, 'priority', p.priority) AS j
    FROM pipeline_steps p
    WHERE p.tenant_id = @tenant_id AND p.scope_kind = 'scene' AND p.scope_id = s.id AND p.kind = 'align.subtitles'
    ORDER BY p.id DESC LIMIT 1
) ali ON true
LEFT JOIN LATERAL (
    SELECT jsonb_build_object('id', t.id, 'assetId', t.asset_id, 'inputHash', t.input_hash, 'params', t.params,
                              'variants', a.variants, 'durationMs', a.duration_ms, 'createdAt', t.created_at) AS j
    FROM scene_takes t JOIN assets a ON a.id = t.asset_id AND a.tenant_id = @tenant_id
    WHERE t.tenant_id = @tenant_id AND t.scene_id = s.id AND t.kind = 'image' AND t.selected
) timg ON true
LEFT JOIN LATERAL (
    SELECT jsonb_build_object('id', t.id, 'assetId', t.asset_id, 'inputHash', t.input_hash, 'params', t.params,
                              'variants', a.variants, 'durationMs', a.duration_ms, 'createdAt', t.created_at) AS j
    FROM scene_takes t JOIN assets a ON a.id = t.asset_id AND a.tenant_id = @tenant_id
    WHERE t.tenant_id = @tenant_id AND t.scene_id = s.id AND t.kind = 'voice' AND t.selected
) tvoi ON true
LEFT JOIN LATERAL (
    SELECT jsonb_build_object('id', t.id, 'assetId', t.asset_id, 'inputHash', t.input_hash, 'params', t.params,
                              'variants', a.variants, 'durationMs', a.duration_ms, 'createdAt', t.created_at) AS j
    FROM scene_takes t JOIN assets a ON a.id = t.asset_id AND a.tenant_id = @tenant_id
    WHERE t.tenant_id = @tenant_id AND t.scene_id = s.id AND t.kind = 'align' AND t.selected
) tali ON true
WHERE s.tenant_id = @tenant_id AND s.episode_id = @episode_id AND s.lang = @lang
ORDER BY s.idx;
