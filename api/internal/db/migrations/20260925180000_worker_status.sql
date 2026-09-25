-- +goose Up
-- Cross-process status channel: the worker (the only process that can
-- reach the GPU-backed backends, gpu_net being internal) upserts its own
-- row every 5s and immediately after any residency change; the api
-- process reads the freshest row for /gpu and provider availability
-- instead of holding a live network path it architecturally cannot have.
-- Not tenant-scoped: there is one physical GPU and one active worker
-- process for this deployment shape.
CREATE TABLE worker_status (
    worker_id text PRIMARY KEY,
    gpu jsonb NOT NULL DEFAULT '{}'::jsonb,
    resident_ref text,
    providers jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE worker_status;
