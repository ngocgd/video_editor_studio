# Code review: phase 11 analytics (feat/analytics), verify round 1

- Branch `feat/analytics`, worktree `.claude/worktrees/lane-e-11`. The reviewed head is `3e2aa07`, which is the implementer's last commit. Before verifying, main `cfe5f1a` was merged in as `b38b68c` (local only, not pushed). That merge brings in the `feat/youtube-channels-upload` fix commits 32ee61d and 9a37ce1.
- There were two kinds of merge conflict. The first was the generated code (`api/internal/db/gen/{models,querier}.go`, `api/internal/httpapi/gen/server.gen.go`), which I took from main and regenerated with `scripts/tb.sh gen`. The second was the `TENANT_TABLES` line in `scripts/lint-tenant-queries.sh`, which now holds the union of main's list and the eight analytics tables.
- Reviewer: lane c, independent verifier. I made no code edits apart from the merge.
- Verdict: **FAIL**. There is one High finding: the integration suite fails in the two main analytics tests. No Critical findings.

## Scope

The whole of phase 11 as implemented (see `plans/260924-2244-loomtale-studio-mvp/reports/cook-260926-analytics.md`). Two items are expected to stay pending:
- The dashboard "videos awaiting review" widget, because it needs phase 10 part 2.
- The criteria that need a real channel, a real Google OAuth app and a real reach report, all of which are external.

## Verification run (after merging main)

| Check | Result |
|---|---|
| `scripts/tb.sh gen` (run twice, once after the merge commit) | Green. The second run left `git status` clean, so the generated code has no drift. |
| `scripts/tb.sh lint test` | Green. golangci-lint reports 0 issues; tenantctx and lint-tenant-queries pass. `go test ./... -race -count=1` passes in api and tools (analytics, analyticsapi and youtube all ok). pytest: 108 passed, 5 skipped. |
| web: `npm run typecheck`, `lint`, `test`, `npx vite build`, `budget-check` | Green. 20 files and 112 tests pass. The analytics route chunk is about 4.2 KB gzip, plus `use-analytics` at 2.1 KB and the lazy uPlot chunk at 23.9 KB, about 30 KB in all against the 90 KB budget. The shell is 165.8 KB, above the 160 KB soft target, as it already was on main. |
| Integration: compose.yml + compose.integration.yml, project `loomtale-c`, run under the heavy lock | **FAIL**: 83 passed, 2 failed, 0 skipped. `TestAnalyticsTrackSyncAndReadBack` and `TestAnalyticsIsTenantScoped` both stop at the sync step (see H1). `TestAnalyticsDeadGrantMarksReconnectNeeded` passes. The migration `20260927500000_analytics.sql` applied cleanly to a real Postgres. The stack was torn down with `down -v`. |
| Playwright on compose.yml, default limits, `--workers=1`, fresh stack with the seeded owner | Green: 5 passed, including `analytics.spec.ts`. The api and worker logs show no 429 and no panic. The stack was torn down with `down -v`. The e2e run rewrote the phase 6 screenshots, and I restored them. |

## Findings

### High

**H1. The integration suite fails. The fake `reports.query` ignores `maxResults`/`startIndex`, so the two main analytics tests never get past the sync.**
- Location: `api/internal/integration/analytics_test.go:121` (`fakeGoogleAnalytics.reports`), used by the tests at lines 329 and 420.
- Cause: `AnalyticsClient.QueryAll` (`api/internal/youtube/analytics_api.go:222`) pages with `maxResults=200` and a 1-based `startIndex`, and stops at the first short page. The client is correct for the real API. The fake, however, always returns every day in `startDate..endDate`, and the channel window is 365 days. Every page therefore has 365 rows, so the client asks for the next page 200 times and then fails with `youtube: reports.query exceeded 200 pages`.
- Impact: the suite is red. Everything these tests check after the sync step has never run against a real stack:
  - reach ingestion and not counting a report listed twice
  - reuse of the reporting job
  - the look-back upsert without duplicate days
  - video detail with retention
  - the explain enqueue with ids and numbers only
  - the 404 for another tenant on every endpoint
- Fix: make the fake honour `startIndex` and `maxResults`, by slicing `rows[startIndex-1 : startIndex-1+maxResults]`. Then re-run the integration suite to find any failures this one hides.

### Medium

**M1. A sync that finishes with two long notes breaks the `last_error` CHECK, and the sync state then stays `running`.**
- Location: `api/internal/analytics/sync.go` (`SyncChannel` and `run`).
- Cause: `run` can add two notes, `"channel statistics: "+noteOf(err)` and `"reach report: "+noteOf(err)`, and each note is capped at 300 bytes. `FinishAnalyticsSync` stores `strings.Join(notes, "; ")`, which can reach about 636 characters. `analytics_sync_state.last_error` has `CHECK (length(last_error) <= 500)`.
- Impact: the update fails after the metrics rows are written. `SyncChannel` returns the error without calling `fail`, so the row stays `status='running'`. The River retry then gets `ErrSyncRunning` and completes the job as a success. `analytics_through` and `reach_through` do not advance, and the UI shows "running" until the one-hour stale window passes.
- A realistic trigger: Data API quota exhaustion for the channel counter together with a reach failure, when both Google error messages are long.
- Fix: cap the joined text (rune-safe, at most 500), and mark the sync failed or idle whenever the finish write fails.

**M2. A queued or running sync turns a channel the user disconnected into `reconnect_needed`.**
- Location: `api/internal/analytics/sync.go` (`fail`), `db/queries/channels.sql` (`SetYouTubeChannelStatus`).
- Cause: `DisconnectYouTubeChannel` deletes the refresh token, revokes it at Google and sets `status='disconnected'`. A sync job that is already queued or retrying (the job is unique per channel for 5 attempts), or a sync that is running, then fails with `KindAuth`: either `errNoRefreshToken` or the revoked grant. `fail` then calls `SetYouTubeChannelStatus('reconnect_needed')` without any status condition.
- Impact: the user's disconnect is overwritten, and the channel shows "reconnect needed". Neither `SyncChannel` nor the worker checks that the channel is still `connected` before it starts.
- Fix: check `status='connected'` at the start of `SyncChannel` (skip otherwise), and only move a channel to `reconnect_needed` when it is `connected`, using a conditional update.

**M3. No code records app-published videos for analytics yet (this spans two branches).**
- The analytics side lists app-published videos only from `analytics_tracked_videos` rows with `source='publication'`, and `UpsertTrackedVideo` is ready for them. Nothing writes those rows on this branch or on `feat/review-publish`.
- Lane g answered on the board (2026-09-27T00:17): lane g owns this. Once both branches are on main, `publish.upload` will call `UpsertTrackedVideo` right after `SetPublicationVideo`, in whichever branch merges second.
- This is not blocking for this branch, because phase 10 part 2 is out of scope here. Until then, the video table shows only manually tracked videos.

### Low

- **L1.** `msg[:maxReasonLen]` cuts bytes, not runes. This happens in `sync.go` `fail`/`noteOf` and in `daily_rows.go`. A cut in the middle of a multi-byte character makes the `text` parameter invalid UTF-8, and Postgres rejects it. The jsonb paths are safe, because `encoding/json` replaces invalid bytes.
- **L2.** A reach report that fails permanently (over the 50 MB cap, or with a header missing a required column) is never skipped. `AdvanceReportingJobCursor` runs only after a report is ingested, so every later report stays blocked and the sync records the same note every day. Consider recording permanently failed reports and moving the cursor past them.
- **L3.** The report download inherits the 30-second whole-request timeout of the netguard client, because `oauthgoogle.Client` copies `base.Timeout`. The streamed CSV parse runs inside that 30 seconds. This is fine for normal report sizes but could fail near the 50 MB cap. The cook report already raises this as an open question.
- **L4.** `fail` uses the job context. Once the 20-minute job timeout cancels that context, `FailAnalyticsSync` cannot write, and the state stays `running` until the one-hour stale window passes.
- **L5.** `useAnalyticsExplanation` (`web/src/features/analytics/use-analytics.ts:150`) keeps polling every `EXPLANATION_POLL_MS` when the query errors, for example on a 404, because `data` stays undefined and so never looks terminal.

## Areas reviewed and found sound

- **Tenant isolation.** Every query in `db/queries/analytics.sql` filters on `tenant_id`. The only exception is `ListChannelsForAnalyticsSync`, the system-level daily fan-out. Handlers check the channel (`GetYouTubeChannel` by tenant) or the tracked video (`GetTrackedVideo` by tenant) before reading. The explanation lookup is tenant-scoped and checks the step kind. `UpsertTrackedVideo` refuses to move a video to another channel. The lint covers all eight tables.
- **Authorization.** Every analytics operation carries `x-min-role`: viewer for reads, editor for sync, track, untrack, dismiss and explain. There are no public operations.
- **SSRF and secrets.**
  - Report downloads go only to the configured reporting host or `https://*.googleapis.com`.
  - The netguard transport allowlists oauth2, www, youtubeanalytics and youtubereporting.googleapis.com. `oauthgoogle.Client` follows redirects, but the transport enforces the allowlist, so bearer tokens only reach Google hosts.
  - URLs are scrubbed from transport and body errors.
  - Downloads are capped at 50 MB, and fields are capped at 256 bytes.
  - The CSV is parsed as data only.
- **LLM Explain.** The input holds only numbers, ids, dates and rule evidence (no titles). Output tokens are capped at 700, and the web renders the text as plain text (no `innerHTML`).
- **Concurrency.**
  - `StartAnalyticsSync` guards against overlapping syncs of one channel, with a one-hour stale takeover.
  - Retention is replaced inside one transaction.
  - A reach report is claimed and its rows upserted in the same transaction, so a report listed twice is counted once.
  - Suggestion upserts keep the dismissal unless the rule version changes.
- **Jobs and wiring.**
  - Quota errors snooze until the Pacific reset plus 5 minutes, and auth errors cancel the job.
  - The queue and the 09:00 periodic job are registered only when `GOOGLE_CLIENT_ID` is set.
  - The migration version 20260927500000 sorts above main's newest (20260927400000), so goose accepts it on existing databases.

## Success criteria

| Criterion | Status |
|---|---|
| The Analytics page shows views, watch time, CTR from the reach report and a retention curve from real API data, with "data through" stamps (AC7) | Pending, for an external reason: it needs a real Google OAuth app, a real channel and the first reach report about 48h after the job is created. The coverage against fakes is blocked by H1. |
| Step 1: live check of the metric combinations and the reach report type and columns, and creating the reach job on day 1 | Pending, for the same external reason. |
| The Dashboard shows YPP progress, and suggestions list their evidence | Built. Unit and web tests are green, and the widgets render in e2e without a channel. The integration check of the data path is blocked by H1. |
| "Videos awaiting review" on the Dashboard | Pending: it needs phase 10 part 2 (`feat/review-publish`), which is not on main. This was expected by the scope. |
| Analytics route chunk ≤90 KB gzip, including uPlot | Met: about 30 KB. |
| A 100k-row reach CSV parses in ≤10 s with ≤50 MB RSS | A unit test covers the 100k rows. RSS was not measured. |
| Overview ≤80 ms, table ≤60 ms, sync of 200 videos in ≤2 min | Pending: these need real data and the live stack. |
| Security checklist (scopes, tenant scoping, capability URLs, CSV as data, aggregates only, snooze) | Met on review. The remaining behavioural gaps are M2 and L2. |

## Required before merge

1. Fix H1 by making the fake honour paging, then re-run the integration suite and fix whatever that uncovers.
2. Fix M1 and M2, which are small and local to `sync.go` and one query condition, or record an explicit decision to defer them.

## Unresolved questions

- Should `UntrackAnalyticsVideo` also delete the video's metrics rows? The lead's default, recorded on the board at 23:11, is to keep them, and the code does keep them.
- Should a permanently failing reach report be skipped (L2), or keep blocking so that an operator notices?
- Does granular OAuth consent that leaves out `yt-analytics.readonly` classify as `KindAuth`, which would flag the whole channel `reconnect_needed` and so also block uploads? This could not be checked without a live Google app.
