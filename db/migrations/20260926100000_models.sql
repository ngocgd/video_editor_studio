-- +goose Up
-- Local model installs, verified model files and benchmark results.
-- None of these are tenant-scoped: there is one GPU and one models
-- volume per deployment, so an installed model is shared by every
-- tenant. Only owners can change them (see openapi/paths/models.yaml).

-- One row per manifest entry that has ever been installed or queued for
-- install. The manifest (models/manifest.yaml) stays the source of truth
-- for what a model is; this table only records its local install state.
CREATE TABLE model_installs (
    name text PRIMARY KEY,
    status text NOT NULL
        CHECK (status IN ('downloading', 'paused', 'installed', 'failed')),
    bytes_done bigint NOT NULL DEFAULT 0 CHECK (bytes_done >= 0),
    bytes_total bigint NOT NULL DEFAULT 0 CHECK (bytes_total >= 0),
    -- The pull run/step currently responsible for this install, and the
    -- tenant it was enqueued under (pipeline rows are tenant-scoped, so a
    -- pause has to cancel the step inside that tenant).
    run_id uuid,
    step_id uuid,
    started_by_tenant_id uuid,
    error text,
    -- Licence facts copied from the manifest when the licence gate
    -- passed, so the UI shows what was accepted at install time even if
    -- the manifest entry changes later.
    licence_spdx text NOT NULL,
    licence_url text NOT NULL,
    licence_checked_at timestamptz NOT NULL DEFAULT now(),
    revision text NOT NULL,
    installed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- One row per file on the models volume whose sha256 has been verified
-- against the manifest. Engine load checks presence and size against
-- this table instead of re-hashing tens of gigabytes every time.
CREATE TABLE model_files (
    path text PRIMARY KEY,
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    verified_at timestamptz NOT NULL DEFAULT now()
);

-- Benchmark rows written by `loomtale bench`. Each row is one case (one
-- image) of one suite run; run_id groups the cases of a single run.
CREATE TABLE model_benchmarks (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL,
    suite text NOT NULL,
    case_name text NOT NULL,
    model text NOT NULL,
    ok boolean NOT NULL,
    seconds double precision,
    vram_peak_mb bigint,
    rss_peak_mb bigint,
    switch_seconds double precision,
    error text,
    meta jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX model_benchmarks_suite_idx ON model_benchmarks (suite, created_at DESC);

-- +goose Down
DROP TABLE model_benchmarks;
DROP TABLE model_files;
DROP TABLE model_installs;
