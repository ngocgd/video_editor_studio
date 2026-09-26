-- Analytics: tracked videos, daily metrics from the Analytics API and the
-- Reporting API reach report, retention, sync bookkeeping, suggestions.
-- Batched writes take their rows as one jsonb array so NULL ("not
-- available from the API") survives the trip, which Go slices cannot carry.

-- name: ListChannelsForAnalyticsSync :many
-- lint-tenant-queries:allow: the daily sync walks every tenant's connected channels
SELECT id, tenant_id FROM youtube_channels
WHERE status = 'connected'
ORDER BY tenant_id, id;

-- name: UpsertTrackedVideo :one
-- A video already tracked keeps its source when it came from a
-- publication (a manual add never downgrades it) and gains metadata.
INSERT INTO analytics_tracked_videos (
    tenant_id, channel_id, youtube_video_id, source, title, duration_seconds, published_at, added_by
) VALUES (
    @tenant_id, @channel_id, @youtube_video_id, @source, @title, @duration_seconds, @published_at, @added_by
)
ON CONFLICT (tenant_id, youtube_video_id) DO UPDATE SET
    source = CASE WHEN analytics_tracked_videos.source = 'publication' THEN 'publication' ELSE EXCLUDED.source END,
    title = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title ELSE analytics_tracked_videos.title END,
    duration_seconds = COALESCE(EXCLUDED.duration_seconds, analytics_tracked_videos.duration_seconds),
    published_at = COALESCE(EXCLUDED.published_at, analytics_tracked_videos.published_at)
WHERE analytics_tracked_videos.channel_id = EXCLUDED.channel_id
RETURNING *;

-- name: ListTrackedVideos :many
SELECT * FROM analytics_tracked_videos
WHERE tenant_id = @tenant_id AND channel_id = @channel_id
ORDER BY published_at NULLS LAST, youtube_video_id;

-- name: GetTrackedVideo :one
SELECT * FROM analytics_tracked_videos
WHERE tenant_id = @tenant_id AND youtube_video_id = @youtube_video_id;

-- name: DeleteTrackedVideo :execrows
DELETE FROM analytics_tracked_videos
WHERE tenant_id = @tenant_id AND youtube_video_id = @youtube_video_id AND source = 'manual';

-- name: UpsertVideoAnalyticsDaily :exec
-- rows: [{"video":..,"date":"YYYY-MM-DD","views":..,"minutes":..,
-- "avg_duration":..,"avg_percentage":..,"subs_gained":..,"unavailable":{..}}].
-- Only the Analytics API columns are written; the keys listed in
-- clear_keys are dropped from unavailable before the row's own reasons
-- are merged in, so reach-report reasons are left alone.
INSERT INTO video_metrics_daily AS m (
    tenant_id, channel_id, youtube_video_id, date, views, estimated_minutes_watched,
    average_view_duration, average_view_percentage, subscribers_gained, unavailable, analytics_synced_at
)
SELECT @tenant_id, @channel_id, r.video, r.date, r.views, r.minutes,
       r.avg_duration, r.avg_percentage, r.subs_gained, COALESCE(r.unavailable, '{}'), now()
FROM jsonb_to_recordset(@rows::jsonb) AS r(
    video text, date date, views bigint, minutes double precision,
    avg_duration double precision, avg_percentage double precision, subs_gained bigint, unavailable jsonb
)
ON CONFLICT (tenant_id, youtube_video_id, date) DO UPDATE SET
    channel_id = EXCLUDED.channel_id,
    views = EXCLUDED.views,
    estimated_minutes_watched = EXCLUDED.estimated_minutes_watched,
    average_view_duration = EXCLUDED.average_view_duration,
    average_view_percentage = EXCLUDED.average_view_percentage,
    subscribers_gained = EXCLUDED.subscribers_gained,
    unavailable = (m.unavailable - @clear_keys::text[]) || EXCLUDED.unavailable,
    analytics_synced_at = now()
WHERE m.tenant_id = @tenant_id;

-- name: UpsertVideoReachDaily :exec
-- rows: [{"video":..,"date":"YYYY-MM-DD","impressions":..,"ctr":..}], one
-- per video and day, already aggregated across the report's other
-- dimensions. Values replace (never add to) what is stored, so a report
-- re-delivered or a backfill for the same days cannot double count.
INSERT INTO video_metrics_daily AS m (
    tenant_id, channel_id, youtube_video_id, date, impressions, ctr, reach_synced_at
)
SELECT @tenant_id, @channel_id, r.video, r.date, r.impressions, r.ctr, now()
FROM jsonb_to_recordset(@rows::jsonb) AS r(video text, date date, impressions bigint, ctr double precision)
ON CONFLICT (tenant_id, youtube_video_id, date) DO UPDATE SET
    impressions = EXCLUDED.impressions,
    ctr = EXCLUDED.ctr,
    unavailable = m.unavailable - 'impressions' - 'ctr',
    reach_synced_at = now()
WHERE m.tenant_id = @tenant_id;

-- name: UpsertChannelMetricsDaily :exec
-- rows: [{"date":"YYYY-MM-DD","views":..,"minutes":..,"subs_gained":..,
-- "subs_lost":..,"unavailable":{..}}].
INSERT INTO channel_metrics_daily AS c (
    tenant_id, channel_id, date, views, estimated_minutes_watched,
    subscribers_gained, subscribers_lost, unavailable, synced_at
)
SELECT @tenant_id, @channel_id, r.date, r.views, r.minutes, r.subs_gained, r.subs_lost,
       COALESCE(r.unavailable, '{}'), now()
FROM jsonb_to_recordset(@rows::jsonb) AS r(
    date date, views bigint, minutes double precision, subs_gained bigint, subs_lost bigint, unavailable jsonb
)
ON CONFLICT (tenant_id, channel_id, date) DO UPDATE SET
    views = EXCLUDED.views,
    estimated_minutes_watched = EXCLUDED.estimated_minutes_watched,
    subscribers_gained = EXCLUDED.subscribers_gained,
    subscribers_lost = EXCLUDED.subscribers_lost,
    unavailable = EXCLUDED.unavailable,
    synced_at = now()
WHERE c.tenant_id = @tenant_id;

-- name: DeleteVideoRetention :exec
DELETE FROM video_retention
WHERE tenant_id = @tenant_id AND youtube_video_id = @youtube_video_id;

-- name: InsertVideoRetention :exec
-- rows: [{"ratio":..,"watch":..,"relative":..}].
INSERT INTO video_retention (
    tenant_id, channel_id, youtube_video_id, elapsed_ratio,
    audience_watch_ratio, relative_retention_performance
)
SELECT @tenant_id, @channel_id, @youtube_video_id, r.ratio, r.watch, r.relative
FROM jsonb_to_recordset(@rows::jsonb) AS r(ratio double precision, watch double precision, relative double precision);

-- name: GetAnalyticsSyncState :one
SELECT * FROM analytics_sync_state
WHERE tenant_id = @tenant_id AND channel_id = @channel_id;

-- name: StartAnalyticsSync :one
-- Marks a channel's sync running. Returns no row when another sync of
-- the same channel started less than stale_after ago and is still
-- running, so concurrent syncs of one channel never overlap.
INSERT INTO analytics_sync_state AS s (tenant_id, channel_id, status, last_started_at, updated_at)
VALUES (@tenant_id, @channel_id, 'running', now(), now())
ON CONFLICT (tenant_id, channel_id) DO UPDATE SET
    status = 'running',
    last_started_at = now(),
    updated_at = now()
WHERE s.tenant_id = @tenant_id
  AND (s.status <> 'running' OR s.last_started_at < now() - @stale_after::interval)
RETURNING *;

-- name: FinishAnalyticsSync :exec
-- A NULL through date or subscriber count keeps the stored one.
UPDATE analytics_sync_state SET
    status = 'idle',
    analytics_through = COALESCE(sqlc.narg(analytics_through)::date, analytics_through),
    reach_through = COALESCE(sqlc.narg(reach_through)::date, reach_through),
    subscriber_count = COALESCE(sqlc.narg(subscriber_count)::bigint, subscriber_count),
    last_finished_at = now(),
    last_error = @last_error,
    updated_at = now()
WHERE tenant_id = @tenant_id AND channel_id = @channel_id;

-- name: FailAnalyticsSync :exec
UPDATE analytics_sync_state SET
    status = 'failed',
    last_finished_at = now(),
    last_error = @last_error,
    updated_at = now()
WHERE tenant_id = @tenant_id AND channel_id = @channel_id;

-- name: GetReportingJob :one
SELECT * FROM analytics_reporting_jobs
WHERE tenant_id = @tenant_id AND channel_id = @channel_id AND report_type_id = @report_type_id;

-- name: InsertReportingJob :one
INSERT INTO analytics_reporting_jobs (tenant_id, channel_id, report_type_id, job_id)
VALUES (@tenant_id, @channel_id, @report_type_id, @job_id)
ON CONFLICT (tenant_id, channel_id, report_type_id) DO UPDATE SET job_id = EXCLUDED.job_id
RETURNING *;

-- name: AdvanceReportingJobCursor :exec
UPDATE analytics_reporting_jobs SET
    last_report_created_at = GREATEST(last_report_created_at, @created_at::timestamptz)
WHERE tenant_id = @tenant_id AND channel_id = @channel_id AND report_type_id = @report_type_id;

-- name: ClaimReportingReport :one
-- Records a report as ingested. Returns no row when it already was, and
-- must run in the same transaction as the report's rows.
INSERT INTO analytics_reporting_reports (tenant_id, channel_id, job_id, report_id, start_time, end_time, rows_ingested)
VALUES (@tenant_id, @channel_id, @job_id, @report_id, @start_time, @end_time, @rows_ingested)
ON CONFLICT (tenant_id, job_id, report_id) DO NOTHING
RETURNING report_id;

-- name: ReachDataThrough :one
-- The last day with reach data for the channel, NULL before the first
-- reach report has been ingested.
SELECT max(date)::date AS through FROM video_metrics_daily
WHERE tenant_id = @tenant_id AND channel_id = @channel_id AND impressions IS NOT NULL;

-- name: ChannelDailySeries :many
SELECT date, views, estimated_minutes_watched, subscribers_gained, subscribers_lost, unavailable
FROM channel_metrics_daily
WHERE tenant_id = @tenant_id AND channel_id = @channel_id
  AND date >= @from_date AND date <= @to_date
ORDER BY date;

-- name: ChannelWatchHoursWindow :one
-- Public watch hours over [from_date, to_date], the YPP 12-month window.
SELECT (COALESCE(sum(estimated_minutes_watched), 0) / 60)::float8 AS watch_hours,
       count(*) FILTER (WHERE estimated_minutes_watched IS NULL)::int AS days_missing
FROM channel_metrics_daily
WHERE tenant_id = @tenant_id AND channel_id = @channel_id
  AND date >= @from_date AND date <= @to_date;

-- name: ListVideoTotals :many
-- Tracked videos of a channel with totals over [from_date, to_date].
-- Averages are view-weighted and CTR is impression-weighted. Each *_days
-- count says on how many days the metric was available: 0 means "not
-- available from the API" rather than a real zero.
SELECT t.youtube_video_id, t.title, t.source, t.duration_seconds, t.published_at,
       COALESCE(sum(m.views), 0)::bigint AS views,
       count(m.views)::int AS views_days,
       COALESCE(sum(m.estimated_minutes_watched), 0)::float8 AS minutes_watched,
       count(m.estimated_minutes_watched)::int AS minutes_days,
       COALESCE(sum(m.average_view_percentage * m.views) / NULLIF(sum(m.views) FILTER (WHERE m.average_view_percentage IS NOT NULL), 0), 0)::float8 AS average_view_percentage,
       count(m.average_view_percentage)::int AS average_view_percentage_days,
       COALESCE(sum(m.average_view_duration * m.views) / NULLIF(sum(m.views) FILTER (WHERE m.average_view_duration IS NOT NULL), 0), 0)::float8 AS average_view_duration,
       count(m.average_view_duration)::int AS average_view_duration_days,
       COALESCE(sum(m.subscribers_gained), 0)::bigint AS subscribers_gained,
       COALESCE(sum(m.impressions), 0)::bigint AS impressions,
       count(m.impressions)::int AS impressions_days,
       COALESCE(sum(m.ctr * m.impressions) / NULLIF(sum(m.impressions) FILTER (WHERE m.ctr IS NOT NULL), 0), 0)::float8 AS ctr,
       count(m.ctr)::int AS ctr_days
FROM analytics_tracked_videos t
LEFT JOIN video_metrics_daily m
  ON m.tenant_id = t.tenant_id AND m.youtube_video_id = t.youtube_video_id
 AND m.date >= @from_date AND m.date <= @to_date
WHERE t.tenant_id = @tenant_id AND t.channel_id = @channel_id
GROUP BY t.youtube_video_id, t.title, t.source, t.duration_seconds, t.published_at;

-- name: VideoDailySeries :many
SELECT date, views, estimated_minutes_watched, average_view_duration, average_view_percentage,
       subscribers_gained, impressions, ctr, unavailable
FROM video_metrics_daily
WHERE tenant_id = @tenant_id AND youtube_video_id = @youtube_video_id
  AND date >= @from_date AND date <= @to_date
ORDER BY date;

-- name: VideoRetentionCurve :many
SELECT elapsed_ratio, audience_watch_ratio, relative_retention_performance, synced_at
FROM video_retention
WHERE tenant_id = @tenant_id AND youtube_video_id = @youtube_video_id
ORDER BY elapsed_ratio;

-- name: UpsertSuggestion :exec
-- A new rule version re-opens a dismissed suggestion.
INSERT INTO analytics_suggestions AS s (tenant_id, channel_id, youtube_video_id, rule, version, evidence)
VALUES (@tenant_id, @channel_id, @youtube_video_id, @rule, @version, @evidence)
ON CONFLICT (tenant_id, channel_id, youtube_video_id, rule) DO UPDATE SET
    dismissed = s.dismissed AND s.version = EXCLUDED.version,
    version = EXCLUDED.version,
    evidence = EXCLUDED.evidence,
    updated_at = now()
WHERE s.tenant_id = @tenant_id;

-- name: DeleteSuggestionsExcept :exec
-- Drops the channel's suggestions whose "video/rule" key is not in keep:
-- the rule no longer fires for them.
DELETE FROM analytics_suggestions
WHERE tenant_id = @tenant_id AND channel_id = @channel_id
  AND NOT ((youtube_video_id || '/' || rule) = ANY (@keep::text[]));

-- name: ListSuggestions :many
SELECT * FROM analytics_suggestions
WHERE tenant_id = @tenant_id AND channel_id = @channel_id
  AND (sqlc.narg(youtube_video_id)::text IS NULL OR youtube_video_id = sqlc.narg(youtube_video_id)::text)
  AND (@include_dismissed::boolean OR NOT dismissed)
ORDER BY youtube_video_id, rule;

-- name: SetSuggestionDismissed :execrows
UPDATE analytics_suggestions SET dismissed = @dismissed, updated_at = now()
WHERE tenant_id = @tenant_id AND channel_id = @channel_id
  AND youtube_video_id = @youtube_video_id AND rule = @rule;
