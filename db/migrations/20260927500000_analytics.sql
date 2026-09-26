-- +goose Up
-- YouTube analytics: daily per-video and per-channel metrics synced from
-- the YouTube Analytics API, impressions and click-through rate from the
-- YouTube Reporting API reach report, per-video retention curves, the
-- sync bookkeeping for both sources, and rule-based suggestions.
--
-- Every metric column is nullable: NULL means "not available from the
-- API" (the reason is kept in the row's unavailable map), never zero.

-- Videos listed in the analytics video table. Channel totals cover every
-- video on the channel; only these videos get per-video rows. source is
-- 'publication' for videos this app uploaded and 'manual' for an existing
-- video a user added for tracking.
CREATE TABLE analytics_tracked_videos (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES youtube_channels (id) ON DELETE CASCADE,
    youtube_video_id text NOT NULL CHECK (youtube_video_id ~ '^[A-Za-z0-9_-]{11}$'),
    source text NOT NULL CHECK (source IN ('publication', 'manual')),
    title text NOT NULL DEFAULT '' CHECK (length(title) <= 200),
    duration_seconds integer CHECK (duration_seconds >= 0),
    published_at timestamptz,
    added_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, youtube_video_id)
);
CREATE INDEX analytics_tracked_videos_channel ON analytics_tracked_videos (tenant_id, channel_id);

-- One row per tracked video per day (the day as YouTube reports it, in
-- Pacific time). Analytics API columns and reach columns are written by
-- two independent syncs; each only touches its own columns and its own
-- keys in unavailable ({"metric": "reason"}).
CREATE TABLE video_metrics_daily (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES youtube_channels (id) ON DELETE CASCADE,
    youtube_video_id text NOT NULL,
    date date NOT NULL,
    views bigint,
    estimated_minutes_watched double precision,
    -- Seconds.
    average_view_duration double precision,
    -- Percent of the video watched, 0-100 (can exceed 100 on rewatches).
    average_view_percentage double precision,
    subscribers_gained bigint,
    -- From the reach report. ctr is a fraction (0-1).
    impressions bigint,
    ctr double precision,
    unavailable jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(unavailable) = 'object'),
    analytics_synced_at timestamptz,
    reach_synced_at timestamptz,
    PRIMARY KEY (tenant_id, youtube_video_id, date)
);
CREATE INDEX video_metrics_daily_channel_date ON video_metrics_daily (tenant_id, channel_id, date);

-- Audience retention for one video over its lifetime, 100 buckets of
-- elapsedVideoTimeRatio (0.01 .. 1.00). Replaced wholesale on each sync.
CREATE TABLE video_retention (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES youtube_channels (id) ON DELETE CASCADE,
    youtube_video_id text NOT NULL,
    elapsed_ratio double precision NOT NULL CHECK (elapsed_ratio >= 0 AND elapsed_ratio <= 1),
    audience_watch_ratio double precision,
    relative_retention_performance double precision,
    synced_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, youtube_video_id, elapsed_ratio)
);

-- Channel totals per day (every video, not only tracked ones).
CREATE TABLE channel_metrics_daily (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES youtube_channels (id) ON DELETE CASCADE,
    date date NOT NULL,
    views bigint,
    estimated_minutes_watched double precision,
    subscribers_gained bigint,
    subscribers_lost bigint,
    unavailable jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(unavailable) = 'object'),
    synced_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, channel_id, date)
);

-- Per-channel sync cursor and status. analytics_through and reach_through
-- are the last day each source has delivered data for; the UI shows them
-- as "data through <date>".
CREATE TABLE analytics_sync_state (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES youtube_channels (id) ON DELETE CASCADE,
    analytics_through date,
    reach_through date,
    -- Current subscriber count from channels.list; NULL when hidden.
    subscriber_count bigint,
    status text NOT NULL DEFAULT 'idle' CHECK (status IN ('idle', 'running', 'failed')),
    last_started_at timestamptz,
    last_finished_at timestamptz,
    -- Stable reason code (e.g. reconnect_needed, quota) for the UI.
    last_error text NOT NULL DEFAULT '' CHECK (length(last_error) <= 500),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, channel_id)
);

-- The Reporting API job created for a channel's reach report type.
CREATE TABLE analytics_reporting_jobs (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES youtube_channels (id) ON DELETE CASCADE,
    report_type_id text NOT NULL CHECK (report_type_id <> ''),
    job_id text NOT NULL CHECK (job_id <> ''),
    -- createTime of the newest report seen, passed back as createdAfter.
    last_report_created_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, channel_id, report_type_id)
);

-- Reports already ingested: a report id is ingested exactly once, in the
-- same transaction as its rows.
CREATE TABLE analytics_reporting_reports (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES youtube_channels (id) ON DELETE CASCADE,
    job_id text NOT NULL,
    report_id text NOT NULL,
    start_time timestamptz NOT NULL,
    end_time timestamptz NOT NULL,
    rows_ingested integer NOT NULL DEFAULT 0 CHECK (rows_ingested >= 0),
    ingested_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, job_id, report_id)
);

-- Suggestions produced by the versioned rules. youtube_video_id is ''
-- for a channel-level suggestion. A dismissal sticks until the rule
-- version changes.
CREATE TABLE analytics_suggestions (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id uuid NOT NULL REFERENCES youtube_channels (id) ON DELETE CASCADE,
    youtube_video_id text NOT NULL DEFAULT '',
    rule text NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    evidence jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(evidence) = 'object'),
    dismissed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, channel_id, youtube_video_id, rule)
);

-- +goose Down
DROP TABLE analytics_suggestions;
DROP TABLE analytics_reporting_reports;
DROP TABLE analytics_reporting_jobs;
DROP TABLE analytics_sync_state;
DROP TABLE channel_metrics_daily;
DROP TABLE video_retention;
DROP TABLE video_metrics_daily;
DROP TABLE analytics_tracked_videos;
