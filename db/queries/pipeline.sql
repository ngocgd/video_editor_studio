-- name: CreateRun :one
INSERT INTO pipeline_runs (id, tenant_id, scope_kind, scope_id, kind, created_by)
VALUES (@id, @tenant_id, @scope_kind, @scope_id, @kind, @created_by)
RETURNING *;

-- name: GetRun :one
SELECT * FROM pipeline_runs WHERE tenant_id = @tenant_id AND id = @id;

-- name: MarkRunStatus :exec
UPDATE pipeline_runs SET status = @status, updated_at = now() WHERE tenant_id = @tenant_id AND id = @id;

-- name: SupersedeRun :exec
UPDATE pipeline_runs SET status = 'superseded', superseded_by = @new_run_id, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @old_run_id;

-- name: InsertStep :one
INSERT INTO pipeline_steps (
    id, tenant_id, run_id, scope_kind, scope_id, kind, queue, provider_ref,
    priority, status, remaining_deps, input_hash
) VALUES (
    @id, @tenant_id, @run_id, @scope_kind, @scope_id, @kind, @queue, @provider_ref,
    @priority, @status, @remaining_deps, @input_hash
)
RETURNING *;

-- name: InsertStepDep :exec
INSERT INTO pipeline_step_deps (tenant_id, step_id, depends_on_step_id)
VALUES (@tenant_id, @step_id, @depends_on_step_id);

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
SELECT * FROM pipeline_steps
WHERE tenant_id = @tenant_id
  AND (@status_filter::text = '' OR status = @status_filter)
  AND (@queue_filter::text = '' OR queue = @queue_filter)
  AND id > @cursor
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

-- name: RequeueStep :one
-- lint-tenant-queries:allow: internal retry-requeue fenced by id+attempt, not caller input
UPDATE pipeline_steps
SET status = 'queued', version = version + 1
WHERE id = @id AND attempt = @attempt AND status = 'running'
RETURNING *;

-- name: ResetStaleHeartbeats :many
-- lint-tenant-queries:allow: system-wide reconciler sweep, not scoped to a caller's tenant
UPDATE pipeline_steps
SET status = 'queued', version = version + 1
WHERE status = 'running' AND heartbeat_at < @cutoff
RETURNING *;

-- name: ReadySweep :many
-- lint-tenant-queries:allow: system-wide reconciler sweep, not scoped to a caller's tenant
UPDATE pipeline_steps
SET status = 'queued', version = version + 1
WHERE status = 'pending' AND remaining_deps <= 0
RETURNING *;

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

-- name: GetDependents :many
SELECT step_id FROM pipeline_step_deps
WHERE tenant_id = @tenant_id AND depends_on_step_id = ANY(@ids::uuid[]);

-- name: RecomputeRemainingDeps :one
UPDATE pipeline_steps s
SET remaining_deps = (
    SELECT count(*) FROM pipeline_step_deps d
    JOIN pipeline_steps dep ON dep.id = d.depends_on_step_id
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
UPDATE pipeline_steps
SET status = 'queued', error_code = NULL, error_msg = NULL, version = version + 1
WHERE tenant_id = @tenant_id AND id = @id AND status IN ('failed', 'canceled')
RETURNING *;

-- name: CancelRunSteps :many
UPDATE pipeline_steps
SET status = 'canceled', version = version + 1, finished_at = now()
WHERE tenant_id = @tenant_id AND run_id = @run_id AND status IN ('pending', 'queued', 'running')
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
SELECT count(*) FROM pipeline_steps WHERE tenant_id = @tenant_id AND status IN ('queued', 'running');

-- name: GetTenantQuota :one
SELECT * FROM tenant_quotas WHERE tenant_id = @tenant_id;
