-- Model installs, verified files and benchmarks. Not tenant-scoped (see
-- the models migration): one GPU and one models volume per deployment.

-- name: ClaimModelInstall :one
-- Moves a model into "downloading" unless a pull is already running for
-- it. Returns no row when one is, so the caller can answer 409 instead
-- of starting a second concurrent download into the same files.
INSERT INTO model_installs (name, status, bytes_done, bytes_total, started_by_tenant_id, licence_spdx, licence_url, licence_checked_at, revision, error)
VALUES (@name, 'downloading', @bytes_done, @bytes_total, @started_by_tenant_id, @licence_spdx, @licence_url, now(), @revision, NULL)
ON CONFLICT (name) DO UPDATE SET
    status = 'downloading',
    bytes_done = EXCLUDED.bytes_done,
    bytes_total = EXCLUDED.bytes_total,
    started_by_tenant_id = EXCLUDED.started_by_tenant_id,
    licence_spdx = EXCLUDED.licence_spdx,
    licence_url = EXCLUDED.licence_url,
    licence_checked_at = now(),
    revision = EXCLUDED.revision,
    run_id = NULL,
    step_id = NULL,
    error = NULL,
    updated_at = now()
WHERE model_installs.status <> 'downloading'
RETURNING *;

-- name: SetModelInstallStep :exec
UPDATE model_installs SET run_id = @run_id, step_id = @step_id, updated_at = now()
WHERE name = @name AND status = 'downloading';

-- name: UpdateModelInstallProgress :exec
UPDATE model_installs SET bytes_done = @bytes_done, bytes_total = @bytes_total, updated_at = now()
WHERE name = @name AND status = 'downloading';

-- name: MarkModelInstalled :exec
-- Unconditional on purpose: the files are verified on disk, which is the
-- fact this row reports, whatever state a concurrent pause left it in.
INSERT INTO model_installs (name, status, bytes_done, bytes_total, licence_spdx, licence_url, licence_checked_at, revision, installed_at)
VALUES (@name, 'installed', @bytes_total, @bytes_total, @licence_spdx, @licence_url, now(), @revision, now())
ON CONFLICT (name) DO UPDATE SET
    status = 'installed',
    bytes_done = EXCLUDED.bytes_total,
    bytes_total = EXCLUDED.bytes_total,
    licence_spdx = EXCLUDED.licence_spdx,
    licence_url = EXCLUDED.licence_url,
    revision = EXCLUDED.revision,
    error = NULL,
    installed_at = now(),
    updated_at = now();

-- name: MarkModelInstallFailed :exec
UPDATE model_installs SET status = 'failed', error = @error, updated_at = now()
WHERE name = @name AND status = 'downloading';

-- name: PauseModelInstall :one
UPDATE model_installs SET status = 'paused', updated_at = now()
WHERE name = @name AND status = 'downloading'
RETURNING *;

-- name: GetModelInstall :one
SELECT * FROM model_installs WHERE name = @name;

-- name: ListModelInstalls :many
SELECT * FROM model_installs ORDER BY name;

-- name: DeleteModelInstall :exec
DELETE FROM model_installs WHERE name = @name;

-- name: UpsertModelFile :exec
INSERT INTO model_files (path, digest, size_bytes, verified_at)
VALUES (@path, @digest, @size_bytes, now())
ON CONFLICT (path) DO UPDATE SET
    digest = EXCLUDED.digest,
    size_bytes = EXCLUDED.size_bytes,
    verified_at = now();

-- name: ListModelFiles :many
SELECT * FROM model_files ORDER BY path;

-- name: DeleteModelFile :exec
DELETE FROM model_files WHERE path = @path;

-- name: InsertModelBenchmark :exec
INSERT INTO model_benchmarks (id, run_id, suite, case_name, model, ok, seconds, vram_peak_mb, rss_peak_mb, switch_seconds, error, meta)
VALUES (@id, @run_id, @suite, @case_name, @model, @ok, @seconds, @vram_peak_mb, @rss_peak_mb, @switch_seconds, @error, @meta);

-- name: ListModelBenchmarksByRun :many
SELECT * FROM model_benchmarks WHERE run_id = @run_id ORDER BY created_at;
