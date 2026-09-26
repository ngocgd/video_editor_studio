# Code review: phase 11 analytics (feat/analytics), verify round 2

- Branch `feat/analytics`, worktree `.claude/worktrees/lane-e-11`. The reviewed head is `d715651`, the fixer's last commit. Main `cfe5f1a` was already merged in round 1 (`b38b68c`), and main has not moved since, so no new merge was needed.
- Reviewer: lane c, independent verifier (round 2). I made no code edits. The Playwright run rewrote the phase 6 screenshots, and I restored them with `git checkout`.
- Verdict: **PASS**. No Critical, High or Medium findings remain on this branch. All checks are green. Every criterion in scope is met or pending for an external reason.

## Scope

The whole of phase 11 as implemented (see `plans/260924-2244-loomtale-studio-mvp/reports/cook-260926-analytics.md`). This round re-reviews the whole diff `main...HEAD` and checks closely the fix commits made after round 1:
- `a711c76`: the fake `reports.query` pages by `startIndex`/`maxResults`.
- `e8ebfae`: sync notes are capped at the 500-character `last_error` bound without splitting characters.
- `8f9d678`: the sync keeps a user's disconnect and never leaves the sync state `running`.

Two items are expected to stay pending:
- The dashboard "videos awaiting review" widget, because it needs phase 10 part 2.
- The criteria that need a real channel, which are external.

The board shows no new CHANGE lines from other lanes that affect this branch since round 1. While this run was going, lane a merged phase 7 into main (main is now `c05667d`, board 01:11:49). This verification therefore covers the branch on top of `cfe5f1a`. The merge step must merge `c05667d` into the branch and re-run the checks under the merge lock.

## Verification run

| Check | Result |
|---|---|
| `scripts/tb.sh gen` | Green. Afterwards, host `git diff --exit-code` and `git status` were clean, so the generated code has no drift. |
| `scripts/tb.sh lint test` | Green. golangci-lint reports 0 issues, and tenantctx and `lint-tenant-queries` pass. `go test ./... -race -count=1` passes in api and tools (44 packages ok, including analytics, analyticsapi and youtube). pytest: 108 passed, 5 skipped. |
| web: `npm run typecheck`, `lint`, `test`, `npx vite build`, `budget-check` | Green. 20 files and 112 tests pass. The analytics route chunk is 4.16 KB gzip and the video detail chunk is 2.21 KB. The shell is 165.78 KB, above the 160 KB soft target, which was already the case on main. The budget check passes. |
| Integration: compose.yml + compose.integration.yml, project `loomtale-c`, heavy lock `c-an2`, `-race` | **Green**: 82 top-level tests passed, 0 failed, 0 skipped. All six analytics tests pass: `TrackSyncAndReadBack`, `IsTenantScoped`, `DeadGrantMarksReconnectNeeded`, `SyncSkipsADisconnectedChannel`, `DisconnectDuringSyncIsKept` and `LongNotesFitTheSyncState`. The api and worker logs show no panic and no data race. The stack was torn down with `down -v`. |
| Playwright on compose.yml, `--workers=1`, fresh stack with the seeded owner | Green: 5 passed, including `analytics.spec.ts`. There are no 429 responses and no panics in the api or worker logs. The stack was torn down with `down -v`. |

## Round 1 findings: status

- **H1 (the fake `reports.query` ignored paging): fixed.** `pageOf` slices the rows by the 1-based `startIndex` and `maxResults` and returns an empty page past the end. The two main analytics tests now pass on a real stack. They cover:
  - reach ingestion, with a report listed twice counted once
  - reuse of the reporting job
  - the look-back upsert
  - retention
  - the explain enqueue
  - the 404 for another tenant

  The run uncovered no further failures.
- **M1 (a long `last_error` broke the CHECK and left the sync `running`): fixed.**
  - The joined notes pass through `truncateText(..., 500)`, which counts runes and so matches Postgres `length()`.
  - A failed `FinishAnalyticsSync` now calls `fail`, so the state leaves `running`.
  - The integration test `TestAnalyticsLongNotesFitTheSyncState` uses two 400-character multi-byte notes. It checks for a 500-character valid UTF-8 `last_error`, status `idle` and an advanced `analytics_through`.
- **M2 (a queued or running sync overrode a disconnect): fixed.**
  - `SyncChannel` reads the channel by tenant first. It returns `ErrChannelNotConnected` when the channel is missing or not `connected`, and `jobOutcome` completes that job without a retry.
  - `fail` now uses the new query `FlagChannelReconnectNeeded`, whose update carries the conditions `tenant_id = @tenant_id AND id = @id AND status = 'connected'`. A disconnect that lands during a sync is therefore kept.
  - Both paths have integration tests.
- **M3 (nothing records app-published videos): still open, across branches, and not blocking here.** Lane g owns it (board ANSWER 2026-09-27T00:17). `publish.upload` calls `UpsertTrackedVideo` with source `publication` in whichever branch merges second. Until then, the video table lists only manually tracked videos.
- **L1 (byte truncation could produce invalid UTF-8): fixed.** `unavailableReason`, `noteOf` and `fail` all use `truncateText`, and a unit test covers multi-byte text.
- **L2, L3, L4, L5: carried, still Low.**
  - L2: a permanently failing reach report blocks the reporting cursor.
  - L3: the report download inherits the 30-second client timeout.
  - L4: `fail` uses the job context, so it cannot write after the 20-minute timeout. This now also covers a finish write that fails because of that cancellation.
  - L5: `useAnalyticsExplanation` keeps polling after an error such as a 404. `refetchInterval` only checks `data.status`, which stays undefined on an error.

## New findings

### Low

- **N1. The analytics tables reference `youtube_channels (id)` alone, not `(tenant_id, id)`.** Every write takes its channel id from a lookup scoped by tenant (`GetYouTubeChannel`, `GetTrackedVideo`, or the sync args built from those rows), so there is no cross-tenant path today. A composite foreign key would enforce this in the database as defense in depth. Lane g's `20260927700000_publications.sql` adds `UNIQUE (tenant_id, id)` on `youtube_channels`, which would make this possible in a later migration. This is not required for merge.

## Areas re-checked and found sound

- **Tenant isolation.**
  - Every query in `db/queries/analytics.sql` takes `@tenant_id`, except `ListChannelsForAnalyticsSync`, the system-level daily fan-out that selects connected channels only.
  - The new `FlagChannelReconnectNeeded` is scoped by tenant.
  - The explanation lookup uses `GetStepByID` by tenant and checks the step kind.
  - The integration test `TestAnalyticsIsTenantScoped` passes.
- **Authorization.** All 10 operations in `openapi/paths/analytics.yaml` carry `x-min-role`: viewer for the 5 reads, and editor for sync, track, untrack, updating a suggestion and explain.
- **Input and SSRF.**
  - `ParseVideoID` accepts only 11-character ids from an allowlist of YouTube hosts.
  - The track handler confirms through `videos.list` that the video belongs to the connected channel.
  - A reach report download goes only to the configured reporting host or `https://*.googleapis.com`, and larger reports are rejected at the 50 MB cap.
- **LLM Explain.** The input holds only aggregates, ids and rule evidence (no titles). Output is capped at 700 tokens, and the web renders the result without `innerHTML`.
- **Jobs.** A sync job is unique per channel and allows 5 attempts, with a 20-minute timeout. A quota error snoozes the job until the Pacific reset, an auth error cancels it, and a channel that is not connected completes it.
- **Migration.** `db/migrations/20260927500000_analytics.sql` is byte-identical to the embedded copy. It applied cleanly on a real Postgres in the integration run, and its Down migration drops all eight tables in dependency order.

## Success criteria

| Criterion | Status |
|---|---|
| The Analytics page shows views, watch time, CTR from the reach report and a retention curve from real API data, with "data through" stamps (AC7) | Pending, for an external reason: it needs a real Google OAuth app, a real channel and the first reach report about 48h after the job is created. The same data path now passes end to end against the Google fake in integration. |
| Step 1: live check of the metric combinations and the reach report type and columns | Pending, for the same external reason. |
| The Dashboard shows YPP progress, and suggestions list their evidence | Met against fakes. Unit and web tests are green, and the integration data path is green. The widgets render in e2e without a channel. |
| "Videos awaiting review" on the Dashboard | Pending: it needs phase 10 part 2 (`feat/review-publish`). This was expected by the scope. |
| Analytics route chunk ≤90 KB gzip, including uPlot | Met: the route chunks are about 6 KB, and about 30 KB with the lazy uPlot chunk. |
| A 100k-row reach CSV parses in ≤10 s with ≤50 MB RSS | Parsing time is covered by a unit test. RSS was not measured, which needs a live reach report of that size. |
| Overview ≤80 ms, table ≤60 ms, sync of 200 videos in ≤2 min | Pending: these need a real channel with real data volume, which is external. |
| Security checklist (scopes, tenant scoping, capability URLs, CSV as data, aggregates only, snooze) | Met on review and in the integration tests. L2 remains a Low behavioural gap. |

## Required before merge

Nothing on this branch. The merge step must merge main `c05667d` (phase 7 from lane a) into the branch. It must then regenerate the generated code rather than hand-merge it, and keep the union of `TENANT_TABLES` in `scripts/lint-tenant-queries.sh`.

## Unresolved questions

- Should a permanently failing reach report be skipped and recorded (L2), or keep blocking so that an operator notices?
- Does granular OAuth consent that leaves out `yt-analytics.readonly` classify as `KindAuth`, which would flag the whole channel `reconnect_needed` and so also block uploads? This still cannot be checked without a live Google app.
- Should a later migration add composite `(tenant_id, channel_id)` foreign keys once `youtube_channels_tenant_id_id_key` from `feat/review-publish` is on main (N1)?
