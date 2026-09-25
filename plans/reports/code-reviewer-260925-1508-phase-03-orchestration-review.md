# Code review: phase 03 job orchestration + SSE (`feat/job-orchestration-sse` e1c21d2, 6cd57f0)

Scope: 83 files, about 11.3k added lines (4.2k of them hand-written Go/SQL). Checked against phase-03 spec, red-team #1/#2/#5/#6/#14, and the cook report.
Evidence: code read plus a live stack (`-p ltcrp3`, `compose.yml + compose.integration.yml`, since torn down with `down -v`). I ran throwaway probe tests from a scratch copy (none committed). I also ran the branch's own pipeline tests `-race -count=2` with the live `worker` up, the same way CI runs them.

**Verdict: do not merge.** 1 Critical, 5 High, 9 Medium, 5 Low. 5 findings block the merge (C1, H1-H4).

## Critical

**C1. After a worker crash, the reconciler's re-enqueue is deduped against the dead job, so the step stalls for about 4h. Blocks merge: yes.**
- Where: `pipeline/enqueue_jobs.go:65-75`, `reconciler.go:67-75`
- Defect: the unique key is `ByArgs` (step ids) with `ByState` including `running`. After a SIGKILL, River leaves the dead job's row in `running` until `RescueStuckJobsAfter`, which is 4h. The reconciler moves the step to `queued` and calls `InsertTx` with identical args. River treats that as a duplicate and inserts nothing.
- Verified by probe: `step={Status:queued Attempt:1} available_jobs=0 running_jobs=1`. Recovery only happens when River's rescuer runs 4h later. If the rescued attempt is already at MaxAttempts=3, River discards it and the step is orphaned in `queued` forever.
- Spec impact: this breaks AC4 ("kill worker mid-step, restart, run completes"). `TestReconcilerResumesAfterCrash` misses it because it never puts the River job into `running` and claims by hand.
- Fix: the CAS is the fence, so the reconciler and retry inserts should not use `UniqueOpts`. Alternatively, have the reconciler cancel/finalize `claimed_job_id` in the same tx first. Add a regression test that sets `river_job.state='running'` before `RunOnce` and asserts an available job exists afterwards.

## High

**H1. Steps get stranded in `queued` with no River job, and nothing ever recovers them. Blocks merge: yes.**
- Transient path (`dispatcher.go:102-106`): `requeueForRetry` sets the step to `queued` and returns the error. On River attempt 3 of 3 (`enqueue_jobs.go:68`) River discards the job, and the step stays `queued` forever.
- The same thing happens when an error is returned before Claim, 3 times in a row. Examples: pool `Acquire` or try-lock error (`gpu_executor.go:57-66`), `preferResident`/`ensureModel` error including `NoopResidency` (`:80-89`), render probe error (`worker.go:76-79`).
- Impact: those steps count against `max_active_steps` indefinitely (`quota/check.go:38`). The UI shows them queued forever. The reconciler only sweeps `running` and `pending` (`reconciler.go:44-51`).
- Fix: pass `job.Attempt`/`job.MaxAttempts` into `DispatchOpts` and `commitFailed` on the last attempt; add a bounded reconciler sweep for `queued` steps older than N min with `NOT EXISTS` a live `river_job` whose `args->'step_ids' ? id`.

**H2. Chunk dispatch claims every step up front but only heartbeats the one that is running. Blocks merge: yes (chunking is a phase-3 requirement).**
- Where: `dispatcher.go:43-63` claims all ids; `:77` starts the heartbeat per step inside `runOne`.
- Steps 2..N of a chunk (target ≈10 min) keep the `heartbeat_at` from claim time. After 60s the reconciler resets them to `queued` and re-enqueues them, and another cpu/llm worker claims attempt+1 and runs them. The original worker then reaches the same step and runs the handler anyway with its stale row: `runOne` never re-checks ownership, and the heartbeat only fires after 10s.
- Result: side effects run twice at the same time (LLM spend, publish). Only the output commit is fenced.
- It is latent today only because the nil estimator gives chunk size 1. The first phase that registers an estimator turns it on.
- Fix: claim lazily, one step at a time, right before `runOne`. Or heartbeat the whole claimed set in one goroutine (`WHERE id = ANY($remaining) AND attempt = ...`).

**H3. A worker with no handler claims the step and permanently fails it; this is a design bug and the CI integration job goes red. Blocks merge: yes.**
- Where: `dispatcher.go:70-74`, `cmd/worker/main.go:85-93`
- Verified: I ran the branch tests with the live `worker` up, the same way `ci.yml:113-125` runs them.
  - `TestInteractiveStepWaitsAtMostOneChunkBehindBatch` failed in 2 of 2 runs.
  - `TestFanIn...ExactlyOnce` and `TestSSESubscribeReady...` each failed in 1 of 2 runs.
  - The DB afterwards had 17 test steps marked `no_handler|failed`.
- Why it is a design bug: any worker binary that does not know a kind (empty registry today; old binary during a rolling deploy; a worker with GPU disabled) destroys the step instead of leaving it for a capable worker.
- Fix:
  - `PeekSteps` before `Claim`. If any kind is unregistered, do not touch the rows and return `river.JobSnooze(30s)` (snoozing does not consume attempts in River v0.47). Only fail `no_handler` past an age bound.
  - Enable a queue only if at least one registered handler can resolve to it.
  - For tests, isolate from the live worker: a CI compose profile that stops `worker`, or a separate DB/River schema per test run. `DELETE FROM river_job` (`pipeline_scheduling_test.go:80`) is not isolation.

**H4. `RetryStep` revives steps whose dependencies are unmet and steps belonging to canceled or superseded runs. Blocks merge: yes.**
- Where: `pipeline.sql:173` (RetryStep), `cancel.go:23-46`
- The query ignores `remaining_deps` and `run.status`.
- Verified by probe: cancel run → retry B (depends on A, which is canceled) → `b={Status:queued RemainingDeps:1}` with 1 live job, while the run stays `canceled`.
- Impact: the step runs without its inputs, or renders from a stale manifest after supersede. Any editor can trigger this.
- Fix:
  - Retry to `pending` when `remaining_deps > 0`, else `queued`.
  - Reject the retry unless `pipeline_runs.status='active'` (join in the UPDATE); otherwise require a new run.
  - Recompute `remaining_deps` on retry.

**H5. `POST /runs` trusts the client's step graph. Blocks merge: no, but it must be fixed before any handler is registered in cmd/api; today the API registry is empty, so every request returns 400.**
- Where: `enqueue.go:117-163`, `runs.go:45-51`, `schemas/jobs.yaml:101-140`
- `dependsOn` is not validated:
  - A step id from another tenant is accepted. Verified: `err=<nil>`; the FK checks only `id`. That step can never be decremented, because `fanin.go:19` filters by the completer's tenant.
  - A dependency on an already-`done` step still counts as unmet.
  - Cycles and self-dependencies are accepted.
  - All three leave steps `pending` forever.
- Client-chosen step/run ids plus the raw pg error in the 400 give a cross-tenant existence oracle. Verified: a colliding foreign id returns `duplicate key ... "pipeline_steps_pkey"`. A DB outage also comes back as 400, not 5xx.
- The client sets priority 1-4, so any editor can mark batch work interactive and starve every tenant on the one GPU.
- No `maxItems` on `steps`/`dependsOn`.
- Fix:
  - Require `dependsOn ⊆ ids in the same request`, with a DFS cycle check.
  - Generate ids server-side, or map 23505/23503 to a generic 409/400.
  - Only map validation errors to 400; everything else is 500.
  - Derive priority server-side from kind/action.
  - Add schema `maxItems`.

## Medium

**M1. GPU lock release has races and no ping timeout.**
- Where: `gpu_executor.go:70-78, 104-118`
- The watchdog goroutine is cancelled but not joined before `pg_advisory_unlock` runs on the same `*pgxpool.Conn`. A Ping still in flight makes the unlock fail with "conn busy".
- The unlock error is discarded (`_, _ =`). The connection then goes back to the pool still holding the session lock. Session advisory locks are re-entrant, so other processes are locked out until pgxpool recycles that connection.
- `Ping(ctx)` has no timeout, so a half-open TCP connection never trips the watchdog.
- Fix: wait for the watchdog to exit; `Ping` with a 2s timeout; if unlock fails, `conn.Hijack()`/Close instead of `Release`.

**M2. gpu_oom handling inside a chunk is wrong in three ways (`commit.go:111-125`, `dispatcher.go:56-62`, `enqueue_jobs.go:34-41`).**
- After `UnloadAll`, the loop keeps running the remaining claimed steps with no model loaded.
- The retry budget is River's attempt count, so a transient failure on attempt 1 followed by a first OOM on attempt 2 is treated as permanent.
- Chunks are grouped by (queue, kind), not by model (the spec says stage+model), and `ensureModel` only loads `steps[0]`'s model (`gpu_executor.go:159-180`).
- Fix: after an OOM, stop the chunk and requeue the rest; count OOMs on the step; add `provider_ref` to the group key.

**M3. If the commit fails, the step is stranded in `running` and re-executed.**
- Where: `commit.go:29-57`, `dispatcher.go:43-53`
- Any commitDone error (deadlock, failover) rolls back. River retries, `Claim` finds no `queued` row and returns nil, and the job completes. The step sits in `running` until the reconciler resets it about 60s later, and then the successful work is redone.
- Fix: bounded retry of the commit with the same attempt; if it still fails, explicitly `RequeueStep`.

**M4. `GET /jobs` never uses the new index. Verified with EXPLAIN (ANALYZE) on 2×50k seeded rows with a forced generic plan.**
- Where: `pipeline.sql:55`
- Every filter combination planned as a `pipeline_steps_pkey` scan with the tenant/status/queue conditions applied as a filter, never the new index. Rows removed: 120 (no filter), 1077 (status), 6473 (queued+gpu).
- Cost grows as tenant share × selectivity shrinks, so it becomes O(table) for a small tenant or a rare status. The p95 budget was never measured (cook report Q2).
- Fix: add `(tenant_id, id)` and `(tenant_id, status, id)` indexes. Split the `(@x='' OR col=@x)` query into per-filter sqlc queries, because generic plans cannot use the OR form.

**M5. SSE hardening gaps.**
- `events.go:56-80`: topics are unbounded, one `GetRun` per topic (~28k ids fit in the URL). Fix: cap at ~50 and use `WHERE id = ANY`. Raw DB errors are returned as the 403 detail.
- `subscriber.go:329-344`: a buffered progress event can be flushed after the step's terminal transition. Fix: drop `progress[step]` when a transition with version ≥ it arrives.
- There is no maximum stream lifetime, so a revoked session or removed membership keeps receiving events.
- `cmd/api/main.go:246-256`: `server.Shutdown` waits on SSE connections that never go idle, so every deploy stalls for `ShutdownTimeout` and exits with an error. Fix: `RegisterOnShutdown` → cancel the hub/subscriber contexts.

**M6. Error text can leak data, and step logs are only half-scrubbed.**
- `commit.go:75`: `error_msg` stores raw `runErr.Error()` unscrubbed.
- `handler.go:72-77`: it is returned to **viewers** via `/jobs` and `/runs/{id}/steps`, while the log itself is editor-only.
- `log.go:33`: the ring buffer only applies `scrub.URL`; it does not remove bearer/API-key secrets, which the spec requires.
- Fix: scrub (URL + secret patterns) before persisting; return `error_code` only to viewers.

**M7. Quota check is racy and incomplete.**
- Where: `quota/check.go:26-46`
- Check-then-insert happens outside any lock, so N concurrent `CreateRun` calls all pass.
- `pending` steps are not counted, so repeated runs made of mostly-pending steps bypass the limit.
- Fix: take `pg_advisory_xact_lock(tenant)` inside the Enqueue tx and count pending as well.

**M8. The state machine is not enforced anywhere.**
- `CanTransition` is used only in tests. The table allows `done→queued` but the SQL does not. The DB has no trigger.
- Nothing rolls runs up to `done`/`failed`, so dependents of a failed step stay `pending` and the run stays `active` forever.
- `CancelRun` (`cancel.go:66-78`) overwrites `done`/`superseded` and returns 204 plus an audit row for a run that does not exist.
- `SupersedeRun` (`cancel.go:104-113`) is not atomic. The FK needs the new run to exist first. Verified: a 23503 error after `CancelRun` had already committed.
- Fix: route status writes through `CanTransition` or a BEFORE UPDATE trigger; add run status rollup; make supersede a single tx.

**M9. Contract gaps against the spec that phases 5-10 depend on.**
- `MarkStaleDependents` (`fanin.go:64-79`) only recomputes the counter. It never moves `done` dependents back to `pending`, so re-execution never happens, and its dependency join is not tenant-scoped (`pipeline.sql:157`).
- Missing: `StepContext.Storage()`, `sse.Publish`, and GPU-queue SSE events (success criterion: "GPU queue changes ≤1s").

## Low
- **L1.** `ResetStaleHeartbeats`/`ReadySweep` are unbounded single-tx sweeps. Add `LIMIT` + `FOR UPDATE SKIP LOCKED` and loop.
- **L2. Test quality.**
  - Destructive global actions: `DELETE river_job` (`pipeline_scheduling_test.go:80`); `pg_terminate_backend` of every advisory-lock holder and every LISTEN backend, including the live api/worker's River notifier (`pipeline_gpu_test.go:152`, `pipeline_sse_test.go:197`).
  - There is no HTTP-level SSE test (403 foreign topic, 429 cap, flush through Caddy's `encode gzip`), and no SIGKILL or two-process tests as the spec requires.
  - Concurrency is covered only in the fan-in test (40 goroutines); all other tests are sequential.
- **L3. Housekeeping.**
  - `go.mod` marks river `// indirect` even though it is imported directly (tidy drift).
  - The comment at `pipelineapi/handler.go:3-5` refers to a deleted `sse.Handler`.
  - The `GPUExecutor.snoozes` map leaks entries for jobs that end without being cleared.
  - `migrate down` does not revert River's tables.
  - The cook report is untracked in the branch worktree.
- **L4.** `DecodeCursor` failure silently resets to page 1 (`runs.go:83-86`, `steps.go:68-71`). Return 400.
- **L5.** The `compose.yml` worker MinIO env (4 vars): **acceptable**. It is additive, uses the same app key as the api, and the worker started healthy. Later, use a MinIO user scoped to `pipeline-logs/`.

## Verified OK
Single-step CAS/commit/heartbeat fences (zombie commit fenced); fan-in decrement exactly-once under row locks; job args ids only; `AssertTimeoutsBelowRescue` in both binaries; snooze does not consume River attempts; `x-min-role` on every new route (log presign editor+); tenant from session only; NOTIFY payloads ids only; River tables covered by owner default privileges.

Fix order: C1 → H1 → H2 → H3 (CI) → H4 → M3/M1 → H5 (before phase 6) → rest.

## Unresolved questions
1. River v0.47: can `UniqueOpts.ByState` legally omit `running`? If not, C1 needs "no uniqueness" or cancelling `claimed_job_id` rather than a narrower state list.
2. Does Caddy 2.x `encode gzip` flush `text/event-stream` per event behind `reverse_proxy`? I did not verify this end to end. Consider excluding `/api/v1/events` from `encode`.
3. Should priority be client-settable at all (H5), or only derived per kind? This is a product decision.
4. What is the CI isolation choice for H3: stop `worker` in the integration job, or a separate River schema/DB per test run?
