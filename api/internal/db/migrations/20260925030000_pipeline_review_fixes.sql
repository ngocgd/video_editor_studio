-- +goose Up

-- Per-filter indexes for GET /jobs: the original single query OR'd an
-- optional status filter and an optional queue filter in the WHERE
-- clause, which the planner cannot turn into an index scan for any
-- filter combination (it can only ever plan for the union of both
-- branches, i.e. a full scan). ListJobs is now split into one sqlc query
-- per filter combination, each matched to one of these.
CREATE INDEX pipeline_steps_tenant_id_idx ON pipeline_steps (tenant_id, id);
CREATE INDEX pipeline_steps_tenant_status_id_idx ON pipeline_steps (tenant_id, status, id);
CREATE INDEX pipeline_steps_tenant_queue_id_idx ON pipeline_steps (tenant_id, queue, id);

-- Bounds how many times the reconciler will re-enqueue a step it finds
-- "queued" with no corresponding live River job (a step that fell out of
-- River's own bookkeeping, e.g. a job discarded after exhausting its own
-- retries before the step's own last-attempt commit could mark it
-- failed). Once this exceeds a small budget the reconciler fails the
-- step outright instead of looping forever.
ALTER TABLE pipeline_steps ADD COLUMN stranded_requeues integer NOT NULL DEFAULT 0;

-- Counts gpu_oom occurrences for a step, durably (a retry can land on any
-- worker process, so an in-memory counter cannot bound this). Reset to 0
-- whenever a step is freshly claimed with a lower attempt would not be
-- correct either, since the whole point is to survive across attempts;
-- it is only ever reset by starting an entirely new run (manual retry).
ALTER TABLE pipeline_steps ADD COLUMN gpu_oom_count integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE pipeline_steps DROP COLUMN gpu_oom_count;
ALTER TABLE pipeline_steps DROP COLUMN stranded_requeues;
DROP INDEX pipeline_steps_tenant_queue_id_idx;
DROP INDEX pipeline_steps_tenant_status_id_idx;
DROP INDEX pipeline_steps_tenant_id_idx;
