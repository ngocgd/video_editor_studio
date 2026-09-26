-- name: ListManifestSceneInputs :many
-- Every scene of an episode/lang in order with its selected image, voice
-- and align takes: the raw material a render manifest freezes.
SELECT s.id AS scene_id, s.idx, s.motion_preset, s.duration_ms, s.tainted,
       img.asset_id AS image_asset_id, ia.sha256 AS image_sha256, img.params AS image_params,
       vo.asset_id AS voice_asset_id, va.sha256 AS voice_sha256, va.duration_ms AS voice_duration_ms,
       al.asset_id AS align_asset_id, aa.sha256 AS align_sha256
FROM scenes s
LEFT JOIN scene_takes img ON img.tenant_id = s.tenant_id AND img.scene_id = s.id AND img.kind = 'image' AND img.selected
LEFT JOIN assets ia ON ia.tenant_id = s.tenant_id AND ia.id = img.asset_id AND ia.status = 'ready'
LEFT JOIN scene_takes vo ON vo.tenant_id = s.tenant_id AND vo.scene_id = s.id AND vo.kind = 'voice' AND vo.selected
LEFT JOIN assets va ON va.tenant_id = s.tenant_id AND va.id = vo.asset_id AND va.status = 'ready'
LEFT JOIN scene_takes al ON al.tenant_id = s.tenant_id AND al.scene_id = s.id AND al.kind = 'align' AND al.selected
LEFT JOIN assets aa ON aa.tenant_id = s.tenant_id AND aa.id = al.asset_id AND aa.status = 'ready'
WHERE s.tenant_id = @tenant_id AND s.episode_id = @episode_id AND s.lang = @lang
ORDER BY s.idx;

-- name: InsertRenderManifest :one
INSERT INTO render_manifests (id, tenant_id, episode_id, lang, settings, settings_hash, scenes, hash, restarted_after_edit, reused_segments, created_by)
VALUES (@id, @tenant_id, @episode_id, @lang, @settings, @settings_hash, @scenes, @hash, @restarted_after_edit, @reused_segments, @created_by)
RETURNING *;

-- name: InsertManifestSegments :exec
INSERT INTO render_manifest_segments (manifest_id, tenant_id, input_hash)
SELECT @manifest_id, @tenant_id, unnest(@input_hashes::text[])
ON CONFLICT DO NOTHING;

-- name: SetManifestRun :one
UPDATE render_manifests SET run_id = @run_id
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: GetRenderManifest :one
SELECT * FROM render_manifests WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetRenderManifestByRun :one
SELECT * FROM render_manifests WHERE tenant_id = @tenant_id AND run_id = @run_id;

-- name: LatestRenderManifest :one
SELECT * FROM render_manifests
WHERE tenant_id = @tenant_id AND episode_id = @episode_id AND lang = @lang
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: GetActiveRenderRun :one
-- The newest manifest of an episode/lang whose run is still active: the
-- run a scene edit supersedes.
SELECT m.id AS manifest_id, m.run_id, m.hash
FROM render_manifests m
JOIN pipeline_runs r ON r.tenant_id = m.tenant_id AND r.id = m.run_id
WHERE m.tenant_id = @tenant_id AND m.episode_id = @episode_id AND m.lang = @lang AND r.status = 'active'
ORDER BY m.created_at DESC, m.id DESC
LIMIT 1;

-- name: GetEpisodeForRender :one
SELECT e.id, e.series_id, e.idx, e.title, s.title AS series_title, s.target_languages
FROM episodes e
JOIN series s ON s.tenant_id = e.tenant_id AND s.id = e.series_id
WHERE e.tenant_id = @tenant_id AND e.id = @id;

-- name: DeleteRenderManifest :exec
-- Removes a manifest whose run could not be enqueued.
DELETE FROM render_manifests WHERE tenant_id = @tenant_id AND id = @id AND run_id IS NULL;

-- name: ManifestCacheProgress :one
-- How many of a manifest's pinned cache entries are encoded so far.
SELECT count(*)::int AS total, count(s.input_hash)::int AS cached
FROM render_manifest_segments ms
LEFT JOIN render_segments s ON s.tenant_id = ms.tenant_id AND s.input_hash = ms.input_hash
WHERE ms.tenant_id = @tenant_id AND ms.manifest_id = @manifest_id;
