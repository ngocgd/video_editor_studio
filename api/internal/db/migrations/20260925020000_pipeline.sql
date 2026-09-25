-- +goose Up

-- A pipeline run groups steps that build one deliverable (an episode, a
-- scene, a training job). scope_kind/scope_id is polymorphic with no FK:
-- episodes and scenes are added by later phases, so this table cannot
-- reference tables that do not exist yet. Domain delete services call
-- pipeline.CancelScope inside their own transaction instead.
CREATE TABLE pipeline_runs (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    scope_kind text NOT NULL,
    scope_id uuid NOT NULL,
    kind text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'done', 'failed', 'canceled', 'superseded')),
    superseded_by uuid REFERENCES pipeline_runs (id) ON DELETE SET NULL,
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pipeline_runs_tenant_scope_idx ON pipeline_runs (tenant_id, scope_kind, scope_id);

-- One row per unit of pipeline work. River job args carry only step ids
-- (see api/internal/pipeline/claim.go); every other field, including the
-- resolved queue and provider_ref, lives here so a settings change after
-- enqueue never moves an already-queued step. `attempt` plus `status`
-- together are the only fence a handler trusts before writing output.
CREATE TABLE pipeline_steps (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    run_id uuid NOT NULL REFERENCES pipeline_runs (id) ON DELETE CASCADE,
    scope_kind text NOT NULL,
    scope_id uuid NOT NULL,
    kind text NOT NULL,
    queue text NOT NULL CHECK (queue IN ('gpu', 'cpu', 'llm', 'render', 'io')),
    provider_ref text NOT NULL DEFAULT '',
    priority smallint NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'queued', 'running', 'done', 'failed', 'canceled')),
    attempt integer NOT NULL DEFAULT 0,
    -- Bumped on every state or progress change; SSE events and clients use
    -- it to discard events older than the snapshot they already fetched.
    version bigint NOT NULL DEFAULT 0,
    remaining_deps integer NOT NULL DEFAULT 0,
    claimed_job_id bigint,
    input_hash text NOT NULL DEFAULT '',
    progress smallint NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    eta_s integer,
    output jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_code text,
    error_msg text,
    log_asset_id uuid REFERENCES assets (id) ON DELETE SET NULL,
    heartbeat_at timestamptz,
    started_at timestamptz,
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pipeline_steps_tenant_scope_idx ON pipeline_steps (tenant_id, scope_kind, scope_id, kind);
CREATE INDEX pipeline_steps_run_status_idx ON pipeline_steps (run_id, status);
CREATE INDEX pipeline_steps_live_idx ON pipeline_steps (status) WHERE status IN ('queued', 'running');
CREATE INDEX pipeline_steps_ready_idx ON pipeline_steps (id) WHERE status = 'pending' AND remaining_deps = 0;
-- GET /jobs and the GPU queue view both page by (tenant, queue, status, id).
CREATE INDEX pipeline_steps_tenant_queue_status_idx ON pipeline_steps (tenant_id, queue, status, id);

-- tenant_id is redundant with a join through pipeline_steps but is kept as
-- its own column so fan-in and the tenant-query lint stay simple and every
-- write is a single-table statement.
CREATE TABLE pipeline_step_deps (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    step_id uuid NOT NULL REFERENCES pipeline_steps (id) ON DELETE CASCADE,
    depends_on_step_id uuid NOT NULL REFERENCES pipeline_steps (id) ON DELETE CASCADE,
    PRIMARY KEY (step_id, depends_on_step_id)
);
CREATE INDEX pipeline_step_deps_depends_on_idx ON pipeline_step_deps (depends_on_step_id);

-- tenant_quotas already exists (core_tenancy migration); this adds the
-- pipeline admission limit read by api/internal/quota.Check. NULL (the
-- local/default case) means unlimited; a non-null value caps the number
-- of concurrently queued+running steps, enforced before pipeline.Enqueue
-- inserts anything.
ALTER TABLE tenant_quotas ADD COLUMN max_active_steps integer;

-- +goose Down
ALTER TABLE tenant_quotas DROP COLUMN max_active_steps;
DROP TABLE pipeline_step_deps;
DROP TABLE pipeline_steps;
DROP TABLE pipeline_runs;
