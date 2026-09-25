-- name: CreateRun :one
INSERT INTO pipeline_runs (id, tenant_id, scope_kind, scope_id, kind, created_by)
VALUES (@id, @tenant_id, @scope_kind, @scope_id, @kind, @created_by)
RETURNING *;

-- name: GetRunIDsForTenant :many
-- Batch existence check for SSE topic authorization: one query for every
-- requested topic instead of one round trip each.
SELECT id FROM pipeline_runs WHERE tenant_id = @tenant_id AND id = ANY(@ids::uuid[]);

-- name: GetRun :one
SELECT * FROM pipeline_runs WHERE tenant_id = @tenant_id AND id = @id;

-- name: MarkRunStatusIfNotTerminal :one
-- Guards against a cancel/rollup racing an already-terminal run (done,
-- failed, canceled or superseded): only a run still "active" can change
-- status through this path.
UPDATE pipeline_runs
SET status = @status, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND status = 'active'
RETURNING *;

-- name: SupersedeRunTx :one
-- Cancels and links a run to its replacement in a single statement (the
-- caller wraps this with CancelRunSteps in one transaction): the run row
-- itself never passes through an intermediate "canceled" state that a
-- concurrent reader could observe, and newRunID must already exist
-- (insert it before calling this, never after).
UPDATE pipeline_runs
SET status = 'superseded', superseded_by = @new_run_id, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @old_run_id AND status = 'active'
RETURNING *;

-- name: CountNonTerminalStepsInRun :one
SELECT count(*) FROM pipeline_steps
WHERE tenant_id = @tenant_id AND run_id = @run_id AND status NOT IN ('done', 'failed', 'canceled');

-- name: HasFailedStepsInRun :one
SELECT EXISTS (
    SELECT 1 FROM pipeline_steps WHERE tenant_id = @tenant_id AND run_id = @run_id AND status = 'failed'
);

-- name: InsertStepBatch :batchone
-- Batched (pgx pipelining) so enqueueing hundreds of steps in one
-- transaction stays within the enqueue latency budget.
INSERT INTO pipeline_steps (
    id, tenant_id, run_id, scope_kind, scope_id, kind, queue, provider_ref,
    priority, status, remaining_deps, input_hash
) VALUES (
    @id, @tenant_id, @run_id, @scope_kind, @scope_id, @kind, @queue, @provider_ref,
    @priority, @status, @remaining_deps, @input_hash
)
RETURNING *;

-- name: InsertStepDepBatch :batchexec
INSERT INTO pipeline_step_deps (tenant_id, step_id, depends_on_step_id)
VALUES (@tenant_id, @step_id, @depends_on_step_id);

-- name: GetStepByID :one
SELECT * FROM pipeline_steps WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListRunSteps :many
SELECT * FROM pipeline_steps
WHERE tenant_id = @tenant_id AND run_id = @run_id AND id > @cursor
ORDER BY id
LIMIT @page_limit;

-- name: ListJobs :many
-- No filter: the (tenant_id, id) index.
SELECT * FROM pipeline_steps
WHERE tenant_id = @tenant_id AND id > @cursor
ORDER BY id
LIMIT @page_limit;

-- name: ListJobsByStatus :many
SELECT * FROM pipeline_steps
WHERE tenant_id = @tenant_id AND status = @status_filter AND id > @cursor
ORDER BY id
LIMIT @page_limit;

-- name: ListJobsByQueue :many
SELECT * FROM pipeline_steps
WHERE tenant_id = @tenant_id AND queue = @queue_filter AND id > @cursor
ORDER BY id
LIMIT @page_limit;

-- name: ListJobsByStatusAndQueue :many
SELECT * FROM pipeline_steps
WHERE tenant_id = @tenant_id AND status = @status_filter AND queue = @queue_filter AND id > @cursor
ORDER BY id
LIMIT @page_limit;

-- name: ListGpuQueueForTenant :many
SELECT * FROM pipeline_steps
WHERE tenant_id = @tenant_id AND queue = 'gpu' AND status = 'queued'
ORDER BY priority, id
LIMIT @page_limit;

-- name: GetRunningGpuStepForTenant :one
SELECT * FROM pipeline_steps
WHERE tenant_id = @tenant_id AND queue = 'gpu' AND status = 'running'
ORDER BY started_at DESC NULLS LAST
LIMIT 1;

-- name: ClaimSteps :many
-- The only fence: a handler only owns the rows this query returns. Driven
-- solely by River job args (step ids the dispatcher itself enqueued),
-- never by request input, so it is not tenant-filtered.
-- lint-tenant-queries:allow: claim is fenced by id = ANY(job args) + status, not by caller-supplied tenant
UPDATE pipeline_steps
SET attempt = attempt + 1,
    status = 'running',
    heartbeat_at = now(),
    claimed_job_id = @job_id,
    started_at = COALESCE(started_at, now()),
    version = version + 1
WHERE id = ANY(@ids::uuid[]) AND status = 'queued'
RETURNING *;

-- name: HeartbeatStep :execrows
-- lint-tenant-queries:allow: internal heartbeat fenced by id+attempt, not caller input
UPDATE pipeline_steps
SET heartbeat_at = now()
WHERE id = @id AND attempt = @attempt AND status = 'running';

-- name: UpdateStepProgress :one
-- lint-tenant-queries:allow: internal progress write fenced by id+attempt, not caller input
UPDATE pipeline_steps
SET progress = @progress, eta_s = @eta_s, version = version + 1
WHERE id = @id AND attempt = @attempt AND status = 'running'
RETURNING *;

-- name: CommitStepDone :one
-- lint-tenant-queries:allow: internal output commit fenced by id+attempt, not caller input
UPDATE pipeline_steps
SET status = 'done', output = @output, progress = 100, log_asset_id = @log_asset_id,
    finished_at = now(), version = version + 1
WHERE id = @id AND attempt = @attempt AND status = 'running'
RETURNING *;

-- name: CommitStepFailed :one
-- lint-tenant-queries:allow: internal failure commit fenced by id+attempt, not caller input
UPDATE pipeline_steps
SET status = @status, error_code = @error_code, error_msg = @error_msg,
    log_asset_id = @log_asset_id, finished_at = now(), version = version + 1
WHERE id = @id AND attempt = @attempt AND status = 'running'
RETURNING *;

-- name: FailQueuedStep :one
-- Used by the reconciler when a "queued" step has exceeded its stranded
-- re-enqueue budget: unlike CommitStepFailed this fences on status =
-- 'queued', not 'running', since a stranded step was never re-claimed.
-- lint-tenant-queries:allow: internal reconciler write, not caller input
UPDATE pipeline_steps
SET status = 'failed', error_code = @error_code, error_msg = @error_msg,
    finished_at = now(), version = version + 1
WHERE id = @id AND status = 'queued'
RETURNING *;

-- name: IncrementStrandedRequeue :one
-- lint-tenant-queries:allow: internal reconciler write, not caller input
UPDATE pipeline_steps
SET stranded_requeues = stranded_requeues + 1, version = version + 1
WHERE id = @id AND status = 'queued'
RETURNING *;

-- name: IncrementGpuOomCount :one
-- Durable OOM counter, checked instead of River's own attempt count: a
-- transient failure on attempt 1 followed by the first real OOM on
-- attempt 2 must still count as "first OOM", not "second".
-- lint-tenant-queries:allow: internal failure-path write fenced by id+attempt, not caller input
UPDATE pipeline_steps
SET gpu_oom_count = gpu_oom_count + 1, version = version + 1
WHERE id = @id AND attempt = @attempt AND status = 'running'
RETURNING *;

-- name: RequeueStep :one
-- lint-tenant-queries:allow: internal retry-requeue fenced by id+attempt, not caller input
UPDATE pipeline_steps
SET status = 'queued', version = version + 1
WHERE id = @id AND attempt = @attempt AND status = 'running'
RETURNING *;

-- name: ResetStaleHeartbeatsBatch :many
-- Bounded (LIMIT + FOR UPDATE SKIP LOCKED) so the reconciler never holds
-- one giant transaction; the caller loops until fewer than page_limit
-- rows come back.
-- lint-tenant-queries:allow: system-wide reconciler sweep, not scoped to a caller's tenant
WITH candidates AS (
    SELECT p.id FROM pipeline_steps p
    WHERE p.status = 'running' AND p.heartbeat_at < @cutoff
    ORDER BY p.id
    LIMIT @page_limit
    FOR UPDATE SKIP LOCKED
)
UPDATE pipeline_steps s
SET status = 'queued', version = version + 1
FROM candidates c
WHERE s.id = c.id
RETURNING s.*;

-- name: ReadySweepBatch :many
-- lint-tenant-queries:allow: system-wide reconciler sweep, not scoped to a caller's tenant
WITH candidates AS (
    SELECT p.id FROM pipeline_steps p
    WHERE p.status = 'pending' AND p.remaining_deps <= 0
    ORDER BY p.id
    LIMIT @page_limit
    FOR UPDATE SKIP LOCKED
)
UPDATE pipeline_steps s
SET status = 'queued', version = version + 1
FROM candidates c
WHERE s.id = c.id
RETURNING s.*;

-- name: OrphanedQueuedStepsBatch :many
-- Candidate "queued" steps under their stranded-requeue budget, for the
-- reconciler to check against River's own job table (not visible to
-- sqlc/goose, so that check is a hand-written query in Go, not here).
-- lint-tenant-queries:allow: system-wide reconciler sweep, not scoped to a caller's tenant
SELECT * FROM pipeline_steps
WHERE status = 'queued' AND stranded_requeues < @max_stranded_requeues
ORDER BY id
LIMIT @page_limit;

-- name: DecrementRemainingDeps :many
UPDATE pipeline_steps
SET remaining_deps = remaining_deps - 1, version = version + 1
WHERE tenant_id = @tenant_id AND id = ANY(@ids::uuid[])
RETURNING *;

-- name: MarkStepsQueued :many
UPDATE pipeline_steps
SET status = 'queued', version = version + 1
WHERE tenant_id = @tenant_id AND id = ANY(@ids::uuid[]) AND status = 'pending' AND remaining_deps <= 0
RETURNING *;

-- name: MarkStepsPending :many
-- Reopens dependents that were already "done" so MarkStaleDependents can
-- put them back in the normal fan-in path instead of leaving a stale
-- done step's dependents permanently skipped.
UPDATE pipeline_steps
SET status = 'pending', version = version + 1
WHERE tenant_id = @tenant_id AND id = ANY(@ids::uuid[]) AND status = 'done'
RETURNING *;

-- name: GetDependents :many
SELECT step_id FROM pipeline_step_deps
WHERE tenant_id = @tenant_id AND depends_on_step_id = ANY(@ids::uuid[]);

-- name: GetDependencies :many
-- The reverse of GetDependents: every step ids' own upstream steps,
-- tenant-scoped (unlike the version this replaces).
SELECT d.step_id, d.depends_on_step_id, dep.status AS depends_on_status
FROM pipeline_step_deps d
JOIN pipeline_steps dep ON dep.id = d.depends_on_step_id
WHERE d.tenant_id = @tenant_id AND d.step_id = ANY(@ids::uuid[]);

-- name: RecomputeRemainingDeps :one
UPDATE pipeline_steps s
SET remaining_deps = (
    SELECT count(*) FROM pipeline_step_deps d
    JOIN pipeline_steps dep ON dep.id = d.depends_on_step_id AND dep.tenant_id = @tenant_id
    WHERE d.tenant_id = @tenant_id AND d.step_id = s.id AND dep.status <> 'done'
), version = s.version + 1
WHERE s.tenant_id = @tenant_id AND s.id = @id
RETURNING s.*;

-- name: CancelStep :one
UPDATE pipeline_steps
SET status = 'canceled', version = version + 1, finished_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND status IN ('pending', 'queued', 'running')
RETURNING *;

-- name: RetryStep :one
-- Only revives a step whose run is still active (never canceled or
-- superseded) and whose own dependencies are already satisfied
-- (remaining_deps <= 0); otherwise this returns no rows and the caller
-- rejects the request with a problem+json error rather than silently
-- re-queuing a step whose inputs are not ready.
UPDATE pipeline_steps s
SET status = 'queued',
    error_code = NULL, error_msg = NULL, gpu_oom_count = 0, stranded_requeues = 0,
    version = s.version + 1
FROM pipeline_runs r
WHERE s.tenant_id = @tenant_id AND s.id = @id
  AND s.run_id = r.id AND r.tenant_id = @tenant_id AND r.status = 'active'
  AND s.status IN ('failed', 'canceled') AND s.remaining_deps <= 0
RETURNING s.*;

-- name: CancelRunSteps :many
UPDATE pipeline_steps
SET status = 'canceled', version = version + 1, finished_at = now()
WHERE tenant_id = @tenant_id AND run_id = @run_id AND status IN ('pending', 'queued', 'running')
RETURNING *;

-- name: CancelPendingDependents :many
-- Used to cascade-cancel the downstream of a permanently failed step:
-- a "pending" step whose upstream will never produce output can never
-- become ready on its own, so it must be cancelled explicitly or the run
-- would stay "active" forever with no step left that could ever finish
-- it.
UPDATE pipeline_steps
SET status = 'canceled', version = version + 1, finished_at = now()
WHERE tenant_id = @tenant_id AND id = ANY(@ids::uuid[]) AND status = 'pending'
RETURNING *;

-- name: CancelScopeSteps :many
UPDATE pipeline_steps
SET status = 'canceled', version = version + 1, finished_at = now()
WHERE tenant_id = @tenant_id AND scope_kind = @scope_kind AND scope_id = @scope_id
  AND status IN ('pending', 'queued', 'running')
RETURNING *;

-- name: PeekSteps :many
-- lint-tenant-queries:allow: internal read-only scheduling peek, not caller input
SELECT * FROM pipeline_steps WHERE id = ANY(@ids::uuid[]);

-- name: CountQueuedGpuStepsForModel :one
-- lint-tenant-queries:allow: cross-tenant GPU scheduling decision, there is only one physical GPU
SELECT count(*) FROM pipeline_steps
WHERE queue = 'gpu' AND status = 'queued' AND priority = @priority AND provider_ref = @provider_ref;

-- name: CountActiveStepsForTenant :one
-- Counts pending steps too: a run made mostly of fan-in-blocked
-- "pending" steps still reserves the capacity they will need once
-- unblocked, so it must count against the same quota queued/running
-- steps do.
SELECT count(*) FROM pipeline_steps WHERE tenant_id = @tenant_id AND status IN ('pending', 'queued', 'running');

-- name: GetTenantQuota :one
SELECT * FROM tenant_quotas WHERE tenant_id = @tenant_id;

-- name: LockTenantForAdmission :exec
-- Serializes concurrent Enqueue calls for the same tenant so the
-- quota check-then-insert in Engine.Enqueue cannot race: every caller
-- must hold this lock (acquired inside the same transaction as the
-- check and the insert) before counting active steps. hashtext's 32-bit
-- output is widened to bigint because pg_advisory_xact_lock has no
-- native 32-bit single-key overload; a hash collision between two
-- tenants only costs extra serialization, never a correctness bug.
SELECT pg_advisory_xact_lock(hashtext(@tenant_id::text)::bigint);
