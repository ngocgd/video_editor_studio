-- name: UpsertWorkerStatus :exec
INSERT INTO worker_status (worker_id, gpu, resident_ref, providers)
VALUES (@worker_id, @gpu, @resident_ref, @providers)
ON CONFLICT (worker_id) DO UPDATE SET
    gpu = EXCLUDED.gpu,
    resident_ref = EXCLUDED.resident_ref,
    providers = EXCLUDED.providers,
    updated_at = now();

-- name: GetLatestWorkerStatus :one
SELECT * FROM worker_status ORDER BY updated_at DESC LIMIT 1;
