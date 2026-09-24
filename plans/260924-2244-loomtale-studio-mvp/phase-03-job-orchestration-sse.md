# Phase 03: Job orchestration (River), GPU slot, resume, SSE progress

## Context links
- [plan.md](plan.md) · [contract §2 AC4, AC5; §5 option A; §11 performance](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- [backend research §2–3 (River state machine, GPU single slot, SSE)](../reports/researcher-260924-2128-backend-architecture-goclaw.md)
- [model research §7: sequential load/unload per stage](../reports/researcher-260924-2128-local-ai-models-rtx5060ti.md)
- Depends on phase 2. Runs in parallel with phases 4 and 5 under the interface pact below.

## Overview
- Priority: P1 · Status: pending · Effort: 26h <!-- RT#15 re-estimate -->
- This phase builds a generic pipeline engine. Each step is a row in Postgres and each execution is a River job. The engine provides lost-wakeup-free fan-in, a per-step DB compare-and-set claim as the only fence, crash resume, stale detection by input hash, cancel and retry, priorities, a single GPU slot with VRAM-aware admission, admission checks (quota, disk), and a versioned SSE progress stream. Phases 6–10 only register step handlers; they never touch the engine.

## Requirements
- Queues <!-- RT#6 -->:
  - `gpu` (MaxWorkers=1, enabled only when `WORKER_GPU=true`, guarded by the Postgres advisory lock `gpu_slot`)
  - `cpu` (N = cores − 2 = 10 on the 12-vCPU VM)
  - `llm` (remote LLM providers; concurrency comes from the provider semaphore)
  - `render` (2; NVENC is an encoder, not a model; admitted only with a VRAM reserve, see GPU executor)
  - `io` (uploads, sync, model pulls)
- **Priorities** (River priority, lower runs first): 1 interactive (writer AI actions, single regenerate), 2 per-scene reruns, 3 batch generation, 4 training and benchmarks. <!-- RT#6 -->
- **Batch chunking:** batch GPU work is split into jobs of ≈10 min estimated duration (by stage and model), so an interactive job waits at most one chunk. <!-- RT#6 -->
- Step state is `pending → queued → running → done | failed | canceled`, with `stale` derived as `done` where the stored `input_hash` ≠ the current hash. Every state or progress change increments `pipeline_steps.version` (bigint). <!-- RT#2 -->
- **Claim and fencing (the only fence)** <!-- RT#1 -->:
  - River job args are **step IDs only**. A handler claims its steps with one CAS: `UPDATE pipeline_steps SET attempt = attempt + 1, status = 'running', heartbeat_at = now(), claimed_job_id = $job WHERE id = ANY($ids) AND status = 'queued' RETURNING id, attempt`. Steps not returned are skipped (someone else owns them or they were canceled).
  - Heartbeat (every 10s) and output commit are conditional on `attempt = $mine AND status = 'running'`. Zero rows updated means the step was reclaimed: the handler cancels its ctx and exits without writing.
  - The reconciler (boot + every 60s) resets `running` steps whose heartbeat is older than 60s to `queued`, then enqueues new jobs **regrouped by (stage, model)** and chunked as above.
  - River `RescueStuckJobsAfter` is set to 4h, longer than the longest `JobTimeout` (render 3h), so River never re-runs a live job. River rescue and the reconciler may both enqueue a job for the same step; the CAS makes that harmless.
- **Fan-in without lost wakeups** <!-- RT#2 -->: each step has `remaining_deps int`. Completing a step, in the same tx as its output commit, runs `UPDATE pipeline_steps SET remaining_deps = remaining_deps - 1 WHERE id IN (dependents) RETURNING id, remaining_deps`; each dependent reaching 0 moves `pending → queued` and is inserted with `InsertTx` using River unique options keyed by step ID and attempt. The reconciler also sweeps `pending` steps with `remaining_deps = 0` and no live job. `MarkStaleDependents` recomputes `remaining_deps` from `pipeline_step_deps`.
- **Admission checks** in `pipeline.Enqueue`: a registered list of `AdmissionCheck` functions runs before insert. Phase 3 registers `quota.Check` (reads `tenant_quotas`; unlimited by default locally). Phase 8 registers the disk watermark check. A failed check returns problem+json 429/507 and enqueues nothing. <!-- RT#2 RT#10 -->
- Per-scene rerun creates a new step for that scope only, and its dependents are marked for re-execution. A per-scene rerun is a batch of 1 at priority 2.
- **Queue resolution at enqueue:** `Queue(ctx, StepRef)` is resolved when the step is enqueued (for example an LLM step whose provider is Ollama resolves to `gpu`, others to `llm`). The resolved queue and `provider_ref` are stored on the step row, so a settings change after enqueue does not move a queued step. <!-- RT#6 -->
- Failures are classified:
  - Transient (network, 5xx, timeouts) are retried with backoff (max 3).
  - `gpu_oom` (CUDA OOM from any backend) triggers a **full unload of all backends** through residency, then exactly 1 retry; a second OOM is permanent. <!-- RT#5 -->
  - Permanent errors (validation, `engine_not_installed`, licence refused) call `river.JobCancel` and mark the step `failed` with an `error_code`.
- Progress persistence is throttled to ≤1 write per second per step. Events go out through `pg_notify('lt_events', ≤1KB json)` carrying IDs, numbers and `version` only.
- **SSE** at `GET /api/v1/events?topics=...` <!-- RT#2 -->: cookie auth, tenant-filtered, 250ms per-key coalescing for **progress only** (state transitions and terminal events are never coalesced or dropped), each event carries `version`, 15s heartbeat comment, no replay. The first event on a stream is `ready`; clients subscribe first and fetch snapshots after `ready`, discarding events whose `version` ≤ the snapshot's. If the hub's `LISTEN` connection drops, it reconnects and sends `resync` to every subscriber, which refetches snapshots. Cap: ≤6 streams per **user** (the web client shares one stream across tabs, phase 5).
- **GPU status schema (full, owned here)** <!-- RT#15 -->: `GET /api/v1/gpu` returns `GpuStatus{ running: StepSummary|null, queue: StepSummary[] (gpu queue by priority, scheduled_at), resident: {backend, model}|null, vram: {total_mb, free_mb, budget_mb, render_reserve_mb, measured_at}|null, backends: [{name, reachable, loaded: string[]}], encoder: {name, hw: bool}|null, capabilities: string[] }`. Phases 4, 8 and 9a–9c only populate it through Go interfaces (`GpuProbe`, `EncoderInfo`, `CapabilitySource`); they never edit `openapi/schemas/gpu.yaml`.

## Architecture
- Tables:
  - `pipeline_runs(id, tenant_id, scope_kind, scope_id, kind, status, superseded_by, created_by)`
  - `pipeline_steps(id, tenant_id, run_id, scope_kind, scope_id, kind, queue, provider_ref, priority, status, attempt, version, remaining_deps, claimed_job_id, input_hash, progress, eta_s, output jsonb, error_code, error_msg, log_asset_id, heartbeat_at, started_at, finished_at)`
  - `pipeline_step_deps(tenant_id, step_id, depends_on_step_id)` <!-- RT#14 -->
  - Indexes: `(tenant_id, scope_kind, scope_id, kind)`, `(run_id, status)`, partial `(status) where status in ('queued','running')`, partial `(id) where status = 'pending' and remaining_deps = 0`.
- `scope_kind/scope_id` is polymorphic without an FK, because episodes and scenes arrive in later phases. Domain delete services call `pipeline.CancelScope` inside their own transaction. `pipeline.SupersedeRun(old, new)` cancels a run and links it (used by phase 8).
- The handler contract (`api/internal/pipeline/handler.go`) <!-- RT#6 -->:
  `type StepHandler interface { Kind() string; Queue(ctx context.Context, s StepRef) (string, error); InputHash(ctx, StepRef) (string, error); ModelRef(ctx, StepRef) (*ModelRef, error); Run(ctx, *StepContext) (Output, error) }`
  `StepContext` provides `Progress(pct, eta)`, `Log(line)` (ring buffer, scrubbed, flushed to an asset), `Tenant()`, `Storage()` (the internal client) and `Attempt()`.
- One River job kind, `pipeline_step`, carries the args `{StepIDs []uuid}`. The dispatcher claims by CAS, looks up the handler by step kind (DRY), and commits outputs plus fan-in in one tx.
- **GPU executor** <!-- RT#5 RT#6 -->: `pg_try_advisory_lock(gpu_slot)` on a dedicated connection; if not acquired, `JobSnooze(5s)`. A watchdog pings that connection every 5s and cancels the job ctx if it dies. Then **prefer resident model**: if the job's model differs from `residency.Current()` and a queued gpu step with the same priority for the resident model exists, snooze (bounded to 3 snoozes per job, so nothing starves). Then `ModelResidency.Ensure(ctx, modelRef)` (which waits for VRAM, phase 4) → run → keep the model resident.
- **Render admission** <!-- RT#5 -->: `render.*` jobs start only if `GpuProbe` reports `free_mb ≥ render_reserve_mb` (default 1024); otherwise they snooze 15s.
- **Interface pact with phase 4** (declared here, implemented there): `type ModelResidency interface { Ensure(ctx context.Context, m ModelRef) error; UnloadAll(ctx context.Context) error; Current() *ModelRef }` and `type GpuProbe interface { Snapshot(ctx) (GpuSnapshot, error) }`. Phase 3 ships `NoopResidency` used only when `WORKER_GPU=false`.
- The SSE hub opens one `LISTEN` connection per API instance (with reconnect + `resync`) and fans events out through buffered channels. Slow consumers drop intermediate progress events and keep the latest per key; transitions are queued, and a consumer that overflows the transition queue is sent `resync` and has its buffer reset.

## Related files
- Create:
  - `db/migrations/*_pipeline.sql`, `db/queries/pipeline.sql`
  - `api/internal/pipeline/{engine,handler,dispatcher,claim,fanin,reconciler,admission,gpu_executor,hash,classify}.go`
  - `api/internal/quota/`, `api/internal/sse/{hub,handler}.go`
  - `openapi/paths/{jobs,events,gpu}.yaml`, `openapi/schemas/{jobs,events,gpu}.yaml`
- Create or own: `api/cmd/worker/main.go` (River client wiring; this phase owns `cmd/*` from now on).
- Modify: `openapi/root.yaml`. `WORKER_GPU` defaults to `false` in code; phase 4 sets `WORKER_GPU=true` on the worker in `compose.gpu.yml`, because phase 4 owns both compose files during parallel group C (file ownership).

## Implementation steps
1. Write the migration, queries, and the step state transition functions (a pure function table with exhaustive tests), including `version` and `remaining_deps`.
2. Set up the River client: queues, priorities, `RescueStuckJobsAfter` 4h, `JobTimeout` per queue (gpu 30m for ≈10-min chunks, render 3h, io 2h) with a per-kind override through the worker's `Timeout(job)` (`train.lora` 2h, `bench.*` 1h; every override must stay below `RescueStuckJobsAfter`, asserted by a startup check), error handler → classify, graceful shutdown (stop fetching, wait up to 30s, then cancel ctx).
3. Write the CAS claim, the dispatcher, fan-in with the atomic counter and unique insert, batch chunking, and per-step results (partial failure marks only the failed steps).
4. Write the reconciler (boot plus periodic: stale heartbeats, ready `pending` sweep, regroup by stage and model) and the conditional heartbeat goroutine.
5. Write the admission-check registry with `quota.Check`.
6. Write the GPU executor (try-lock + snooze, lock watchdog, resident-model preference, residency) and the render admission gate.
7. Add the API endpoints:
   - `POST /runs`, `GET /runs/{id}`, `GET /runs/{id}/steps?cursor`
   - `POST /steps/{id}/retry`, `POST /steps/{id}/cancel`, `POST /runs/{id}/cancel`
   - `GET /jobs?status&queue&cursor` (render queue page)
   - `GET /steps/{id}/log` (presigned; **editor or owner only**) <!-- RT#14 -->
   - `GET /gpu` with the full schema
8. Write the SSE hub, handler, topic authorisation, `ready`/`resync`, versioning and the per-user cap. Emit events on every state change and on throttled progress.

## Todo checklist
- [ ] Schema + transition table + version + remaining_deps
- [ ] River client + queues + priorities + classify (incl. gpu_oom)
- [ ] CAS claim + dispatcher + fan-in + chunking
- [ ] Reconciler (heartbeats, ready sweep, regroup)
- [ ] Admission checks (quota)
- [ ] GPU executor (try-lock, watchdog, resident preference) + render VRAM admission
- [ ] Jobs/runs/steps/gpu endpoints
- [ ] SSE hub (ready, resync, version, per-user cap)

## Performance budget checks
- Enqueueing 300 scene steps plus deps takes one tx and must finish in ≤150ms (integration benchmark).
- `GET /jobs` p95 ≤50ms with 50k steps seeded; `EXPLAIN` shows index use and there is no N+1.
- The SSE hub must sustain 200 subscribers and 500 events/s input, with ≤4 progress events/s/key delivered, zero dropped transitions, and ≤30MB RSS (Go benchmark).
- Progress DB writes stay ≤1/s/step, as asserted by a counter in the test.
- Interactive wait behind batch GPU work ≤ one chunk (≈10 min) plus residency switch, asserted with a test-only 2s-chunk handler.

## Security checklist
- [ ] SSE topics authorised against tenant ownership; unknown topics rejected
- [ ] NOTIFY payload has IDs, numbers and versions only, no user text
- [ ] Step logs scrubbed (secrets and capability URLs) before buffering; served presigned to editor+ only
- [ ] Retry/cancel require editor role and are audited
- [ ] River job args contain step IDs only (no prompts, no tokens, no attempt)
- [ ] `pipeline_step_deps` carries `tenant_id`; every pipeline query is tenant-scoped

## Reuse points
- Create `pipeline.StepHandler` (the single extension point for LLM, image, TTS, align, render, publish and analytics steps), `pipeline.Enqueue`, `pipeline.AdmissionCheck`, `pipeline.MarkStaleDependents`, `pipeline.SupersedeRun`, and `sse.Publish(topic, event)`.
- Reuse phase 2's cursor helper, audit, scrubber and problem+json.

## Tests
- `scripts/tb.ps1 test` covers the transition table, classify and hash.
- `scripts/tb.ps1 test-integration` covers:
  - a fan-in DAG with concurrent dep completion (100 iterations, `-race`): the dependent is enqueued exactly once, never zero
  - batch partial failure
  - crash resume: a helper process runs a long test-only handler and is SIGKILLed; the reconciler requeues, the new job claims attempt 2, and a forced duplicate job (simulated River rescue) claims nothing and exits
  - a zombie writer: a handler whose step was reclaimed fails its heartbeat and commits nothing
  - the GPU lock: two worker processes never run gpu steps concurrently; killing the lock connection cancels the running job
  - priorities: an interactive job starts before queued batch chunks
  - SSE: subscribe → `ready` → progress coalesced, transitions all delivered; a forced LISTEN drop yields `resync`; tenant B receives nothing
- `go test ./internal/sse -bench .`

## Success criteria
- Killing the worker mid-step and restarting it completes the run without re-running finished steps and without any step running twice concurrently (AC4, resume).
- Rerunning one scene step re-executes only that step and its dependents (AC4, per-step).
- The browser (a curl `-N` check here, UI in phase 5) receives per-step progress and GPU queue changes within ≤1s, and never misses a terminal event (AC5, backend half).

## Risks + rollback
- Double execution through River rescue plus reconciler (Medium×High): rescue window 4h > longest timeout, and the per-step CAS plus conditional heartbeat/commit is the single fence.
- Advisory lock silently lost with its connection (Low×High): dedicated connection, 5s watchdog cancels the job ctx.
- Batch starvation of interactive work (Medium×Medium): priorities plus ≈10-min chunks; the resident-model preference is bounded to 3 snoozes.
- A NOTIFY storm (Low×Medium) is prevented by the throttle and coalescing.
- Rollback: revert the PR and the goose down migration. No other phase data exists yet.

## Next steps
Phases 6–8 register handlers. Phase 5 consumes `/events`, `/jobs` and `/gpu`.
