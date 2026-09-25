# Phase 03: job orchestration (River), GPU slot, SSE progress

Branch `feat/job-orchestration-sse`, based on `main`@215cfe0. Final SHA `6cd57f0` (2 commits: `e1c21d2` feature, `6cd57f0` <200-line split of dispatcher.go/enqueue.go). Not pushed/merged.

## Built

- **Schema**: `db/migrations/20260925020000_pipeline.sql` — `pipeline_runs`, `pipeline_steps`, `pipeline_step_deps`, `tenant_quotas.max_active_steps` (reused phase 2's table, added the column instead of redeclaring it). `db/queries/pipeline.sql` — CAS claim, conditional heartbeat/commit/requeue, fan-in decrement, ready-sweep, stale-heartbeat sweep, cancel/retry, quota lookup, plus `:batchone`/`:batchexec` step/dep inserts for the enqueue latency budget.
- **`api/internal/pipeline`** (engine): `types.go`/`job.go` (StepHandler, StepRef, ModelRef, StepJobArgs — args are step IDs only), `transition.go` (pure state table, tested), `hash.go` (order-independent content hash), `classify.go` (transient/gpu_oom/permanent), `registry.go`, `chunk.go` (batch chunking to a ~10-min target via a pluggable per-kind estimator), `claim.go` (the CAS + conditional heartbeat), `enqueue.go`/`enqueue_jobs.go` (Enqueue, admission checks, River job insert grouped by queue+kind with `UniqueOpts` dedupe), `fanin.go` (lost-wakeup-free dependency resolution + `MarkStaleDependents`), `dispatcher.go`/`commit.go` (claim→run→commit, detached-context bookkeeping so a cancelled ctx never strands a step in `running`), `gpu_executor.go` (advisory lock try/watchdog, resident-model preference bounded to 3 snoozes), `worker.go` (River `Worker`, per-kind `Timeout` via job Metadata), `reconciler.go` (boot + 60s: stale-heartbeat reset, ready sweep, both re-enqueue), `admission.go`/`cancel.go`/`query.go`, `log.go`/`log_sink.go`/`step_context.go` (scrubbed ring-buffer log flushed to an asset on terminal state), `notify.go` (`pg_notify` with only IDs/numbers/versions).
- **`api/internal/quota`**: `Checker.Check` — `pipeline.AdmissionCheck`, missing row or NULL `max_active_steps` = unlimited (the local default).
- **`api/internal/sse`**: `Hub` (one LISTEN connection, reconnect + `resync` to every subscriber on loss, per-user cap of 6), `Subscriber` (`ready` first, 250ms per-key progress coalescing, transitions queued not dropped, overflow forces `resync`), `event.go`.
- **`api/internal/pipelineapi`**: `runs.go`/`steps.go`/`gpu.go`/`events.go` implementing `createRun`/`getRun`/`listRunSteps`/`cancelRun`/`retryStep`/`cancelStep`/`listJobs`/`getStepLog`/`getGpuStatus`/`streamEvents` on the generated strict server; retry/cancel are audited. `streamEvents` is a normal strict-server operation (RBAC/validation apply) whose response is a hand-written `io.Pipe`-backed SSE body, not the JSON model oapi-codegen otherwise generates.
- **OpenAPI**: `openapi/schemas/gpu.yaml` (full `GpuStatus`: running, queue, resident, vram, backends, encoder, capabilities), `schemas/jobs.yaml`, `paths/{jobs,gpu,events}.yaml`, wired into `root.yaml`.
- **`cmd/worker`**: real River client (`cpu`/`llm`/`render`/`io` queues always, `gpu` only when `WORKER_GPU=true`), `StepWorker`, `GPUExecutor` with `NoopResidency` default, `Reconciler` goroutine, graceful soft-stop→`StopAndCancel` shutdown. `cmd/api`: insert-only River client + `sse.Hub.Run` goroutine. `cmd/loomtale migrate`: now also runs River's own schema migration (`rivermigrate`) against the **owner** DSN, right after goose — moved out of `cmd/api`/`cmd/worker` startup because those only ever hold the least-privilege `loomtale_app` role, which cannot `CREATE TABLE` (this was the first real bug the toolbox found).

## Deviations from the spec text

1. **Per-kind timeout override carrier**: spec's architecture section implies River `Tags`; River's `Tags` field is regex-restricted to `\w[\w-]+\w` (no `.`, no `=`), which rejects real kind names like `train.lora`. Switched to River's `InsertOpts.Metadata` (free-form JSON `{"kind": "..."}"`), read back in `StepWorker.Timeout` via `job.Metadata` — no DB round trip either way, same net effect.
2. **`UniqueOpts.ByState`**: River requires `pending` in the state list "unless overridden with an explicit fallback"; added it to `readyUniqueStates` (found by the toolbox, not reasoned in advance).
3. **`deploy/compose.yml` `worker` service**: added `MINIO_ENDPOINT`/`MINIO_BUCKET`/`MINIO_APP_ACCESS_KEY`/`MINIO_APP_SECRET_KEY` env vars, because this phase's worker is the first thing that actually makes the worker service do real MinIO I/O (step-log flush via `AssetLogSink`). Plan.md assigns "both compose files" to phase 4 during the parallel group; the task brief's own exclusion list only named `compose.gpu.yml`, not `compose.yml`, and phases run sequentially here, not in parallel, so I made this minimal, additive change (env vars on an existing service block only) rather than leave the worker permanently crash-looping. Flagging for the controller to confirm against phase 4's plan when that phase starts.
4. **GPU status `resident`/`vram`/`backends`/`encoder`**: populated only when phase 4 wires a real `GpuProbe`/`ModelResidency` into `pipelineapi.PipelineAPI` (both nil today); the endpoint omits those fields rather than fabricating data, per "no mock data served to users."
5. **`GET /events` implementation**: the spec's "Create ... paths/events.yaml" is satisfied, and the route goes through the generated strict server (RBAC `x-min-role: viewer`, request validation) like every other route — I initially planned to mount it outside codegen and hand-roll auth, then found oapi-codegen already special-cases `text/event-stream` responses with a flush-per-chunk `io.Copy` loop, so `StreamEvents` is a normal strict handler returning a custom `io.Pipe`-backed response object. Cleaner and more consistent than the bypass I originally wrote (which I deleted).

## Toolbox environment note (not a code defect)

Every `.sh` file in this Windows checkout has CRLF line endings in the working tree (git's `core.autocrlf`; the committed blobs are LF). Any script bind-mounted into a Linux container and executed by its own shebang (`deploy/minio-init.sh`, `deploy/postgres/init-roles.sh`, `scripts/lint-tenant-queries.sh` when run via `make lint` inside the toolbox, etc.) fails with `set: pipefail: invalid option name`. Worked around locally by stripping `\r` from working-tree copies only (never staged/committed) for every affected script, then `git checkout --` on the ones I didn't intend to change. This is environment-specific to a Windows checkout of this repo and would not reproduce on the Ubuntu CI runners (which is presumably why it was never caught before); worth a `.gitattributes` line (`*.sh text eol=lf`) at some point, but that's outside this phase's ownership and I did not add it.

## Commands run + results

All via `scripts/tb.sh` / `docker compose -f deploy/compose.tools.yml` (toolbox) unless noted; live-stack tests via `PROJECT=loomtale-p3 scripts/test-integration-toolbox.sh` against `docker compose -p loomtale-p3 -f deploy/compose.yml -f deploy/compose.integration.yml up -d --wait --build`, torn down with `down -v` after.

| Command | Result |
|---|---|
| `go build ./...` | pass |
| `go vet ./...` | pass |
| `golangci-lint run ./...` (and `--build-tags=integration ./internal/integration/...`) | 0 issues (after fixing 2 staticcheck findings during development) |
| `go test ./... -race -count=1` (unit) | pass, all packages |
| `bash scripts/lint-tenant-queries.sh` | OK (added `pipeline_runs pipeline_steps pipeline_step_deps` to `TENANT_TABLES`) |
| `./bin/tenantctx ./...` | pass |
| `workers-python`: ruff, pytest | pass (untouched by this phase) |
| `web`: eslint, `npm run gen` | pass; TS client regenerated for the new endpoints |
| `go tool govulncheck ./...` | 0 reachable vulnerabilities |
| `npm audit --audit-level=high` (web) | 0 vulnerabilities |
| `make gen` + host-side `git diff --exit-code` | clean (only the intended new/changed generated files; `make gen-check`'s own git call fails inside the toolbox in a worktree, as flagged in the task brief — verified the equivalent by hand) |
| `docker compose -f deploy/compose.yml build` | all 4 images build |
| non-root user check | `api`/`worker` → `nonroot:nonroot`, `web` → `caddyapp` |
| `mem_limit` on every service | 8/8 |
| no `docker.sock` mount | confirmed |
| `go test ./internal/integration/... -tags=integration -race` (full suite: phase 1/2's existing tests + this phase's 15 new tests) | **pass**, 16.6s, twice in a row |

### New integration tests (all in `api/internal/integration/pipeline_*.go`, `//go:build integration`, run by the existing CI `integration` job unchanged)

| Test | Verifies |
|---|---|
| `TestFanInConcurrentDepCompletionEnqueuesDependentExactlyOnce` | 40 leaves completed concurrently fan into one join step exactly once (checked both DB state and live River job count) |
| `TestBatchPartialFailureMarksOnlyTheFailedStep` | one permanent failure inside a 3-step batch job doesn't fail the job or the other two steps |
| `TestReconcilerResumesAfterCrash` | stale heartbeat → reconciler requeues → next claim gets attempt 2 → a forced duplicate claim (rescue-style) claims nothing |
| `TestZombieWriterFailsHeartbeatAndCommitsNothing` | a step reclaimed out from under its original claim fails heartbeat and can never commit under the stale attempt |
| `TestReconcilerSweepsReadyPendingStepWithNoLiveJob` | a fan-in gap (remaining_deps hits 0 with no job) is swept and re-enqueued |
| `TestGpuAdvisoryLockSerializesTwoExecutors` | two `GPUExecutor`s against the same DB never run handlers concurrently (real `pg_try_advisory_lock`, not a mock) |
| `TestGpuLockConnectionLossCancelsRunningJob` | killing the lock connection's backend cancels the running job within one watchdog cycle |
| `TestQueueResolvedAtEnqueueTimeIsPinnedOnTheStepRow` | queue is resolved and pinned at enqueue time |
| `TestPriorityIsStoredOnTheRiverJobRow` | interactive (1) vs batch (3) priority lands on the River job row |
| `TestInteractiveStepWaitsAtMostOneChunkBehindBatch` | real `river.Client` (Start/Stop), single cpu worker: interactive step starts ~1.8s after a 2s batch chunk, not starved |
| `TestSSESubscribeReadyThenTransitionsDeliveredAndTenantIsolated` | real Postgres LISTEN/NOTIFY end to end: ready → done transition delivered; a same-run-id subscription from a different tenant receives nothing |
| `TestSSEResyncOnListenLoss` | killing the hub's LISTEN backend yields `resync` after reconnect |
| `TestQuotaCheckEnforcesConfiguredLimit`, `TestEnqueueRejectsOverQuotaRunWithNoRowsWritten` | quota admission check and its 429 path write zero rows |
| `TestEnqueue300StepsWithDepsMeetsLatencyBudget` | perf budget (below) |
| `TestProgressWritesAreThrottledToOncePerSecond` | ≤1 accepted progress write/s/step (version bump ceiling) |

A real, non-obvious bug the toolbox caught mid-development: my first fan-in/GPU-lock test runs raced against the **live `worker` compose service** (which, in phase 3, has zero registered handlers) — it claimed and permanently failed test steps before my test's own manual `Dispatch()` call got to them. This is real production behavior (an unregistered kind should fail fast), just incompatible with directly driving `pipeline.Engine` in a test while a live poller shares the same queues. I stop the `worker` container before running `internal/integration`'s pipeline-level tests locally (documented in `pipeline_helper_test.go`'s package doc); the one test that needs a real `river.Client` (`TestInteractiveStepWaitsAtMostOneChunkBehindBatch`) clears any leftover `cpu`-queue jobs from earlier tests in the same run before starting its own. This is worth the controller's attention before merge: **the CI `integration` job brings up the full stack including `worker`**, and until a later phase registers real handlers, `worker`'s empty registry will race and fail any pipeline-level step a future phase's tests enqueue through the live stack the same way. I did not change `.github/workflows/ci.yml` for this (out of scope for a controller-reviewed change I wasn't asked to make); flagging it explicitly here instead.

## Performance budgets (measured, not just reasoned about)

- Enqueue 300 scene steps + 150 dep rows, one tx: **89–95ms** (with a realistic 30s/step estimator so batch chunking behaves like a real per-scene stage, not the package's deliberately-conservative 1-chunk-per-step default for an unknown kind) — comfortably under the 150ms budget, including toolbox→postgres docker network overhead, under `-race`.
- SSE hub fan-out, 200 subscribers, `go test ./internal/sse -bench .`: **~88µs/broadcast** ⇒ >11,000 broadcasts/s, well above the 500 events/s target (`BenchmarkHubBroadcast`).
- Progress DB writes: asserted via version-bump ceiling (claim + ≤1 throttled write + commit ≤ 3 bumps against 50 rapid `Progress()` calls) — real DB counter, not a mock.
- Interactive-vs-batch wait: measured **~1.8s** behind a 2s (2-step) batch chunk on a single-worker cpu queue, via a real `river.Client`.
- `GET /jobs` p95 at 50k seeded rows: **not measured**. `ListJobs`'s query is covered by the `(tenant_id, queue, status, id)` index (added in the migration specifically for this), but I did not seed 50k rows and run `EXPLAIN ANALYZE` under load — deprioritized given the time budget in favor of the correctness-critical CAS/fan-in/GPU-lock/SSE tests above. Flagging as an open item for phase 8 (which starts seeding meaningful step volume) or a dedicated follow-up.

## Security checklist

- [x] SSE topics authorised against tenant ownership (`RunBelongsToTenant`); unknown/foreign topics → 403; enforced by `TestSSESubscribeReadyThenTransitionsDeliveredAndTenantIsolated`'s tenant-isolation half plus `sub.allows()`'s own tenant check as defence in depth.
- [x] NOTIFY payload (`stepEvent`) carries only IDs, a status string, numbers and a version — no prompts, no user text.
- [x] Step logs scrubbed (`obs/scrub.URL`) before ever entering the in-memory ring buffer, not just before flush; served via a short-lived (10 min) presigned GET, `x-min-role: editor` (editor+owner).
- [x] Retry/cancel require `editor` role (RBAC via `x-min-role`) and are audited (`audit.Record`, best-effort, never blocks the action).
- [x] River job args are `{StepIDs []uuid}` only — no prompts, no tokens, no attempt number (attempt lives in the DB CAS).
- [x] `pipeline_step_deps` carries `tenant_id`; every pipeline query is tenant-scoped except the ones the fence itself is (claim/heartbeat/commit by id+attempt, or system-wide reconciler sweeps), each marked with a `lint-tenant-queries:allow` comment and passing the updated lint script.

## Unresolved questions

1. `deploy/compose.yml` `worker` env vars (deviation #3): please confirm this doesn't collide with what phase 4 planned to add there for GPU wiring — I only added the four MinIO vars the worker's own `storage.Config` needs.
2. `GET /jobs` at 50k-row scale is untested; worth a follow-up before/при phase 8 seeds real volume.
3. The live-`worker`-races-empty-registry issue above: should the CI `integration` job stop/scale the `worker` service to 0 until a phase registers real handlers, or is racing-and-failing-fast acceptable/expected for any future phase's own pipeline-level tests to defend against the same way I did?
4. `deploy/minio-init.sh`, `deploy/postgres/init-roles.sh`, and every `scripts/*.sh` have CRLF endings on a fresh Windows checkout (autocrlf), which breaks them when bind-mounted and exec'd inside a Linux container. Not touched (out of scope), but a `.gitattributes` `*.sh text eol=lf` would prevent the next Windows-checkout agent from hitting the same `make ci` failure I worked around locally.

Status: DONE
Summary: Phase 3's job orchestration engine (River-backed, DB-CAS-fenced, crash-resumable), GPU advisory-lock slot, and versioned SSE hub are implemented, wired into cmd/api and a now-real cmd/worker, and verified with 15 new integration tests plus the full phase 1/2 suite against a live stack, all green under `-race`; `make ci`'s Go/lint/vuln/audit/docker checks pass, with `gen-check` verified manually per the task's own worktree caveat.
Concerns/Blockers: the CI integration job's live `worker` service currently has an empty handler registry and will race any future phase's own pipeline-level enqueue tests the same way it raced mine (see unresolved question 3); the `compose.yml` worker env-var addition should be confirmed against phase 4's plan; `GET /jobs` p95 at 50k rows is unverified.

## Review fixes (code-reviewer-260925-1508-phase-03-orchestration-review.md)

Same branch/worktree, review's decisions 1-9 applied as instructed. Full `-race` integration suite run twice against the live (never-stopped) `worker` container: **52/52 pass both times, 0 flakes, 0 data races.**

### Critical

**C1 — reconciler re-enqueue deduped against a dead `running` job.** Removed `river.UniqueOpts` from `enqueueReadySteps` (`api/internal/pipeline/enqueue_jobs.go`) entirely; the DB CAS on `pipeline_steps` (id, attempt, status) is the only fence, so a duplicate `available` row next to a stuck `running` one is harmless — `Claim` only ever takes the one whose `attempt`/`status` still match. New test `TestReconcilerRecoversWhileTheDeadRiverJobIsStillRunning` (`pipeline_reconciler_test.go`) forces `river_job.state='running'` (the exact SIGKILL shape) before `RunOnce`, then asserts a fresh `available` job exists and is claimable to attempt 2 — passes.

### High

**H1 — steps stranded in `queued` forever.** Two parts. (a) `DispatchOpts.RiverAttempt`/`RiverMaxAttempts` now flow from the River job into `Dispatch`/`commit.go`; the last transient attempt commits a permanent failure instead of leaving the step `queued` for a River retry that will never come. (b) New `api/internal/pipeline/reconciler_orphans.go`: `sweepOrphanedQueued` finds `queued` steps with no live `river_job` row (hand-written `EXISTS` check, `river_job` isn't sqlc-visible), re-enqueues up to `maxStrandedRequeues=5` times, then fails outright with `error_code=stranded_no_job` (releasing quota). Verified live: this sweep is what recovered the 40k synthetic orphaned rows the scale test left behind during verification (see "New defect found" below) — real, working code, not just a unit test.

**H2 — chunk dispatch claimed all ids up front, heartbeating only one.** `dispatcher.go`'s `Dispatch` now claims one id at a time, right before working it (picked lazy-claim over the alternative, per the decision). New `TestDispatchClaimsChunkStepsLazilyOneAtATime` (`pipeline_chunk_test.go`, chunk size 3) asserts only the step currently being worked is ever `running` at once.

**H3 — a worker with no handler destroys the step.** `dispatcher.go` now calls `PeekSteps` before `Claim`; a step whose kind has no registered handler is left untouched and `river.JobSnooze(30s)` returned (no attempt consumed) instead of being claimed and permanently failed. `cmd/worker/main.go` only enables a River queue when `registry.Len() > 0` maps to it, so an empty registry (today's `cmd/api`/`cmd/worker` state) subscribes to zero queues and structurally cannot race a test's own `Dispatch()` calls — chosen over a CI compose-profile/stopped-worker change since it fixes the underlying design bug everywhere, not just in CI. New `TestDispatchSnoozesInsteadOfDestroyingAnUnregisteredKind`. Verified empirically: the full integration suite ran twice with `-race` against the live, never-stopped `worker` container (52/52 pass both times) — the exact scenario H3 reproduced against before the fix.

**H4 — `RetryStep` revives steps with unmet deps or non-active runs.** `RetryStep` (`db/queries/pipeline.sql`) now joins `pipeline_runs` and only updates when `r.status='active'`; retried steps go to `pending` (deps unmet) or `queued` (`remaining_deps<=0`), which is now recomputed at retry via a tenant-scoped `GetDependencies` lookup. `cancel.go` maps the "nothing updated" case to `ErrRetryNotAllowed` → 409 (`explainRetryRejection` distinguishes "run not active" from "deps unmet" for the detail message). New `TestRetryStepRejectsInactiveRun`, `TestRetryStepRejectsUnmetDependency` (`pipeline_lifecycle_test.go`).

**H5 — client-controlled graph/priority.** `pipelineapi/graph.go`: `resolveStepGraph` maps client `clientRef` strings to server-generated `uuid.NewV7()` ids (client can no longer choose/collide a primary key) and `priorityForClass` derives priority server-side from the OpenAPI `priorityClass` enum (`interactive|scene|batch|train_bench`) — the request body no longer carries a numeric priority at all (`openapi/schemas/jobs.yaml`). New `pipeline/graph.go`'s `validateGraph` (DFS) rejects self-deps, cross-request deps, and cycles before any row is written. `CreateRun` (`pipelineapi/runs.go`) only special-cases `ErrQuotaExceeded`(429)/`ErrAdmissionDenied`(507)/`ErrInvalidGraph`+`ErrUnknownStepKind`(400); every other error (DB constraint violation, outage) falls through to the strict server's generic 500 — no raw DB text, no cross-tenant existence oracle. `steps.maxItems`/`dependsOn.maxItems` = 500 in the schema. Unit tests: `pipeline/graph_test.go` (7 cases), `pipelineapi/graph_test.go` (5 cases).

### Medium

**M1 — GPU lock release races, no ping timeout, unlock error discarded.** `gpu_executor.go`: the deferred cleanup now waits for the watchdog goroutine to exit (`<-watchdogDone`) before touching the connection; `Ping` bounded by `gpuLockPingTimeout=2s`; an unlock failure now `conn.Hijack()`s and closes the raw connection instead of returning it to the pool (a session-scoped advisory lock must never silently ride back into the pool). `TestGpuLockConnectionLossCancelsRunningJob` (existing test, still passing) exercises the kill-connection path this fix changes the cleanup of.

**M2 — gpu_oom handling wrong three ways.** `dispatcher.go` now stops the rest of a chunk after an OOM (`errStopChunk`) instead of continuing on an unloaded model. `commit.go`'s `handleGPUOOM` counts OOMs on the step itself (`gpu_oom_count`, new column) instead of overloading River's attempt counter, so a transient failure followed by a first OOM is no longer misread as permanent. `enqueue_jobs.go` groups chunks by `(queue, kind, providerRef)` instead of `(queue, kind)`, so a chunk's model is never ambiguous.

**M3 — commit failure strands the step in `running` and re-executes it.** `commit.go`: `commitDoneWithRetry` retries the commit up to `commitRetries=3` times (`commitRetryDelay=200ms`) before giving up; the step is never silently left for the reconciler to reset and redo successful work as a side effect.

**M4 — `GET /jobs` never uses an index.** New migration `db/migrations/20260925030000_pipeline_review_fixes.sql` adds `pipeline_steps_tenant_id_idx`, `pipeline_steps_tenant_status_id_idx`, `pipeline_steps_tenant_queue_id_idx`. `db/queries/pipeline.sql`'s single OR-based `ListJobs` split into four sqlc queries (`ListJobs`/`ListJobsByStatus`/`ListJobsByQueue`/`ListJobsByStatusAndQueue`), since a generic OR-form plan can't use any of these indexes. Verified, not just reasoned about: new `TestListJobsUsesIndexesAndMeetsP95Budget` (`pipeline_jobs_scale_test.go`) seeds 120k rows (60k × 2 tenants) via `pgx.CopyFrom`, `EXPLAIN`s all four filter combinations (no `Seq Scan`, all use an `Index`), and measures p95: **210.8µs** over 60 calls against the 50ms budget.

**M5 — SSE hardening gaps.** Topics capped at `maxTopicsPerStream=50`, authorized in one batch `RunsBelongToTenant` call (`events.go`) instead of one `GetRun` per topic; unauthorized/unknown topics return a fixed `errBadTopic` 403, never a raw DB error string. `subscriber.go`'s `deliver()` now deletes `progress[stepID]` before queueing any transition, so a buffered progress event can never flush after a later terminal transition; added `maxStreamLifetime=1h` so a revoked session's stream can't run forever. `cmd/api/main.go` wires `server.RegisterOnShutdown(cancelShutdownSignal)`; `PipelineAPI.ShutdownSignal` is threaded into `StreamEvents` via `context.AfterFunc`, so shutdown cancels every open subscription instead of `server.Shutdown` blocking on connections that never go idle.

**M6 — error text leaks data, logs half-scrubbed.** New `api/internal/obs/scrub/secrets.go`: `scrub.Text` combines URL scrubbing with a secret-pattern regex (bearer tokens, API keys); `log.go` and `commit.go`'s `commitFailed` both use it now, not just `scrub.URL`. `handler.go`'s `toStepDTO(s, includeErrorMsg bool)` only includes `error_msg` for editor+ callers (`isEditorOrAbove`); viewers get `error_code` only.

**M7 — quota check racy, doesn't count `pending`.** `enqueue.go`'s `Enqueue` takes `qtx.LockTenantForAdmission` (`pg_advisory_xact_lock(hashtext(tenant_id))`) inside its own transaction before running any `AdmissionCheck`, so concurrent `CreateRun` calls for the same tenant serialize instead of all passing a stale count. `quota/check.go`'s `Check` now counts `pending` steps too. Not independently re-verified with a dedicated concurrency test this pass (existing `TestQuotaCheckEnforcesConfiguredLimit`/`TestEnqueueRejectsOverQuotaRunWithNoRowsWritten` still pass); flagged as a follow-up if the controller wants a concurrent-Enqueue race test specifically.

**M8 — state machine not enforced; no run rollup.** New `api/internal/pipeline/rollup.go`: `rollupRun` marks a run `done` once every step is terminal-successful, `failed` if any step permanently failed (`cascadeCancelPendingDependents` BFS-cancels everything downstream of a failed step first). `cancel.go`'s `CancelRun` uses `MarkRunStatusIfNotTerminal` (guards `status='active'`) instead of unconditionally overwriting a `done`/`superseded` run; `SupersedeRun` uses a new `SupersedeRunTx` query that requires the new run to already exist, making cancel+link atomic (no more 23503 after a already-committed cancel). New tests: `TestRunRollsUpToDoneWhenEveryStepFinishes`, `TestPermanentFailureCascadeCancelsDependentsAndFailsTheRun` (`pipeline_lifecycle_test.go`).

**M9 — contract gaps.** `fanin.go`'s `MarkStaleDependents` now actually reopens `done` dependents back to `pending` (not just recomputing the counter) and its dependency join is tenant-scoped (`AND dep.tenant_id = @tenant_id`). New `TestMarkStaleDependentsReopensDoneStepAndReArmsIt`. `StepContext.Storage()` and GPU-queue SSE events remain out of scope for this fix pass (they depend on phase 4's real `ModelResidency`/`GpuProbe`, not wired yet) — same deferral the original report already flagged.

### Low

- **L1** — done as part of M4/H1: `ResetStaleHeartbeatsBatch`/`ReadySweepBatch`/orphan sweep all use a bounded `LIMIT reconcileBatchLimit=500` + `FOR UPDATE SKIP LOCKED` CTE, looped by the reconciler until a page returns fewer than the limit.
- **L2** — HTTP-level SSE test added (`sse_http_test.go`, `TestStreamEventsThroughCaddyDeliversIncrementallyAndRejectsOtherTenant`): real request through Caddy, asserts `Content-Type: text/event-stream` and no `Content-Encoding` (never gzip-buffered), timed gaps between the `ready` event and a later triggered transition prove incremental delivery, plus a cross-tenant rejection (403) via `/auth/switch-tenant` onto a second tenant the same user also belongs to (see the rate-limit note below for why not a second `/auth/login`). Destructive test actions scoped: `pipeline_gpu_test.go`/`pipeline_sse_test.go`'s `pg_terminate_backend` now targets only that test's own `application_name`-tagged connection (`markedPool`), not every advisory-lock/LISTEN holder. SIGKILL/two-real-process tests remain simulated via direct `Claim`/`Dispatch` calls, matching every other test in this package — not redesigned this pass. Concurrency coverage still concentrated in the fan-in test; not broadened further here.
- **L3** — `go.mod`: `go mod tidy` reclassified `river`/`riverpgxv5` as direct. `pipelineapi/handler.go`'s stale `sse.Handler` doc comment removed. `GPUExecutor.snoozes` leak: `clearSnoozeCount` already covers both "never got the lock" and "stopped deferring" exits (verified, not changed further). `cmd/loomtale/migrate.go` added `riverMigrateDown` (`rivermigrate.MigrateOpts{TargetVersion: -1}`) wired into `case "down"`. The cook report is now tracked (this file).
- **L4** — `ListJobs`/`ListRunSteps` (`steps.go`/`runs.go`) map a `DecodeCursor` failure to 400 instead of silently resetting to page 1.
- **L5** — accepted as-is, no change, per the review's own verdict ("acceptable").

### New defect found during this pass's verification (not in the original review)

Running the full integration suite with the M4 scale test present caused `TestReconcilerResumesAfterCrash` to hang for the full 10-minute `go test` timeout, reproducing deterministically. Root cause: `pipeline_jobs_scale_test.go`'s `seedSteps` bulk-inserts 120k synthetic `pipeline_steps` rows via `pgx.CopyFrom` to exercise `EXPLAIN`/p95, about a sixth of them (~40k) with `status='queued'` and no real River job behind them — exactly what H1's new orphan sweep (`sweepOrphanedQueued`) is designed to catch, and it correctly did, one row-transaction at a time, across ~80 batches of 500. The orphan sweep itself is not the bug; the scale test leaking 40k rows into every later test's shared database is. Fixed by adding `t.Cleanup` to `TestListJobsUsesIndexesAndMeetsP95Budget` that deletes the two seeded fixture tenants (cascades through `pipeline_runs → pipeline_steps` via the existing `ON DELETE CASCADE` FKs) once the test finishes. Confirmed fixed: the full suite went from a 10-minute timeout to a 25s clean pass with the fix in place, reproduced across two separate `-race` runs.

Separately, `sse_http_test.go`'s HTTP-level SSE test (added for L2/decision 9) initially used two `/auth/login` calls (one per tenant) for the cross-tenant check. `LoginPerIP` (`api/cmd/api/main.go`, capacity 20, refill 20/hour) is one bucket shared by this whole package's tests — they all reach the API from the same client IP inside the toolbox container, which is exactly why `zz_login_rate_limit_test.go` is named to run last and deliberately exhausts whatever's left. The suite's existing login volume already used the full budget (confirmed: `TestUploadFinalizeAndReuploadIsIsolatedByVersion`, the package's last real login before `zz_`, started failing with 429 once my 2 new calls were added). Rather than raise a production rate-limit constant to fit a growing test suite, the test now uses a single login plus `/auth/switch-tenant` (which doesn't touch `LoginPerIP` — it operates on an already-authenticated session) to get the same user onto a second tenant for the negative case. Confirmed fixed: two full `-race` suite runs, 52/52 pass both times.

### Verification commands + results

| Command | Result |
|---|---|
| `./scripts/tb.sh ci` (lint, unit test, gen-check, vuln, audit) | lint/test/vuln/audit all pass; `gen-check`'s own `git diff` invocation fails inside the toolbox container for the same worktree-path reason noted in the original report (`.git` in a worktree points at an absolute host path the container can't see) — verified the equivalent by hand: `make gen` regenerated files match what's staged for commit, confirmed via host-side `git diff --stat` |
| `golangci-lint run --build-tags=integration ./internal/integration/...` | 0 issues |
| Full integration suite, `-race`, live `worker` container running (never stopped) — pass 1 | 52/52 pass, 25.9s |
| Full integration suite, `-race`, live `worker` container running (never stopped) — pass 2 | 52/52 pass, 24.9s |
| Flake count across both `-race` passes | 0 |

### Commit

All review-fix changes are committed on `feat/job-orchestration-sse` (not pushed, not merged). Final SHA below.

Status: DONE
Summary: All Critical/High/Medium findings from the phase 3 review fixed and verified against a live stack; Lows fixed except L5 (accepted as-is per the review) and the SIGKILL/two-process-test and M7-concurrency-test gaps in L2 (flagged, not redesigned this pass). A real defect not in the original review (the M4 scale test leaking 40k orphaned rows into later tests, hanging `TestReconcilerResumesAfterCrash`) was found and fixed during this pass's own required verification. Full `-race` integration suite passes twice against the live, never-stopped worker service: 52/52, 0 flakes.
Concerns/Blockers: M7 has no dedicated concurrent-Enqueue regression test (existing tests still pass); L2's SIGKILL/two-process and broader-concurrency test gaps remain as documented limitations shared with the rest of this test package, not fixed this pass.
