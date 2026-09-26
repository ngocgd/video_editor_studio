-- name: GetRenderSettings :one
SELECT * FROM render_settings WHERE tenant_id = @tenant_id AND episode_id = @episode_id AND lang = @lang;

-- name: UpsertRenderSettings :one
INSERT INTO render_settings (
    episode_id, lang, tenant_id, width, height, fps, encoder, subtitles, subtitle_style,
    default_motion, crossfade_ms, loudness_lufs_x10, true_peak_dbtp_x10
) VALUES (
    @episode_id, @lang, @tenant_id, @width, @height, @fps, @encoder, @subtitles, @subtitle_style,
    @default_motion, @crossfade_ms, @loudness_lufs_x10, @true_peak_dbtp_x10
)
ON CONFLICT (episode_id, lang) DO UPDATE SET
    width = EXCLUDED.width, height = EXCLUDED.height, fps = EXCLUDED.fps, encoder = EXCLUDED.encoder,
    subtitles = EXCLUDED.subtitles, subtitle_style = EXCLUDED.subtitle_style,
    default_motion = EXCLUDED.default_motion, crossfade_ms = EXCLUDED.crossfade_ms,
    loudness_lufs_x10 = EXCLUDED.loudness_lufs_x10, true_peak_dbtp_x10 = EXCLUDED.true_peak_dbtp_x10,
    updated_at = now()
WHERE render_settings.tenant_id = EXCLUDED.tenant_id
RETURNING *;

-- name: GetRenderSegment :one
-- A cached segment with the checksum its asset row recorded; the caller
-- compares it with the stored object's checksum before reusing it.
SELECT g.*, a.storage_key, a.storage_version_id, a.sha256, a.bytes, a.status
FROM render_segments g
JOIN assets a ON a.tenant_id = g.tenant_id AND a.id = g.asset_id
WHERE g.tenant_id = @tenant_id AND g.input_hash = @input_hash;

-- name: UpsertRenderSegment :one
INSERT INTO render_segments (tenant_id, input_hash, kind, episode_id, lang, asset_id, duration_frames)
VALUES (@tenant_id, @input_hash, @kind, @episode_id, @lang, @asset_id, @duration_frames)
ON CONFLICT (tenant_id, input_hash) DO UPDATE SET
    asset_id = EXCLUDED.asset_id, duration_frames = EXCLUDED.duration_frames, last_used_at = now()
RETURNING *;

-- name: TouchRenderSegments :exec
UPDATE render_segments SET last_used_at = now()
WHERE tenant_id = @tenant_id AND input_hash = ANY(@input_hashes::text[]);

-- name: DeleteAssetRow :exec
-- Drops an asset row (a cached segment whose object no longer matches
-- its checksum); dependent cache rows go with it.
DELETE FROM assets WHERE tenant_id = @tenant_id AND id = @id;

-- name: InsertRender :one
INSERT INTO renders (
    id, tenant_id, episode_id, lang, manifest_id, settings_hash, asset_id, srt_asset_id,
    preview_asset_id, sha256, duration_ms, encoder, report
) VALUES (
    @id, @tenant_id, @episode_id, @lang, @manifest_id, @settings_hash, @asset_id, @srt_asset_id,
    @preview_asset_id, @sha256, @duration_ms, @encoder, @report
)
ON CONFLICT (manifest_id) DO UPDATE SET
    asset_id = EXCLUDED.asset_id, srt_asset_id = EXCLUDED.srt_asset_id, sha256 = EXCLUDED.sha256,
    duration_ms = EXCLUDED.duration_ms, encoder = EXCLUDED.encoder, report = EXCLUDED.report
WHERE renders.tenant_id = EXCLUDED.tenant_id
RETURNING *;

-- name: SetRenderPreview :one
UPDATE renders SET preview_asset_id = @preview_asset_id
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: GetRender :one
SELECT * FROM renders WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetRenderByManifest :one
SELECT * FROM renders WHERE tenant_id = @tenant_id AND manifest_id = @manifest_id;

-- name: ListRenders :many
SELECT * FROM renders
WHERE tenant_id = @tenant_id AND episode_id = @episode_id AND lang = @lang
ORDER BY created_at DESC, id DESC
LIMIT @max_rows;
