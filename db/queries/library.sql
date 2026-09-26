-- name: ListLibraryAssets :many
-- One page of the tenant's assets, newest first (ids are UUIDv7, so id
-- order is creation order), with the project each one belongs to and
-- what references it.
SELECT a.id, a.kind, a.mime, a.bytes, a.status, a.created_at, ref.series_id, se.title AS series_title,
       array_remove(ARRAY[
           CASE WHEN EXISTS (SELECT 1 FROM scene_takes t WHERE t.tenant_id = a.tenant_id AND t.asset_id = a.id AND t.selected) THEN 'selected_take' END,
           CASE WHEN EXISTS (SELECT 1 FROM scene_takes t WHERE t.tenant_id = a.tenant_id AND t.asset_id = a.id AND NOT t.selected) THEN 'take' END,
           CASE WHEN EXISTS (SELECT 1 FROM render_segments g WHERE g.tenant_id = a.tenant_id AND g.asset_id = a.id) THEN 'segment' END,
           CASE WHEN EXISTS (SELECT 1 FROM renders r WHERE r.tenant_id = a.tenant_id AND a.id IN (r.asset_id, r.srt_asset_id, r.preview_asset_id)) THEN 'render' END,
           CASE WHEN EXISTS (SELECT 1 FROM character_refs c WHERE c.tenant_id = a.tenant_id AND c.asset_id = a.id) THEN 'character' END
       ], NULL)::text[] AS referenced_by
FROM assets a
LEFT JOIN LATERAL (
    SELECT e.series_id FROM scene_takes t
    JOIN scenes s ON s.tenant_id = t.tenant_id AND s.id = t.scene_id
    JOIN episodes e ON e.tenant_id = s.tenant_id AND e.id = s.episode_id
    WHERE t.tenant_id = a.tenant_id AND t.asset_id = a.id
    UNION ALL
    SELECT e.series_id FROM render_segments g
    JOIN episodes e ON e.tenant_id = g.tenant_id AND e.id = g.episode_id
    WHERE g.tenant_id = a.tenant_id AND g.asset_id = a.id
    UNION ALL
    SELECT e.series_id FROM renders r
    JOIN episodes e ON e.tenant_id = r.tenant_id AND e.id = r.episode_id
    WHERE r.tenant_id = a.tenant_id AND a.id IN (r.asset_id, r.srt_asset_id, r.preview_asset_id)
    LIMIT 1
) ref ON true
LEFT JOIN series se ON se.tenant_id = a.tenant_id AND se.id = ref.series_id
WHERE a.tenant_id = @tenant_id
  AND (sqlc.narg(kind)::text IS NULL OR a.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(series_id)::uuid IS NULL OR ref.series_id = sqlc.narg(series_id)::uuid)
  AND (sqlc.narg(before_id)::uuid IS NULL OR a.id < sqlc.narg(before_id)::uuid)
ORDER BY a.id DESC
LIMIT @max_rows;

-- name: LibraryUsageBySeries :many
-- Storage used per project; assets no project references group under a
-- NULL series.
SELECT ref.series_id, se.title AS series_title, count(*)::bigint AS assets, COALESCE(sum(a.bytes), 0)::bigint AS bytes
FROM assets a
LEFT JOIN LATERAL (
    SELECT e.series_id FROM scene_takes t
    JOIN scenes s ON s.tenant_id = t.tenant_id AND s.id = t.scene_id
    JOIN episodes e ON e.tenant_id = s.tenant_id AND e.id = s.episode_id
    WHERE t.tenant_id = a.tenant_id AND t.asset_id = a.id
    UNION ALL
    SELECT e.series_id FROM render_segments g
    JOIN episodes e ON e.tenant_id = g.tenant_id AND e.id = g.episode_id
    WHERE g.tenant_id = a.tenant_id AND g.asset_id = a.id
    UNION ALL
    SELECT e.series_id FROM renders r
    JOIN episodes e ON e.tenant_id = r.tenant_id AND e.id = r.episode_id
    WHERE r.tenant_id = a.tenant_id AND a.id IN (r.asset_id, r.srt_asset_id, r.preview_asset_id)
    LIMIT 1
) ref ON true
LEFT JOIN series se ON se.tenant_id = a.tenant_id AND se.id = ref.series_id
WHERE a.tenant_id = @tenant_id AND a.status = 'ready'
GROUP BY ref.series_id, se.title
ORDER BY bytes DESC;

-- name: ListExpiredSegments :many
-- Cached render segments unused for ttl_days and not pinned: a segment is
-- pinned while the latest manifest of its episode/lang, or any manifest
-- that produced a render, needs it, or while a render row points at it.
WITH pinned AS (
    SELECT m.id FROM render_manifests m
    WHERE m.tenant_id = @tenant_id AND (
        EXISTS (SELECT 1 FROM renders r WHERE r.tenant_id = m.tenant_id AND r.manifest_id = m.id)
        OR m.id = (
            SELECT m2.id FROM render_manifests m2
            WHERE m2.tenant_id = m.tenant_id AND m2.episode_id = m.episode_id AND m2.lang = m.lang
            ORDER BY m2.created_at DESC, m2.id DESC LIMIT 1
        )
    )
)
SELECT g.input_hash, g.kind, g.asset_id, a.storage_key, COALESCE(a.bytes, 0)::bigint AS bytes, g.last_used_at
FROM render_segments g
JOIN assets a ON a.tenant_id = g.tenant_id AND a.id = g.asset_id
WHERE g.tenant_id = @tenant_id
  AND g.last_used_at < now() - make_interval(days => @ttl_days::int)
  AND NOT EXISTS (
      SELECT 1 FROM render_manifest_segments ms JOIN pinned p ON p.id = ms.manifest_id
      WHERE ms.tenant_id = g.tenant_id AND ms.input_hash = g.input_hash
  )
  AND NOT EXISTS (
      SELECT 1 FROM renders r
      WHERE r.tenant_id = g.tenant_id AND g.asset_id IN (r.asset_id, r.srt_asset_id, r.preview_asset_id)
  )
ORDER BY g.last_used_at, g.input_hash
LIMIT @max_rows;

-- name: ListExpiredTakes :many
-- Unselected takes older than ttl_days that no pinned manifest (see
-- ListExpiredSegments) froze into a render.
WITH pinned AS (
    SELECT m.id, m.scenes FROM render_manifests m
    WHERE m.tenant_id = @tenant_id AND (
        EXISTS (SELECT 1 FROM renders r WHERE r.tenant_id = m.tenant_id AND r.manifest_id = m.id)
        OR m.id = (
            SELECT m2.id FROM render_manifests m2
            WHERE m2.tenant_id = m.tenant_id AND m2.episode_id = m.episode_id AND m2.lang = m.lang
            ORDER BY m2.created_at DESC, m2.id DESC LIMIT 1
        )
    )
)
SELECT t.id AS take_id, t.kind, t.asset_id, a.storage_key, COALESCE(a.bytes, 0)::bigint AS bytes, t.created_at
FROM scene_takes t
JOIN assets a ON a.tenant_id = t.tenant_id AND a.id = t.asset_id
WHERE t.tenant_id = @tenant_id
  AND NOT t.selected
  AND t.created_at < now() - make_interval(days => @ttl_days::int)
  AND NOT EXISTS (
      SELECT 1 FROM pinned p CROSS JOIN LATERAL jsonb_array_elements(p.scenes) e
      WHERE t.asset_id::text IN (e->>'imageAssetId', e->>'voiceAssetId', e->>'alignAssetId')
  )
ORDER BY t.created_at, t.id
LIMIT @max_rows;

-- name: GetLibrarySettings :one
SELECT * FROM library_settings WHERE tenant_id = @tenant_id;

-- name: UpsertLibrarySettings :one
INSERT INTO library_settings (tenant_id, segment_ttl_days, take_ttl_days)
VALUES (@tenant_id, @segment_ttl_days, @take_ttl_days)
ON CONFLICT (tenant_id) DO UPDATE SET
    segment_ttl_days = EXCLUDED.segment_ttl_days, take_ttl_days = EXCLUDED.take_ttl_days, updated_at = now()
RETURNING *;

-- name: MarkLibraryCleanup :exec
INSERT INTO library_settings (tenant_id, last_cleanup_at)
VALUES (@tenant_id, now())
ON CONFLICT (tenant_id) DO UPDATE SET last_cleanup_at = now();

-- name: ListTenantsForCleanup :many
-- Tenants whose daily cleanup is due (never ran, or ran over a day ago).
-- lint-tenant-queries:allow: the daily scheduler lists due tenant ids across tenants; each cleanup then runs tenant-scoped
SELECT t.id FROM tenants t
LEFT JOIN library_settings l ON l.tenant_id = t.id
WHERE l.last_cleanup_at IS NULL OR l.last_cleanup_at < now() - interval '1 day'
ORDER BY t.id;
