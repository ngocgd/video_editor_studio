-- name: RefillRateLimitBucket :one
-- Upserts a token bucket, refilling it by elapsed time since its last
-- update, and returns the refilled token count. Deliberately a separate
-- statement from the conditional decrement in ConsumeRateLimitBucket: a
-- data-modifying CTE and a second statement/CTE both targeting the same
-- table within one query execute against the same MVCC snapshot (see
-- "WITH Queries" in the Postgres docs), so a brand-new bucket's insert is
-- never visible to a sibling write in that same statement. Two
-- round-trip statements sidestep that entirely, at the cost of a small
-- race window under heavy concurrent load on the same key, which a login
-- rate limiter does not need to close precisely.
INSERT INTO rate_limit_buckets (bucket_key, tokens, updated_at)
VALUES (@bucket_key, @capacity, now())
ON CONFLICT (bucket_key) DO UPDATE SET
    tokens = LEAST(
        @capacity::real,
        rate_limit_buckets.tokens
            + EXTRACT(EPOCH FROM (now() - rate_limit_buckets.updated_at)) * @refill_per_second::real
    ),
    updated_at = now()
RETURNING tokens;

-- name: ConsumeRateLimitBucket :execrows
-- Consumes one token if at least one is available; the caller checks the
-- returned row count (1 = allowed, 0 = the bucket was already empty).
UPDATE rate_limit_buckets
SET tokens = tokens - 1
WHERE bucket_key = @bucket_key AND tokens >= 1;

-- name: CreateBackupRun :one
INSERT INTO backup_runs (id, started_at, status)
VALUES (@id, @started_at, 'running')
RETURNING *;

-- name: CompleteBackupRun :exec
UPDATE backup_runs SET status = @status, detail = @detail, finished_at = @finished_at
WHERE id = @id;

-- name: GetLatestBackupRun :one
SELECT * FROM backup_runs ORDER BY started_at DESC LIMIT 1;
