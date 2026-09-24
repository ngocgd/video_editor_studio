# Phase 11: Analytics (YouTube Analytics API + Reporting API sync, dashboard, suggestions)

## Context links
- [plan.md](plan.md) · [contract §2 AC7, §3 business goals (YPP), §4 Analytics](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- [market research §3 YPP, §6 (Analytics API not researched in depth)](../reports/researcher-260924-2128-market-channels-copyright-policy.md)
- User decision 2026-09-24: AC7 = CTR **per video**, sourced from the **YouTube Reporting API** reach report (bulk daily CSV, ≈2-day lag). Acceptance evidence may use an existing public video on the connected channel. <!-- RT#3 -->
- The Analytics API metrics page lists `videoThumbnailImpressions` and `videoThumbnailImpressionsClickRate` as added 2026-01-15 ([metrics docs](https://developers.google.com/youtube/analytics/metrics)); per-video support there is checked in step 1 but the Reporting API is the committed CTR source.
- Depends on phase 10 (`youtube.Client`, channel tokens with the `yt-analytics.readonly` scope, which also covers the Reporting API).

## Overview
- Priority: P2 · Status: pending · Effort: 18h (10h + 8h Reporting API) <!-- RT#3 RT#15 -->
- This phase adds a daily incremental sync of per-video and per-channel metrics into Postgres from two sources: the Analytics API (views, watch time, retention, subscribers) and the Reporting API reach report (impressions and CTR per video). It adds an Analytics page (AC7), channel stats and a YPP widget on the Dashboard, and explainable rule-based suggestions.

## Requirements
- Metrics per video per day:
  - Analytics API: `views`, `estimatedMinutesWatched`, `averageViewDuration`, `averageViewPercentage`, `subscribersGained`
  - **Reporting API (reach report)**: thumbnail impressions and impressions click-through rate per video per day <!-- RT#3 -->
  - Retention per video: `audienceWatchRatio` and `relativeRetentionPerformance` by `elapsedVideoTimeRatio`, 100 buckets
  - Channel per day: views, watch hours, subscribers (YPP 4,000h / 12-month rolling window and 1,000 subscribers)
- **Reporting API flow** <!-- RT#3 -->:
  - On first sync for a channel (and at channel connect once this phase ships), `reportTypes.list` is called to find the reach report type (expected `channel_reach_basic_a1`; the exact id and its CTR/impressions columns are verified live in step 1 and recorded in `analytics/reporttypes.go`), then `jobs.create` for that type if no job exists. The job id is stored in `analytics_reporting_jobs`.
  - Daily: `jobs.reports.list` (with `createdAfter` = last seen) → download each new report's CSV through the `downloadUrl` (streamed, size-capped, parsed row by row) → upsert impressions and CTR into `video_metrics_daily`. Reports are idempotent by report id (`analytics_reporting_reports`).
  - Data appears ≈48h after the job is created and then daily with ≈2-day lag; any backfill Google generates is ingested the same way. The UI states "CTR data through <date>".
- Sync:
  - A River periodic job daily at 09:00 Asia/Bangkok (after the PT day closes) runs both sources, plus a manual "Sync now".
  - Incremental cursor per channel with a 3-day look-back (Analytics API) and report-id bookkeeping (Reporting API); backfill from the first publication date where the API allows.
  - Only videos published by this app are listed in the video table, **plus any video explicitly added for tracking** (used for the AC7 evidence on an existing public video). Channel totals cover all videos.
- Honest gaps: a metric or dimension combination rejected by the API is stored as `null` with a reason; the UI shows "Not available from API" (or "Pending: first reach report not yet generated") instead of 0.
- Suggestions (rules versioned in code, each with its evidence): CTR below the channel median over ≥1,000 impressions → test a new thumbnail/title; retention drop >35% in the first 30s → strengthen the hook; average view percentage below median for long videos → consider splitting; upload cadence gaps. An optional "Explain" sends only aggregated numbers to the configured LLM and shows provider and cost.
- UI: Analytics page with channel overview (views and watch-hours trend, YPP bars) and a virtualized video table (sortable by views, watch time, CTR, average view percentage); per-video detail with daily chart, retention curve and suggestions. Dashboard gets channel stats and "videos awaiting review".

## Architecture
Daily `analytics.sync` (queue `io`) calls `youtube.Client.Reports.Query` in batches per channel, date range and metric group, then `analytics.reach_sync` fetches and parses new reach reports. Both upsert into `video_metrics_daily`, `video_retention` and `channel_metrics_daily`, then `analytics_suggestions` is recomputed.
- Tables: `video_metrics_daily(tenant_id, channel_id, youtube_video_id, date, …metrics…, impressions, ctr, unavailable jsonb)` PK `(tenant_id, youtube_video_id, date)`; `video_retention`; `channel_metrics_daily`; `analytics_sync_state`; `analytics_reporting_jobs(channel_id, report_type_id, job_id, created_at)`; `analytics_reporting_reports(job_id, report_id, start_time, end_time, ingested_at)`; `analytics_tracked_videos`; `analytics_suggestions(rule, version, evidence jsonb, dismissed)`.
- Charts: uPlot (≈50KB) in the lazy analytics chunk.
- Report CSV download uses `netguard.Client()` restricted to Google hosts, streamed (no temp file), with a 50MB cap per report.

## Related files
- Create: `db/migrations/*_analytics.sql`, `db/queries/analytics.sql`, `api/internal/analytics/{sync,reach,reporttypes,rules,aggregate}.go`, `openapi/paths/analytics.yaml`, `openapi/schemas/analytics.yaml`, `web/src/features/analytics/`, `web/src/routes/_app/analytics/**`.
- Modify: `web/src/features/dashboard/` (channel widgets), `openapi/root.yaml`.

## Implementation steps
1. **Verify (1h):** live `reports.query` calls on the test channel for each metric group with `dimensions=video,day`, and the retention query; live `reportTypes.list` to confirm the reach report id and its columns. Record accepted combinations in `analytics/querysets.go` and `analytics/reporttypes.go` with doc links. **Create the reach reporting job immediately** (so the ≈48h first-report delay runs in parallel with the rest of this phase).
2. Write the schema, the Analytics API sync with cursor, look-back and backfill, and the periodic job registration.
3. Write the Reporting API sync: job ensure, report listing, streamed CSV parse, idempotent upsert, tracked-video add endpoint.
4. Write the aggregation endpoints: `GET /analytics/channels/{id}/overview`, `GET /analytics/videos?sort&cursor`, `GET /analytics/videos/{id}`, `POST /analytics/tracked-videos`.
5. Write the rules engine, suggestions endpoint and the optional LLM "Explain" step.
6. Build the Analytics UI (overview, table, detail with retention chart and CTR) and the Dashboard widgets.

## Todo checklist
- [ ] Live metric + report-type verification; reach job created on day 1
- [ ] Schema + Analytics API sync + periodic job
- [ ] Reporting API reach sync (CSV) + tracked videos
- [ ] Aggregation endpoints
- [ ] Rules + suggestions + optional LLM explain
- [ ] Analytics UI + dashboard widgets

## Performance budget checks
- A sync of 200 videos × 90 days ≤2 min (batched per metric group per channel). A reach report CSV of 100k rows parses in ≤10s with ≤50MB RSS (streaming).
- Overview endpoint ≤80ms and video table page ≤60ms from pre-aggregated tables indexed on `(tenant_id, channel_id, date)`.
- Analytics route chunk ≤90KB gzip including uPlot; charts render ≤50ms for 365 points.

## Security checklist
- [ ] Read-only scopes; tokens via `envelope`; refresh failures surface "reconnect channel"
- [ ] Tenant scoping on every analytics query; channel and tracked-video ownership checked
- [ ] Report download URLs treated as capability URLs (scrubbed from logs); only Google hosts via netguard; size-capped
- [ ] CSV parsed as data only (no formula evaluation, fields length-capped); LLM Explain sends aggregates only; output rendered as text
- [ ] Quota and API errors snooze, not hot-loop

## Reuse points
- Reuse `youtube.Client`, `pipeline` periodic and snooze, `netguard`, `VirtualTable`, `StatusChip`, `EmptyState` ("No analytics yet. Data appears 2–3 days after the first upload."), `registry.Resolve`, `format.ts`.
- Create `analytics.Rules` (versioned) and `Chart` wrappers (line, retention).

## Tests
- `scripts/tb.ps1 test`: rules table tests, look-back math, upsert idempotence, CSV parser fixtures (header drift, empty report, unknown columns ignored).
- `scripts/tb.ps1 test-integration`: both syncs against `httptest` doubles in `_test.go` (Analytics reports, Reporting jobs/reports/CSV download), including the unavailable-metric path and re-ingesting the same report id (no double count).
- Manual: live sync on the test channel and on one existing public tracked video; views and watch time cross-checked with Studio; CTR cross-checked with Studio's impressions CTR for the same dates (within rounding).

## Success criteria
- The Analytics page shows views, watch time, **CTR (from the reach report)** and a retention curve per video from real API data, with the "data through <date>" stamp; AC7 evidence may use an existing public video (AC7).
- The Dashboard shows YPP progress, and suggestions list their evidence.

## Risks + rollback
- The reach report type id or columns differ from expectations (Medium×Medium): step 1 verifies live; the parser maps columns by header name.
- First reach report takes ≈48h (High×Low): the job is created in step 1; acceptance runs after data exists; the UI shows "pending" honestly.
- Data lag mistaken for a bug (High×Low): "data through <date>" stamps for both sources.
- Rollback: new tables, steps and routes. Revert the PR, unregister the periodic job, and delete the reporting job with `jobs.delete`.

## Next steps
Phase 12 runs the acceptance runs and hardening.
