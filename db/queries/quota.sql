-- name: ReserveQuotaUnits :one
-- Atomically adds units to today's bucket only if the total stays within
-- daily_limit. No row returned means the reservation would exceed the
-- limit and nothing was recorded.
INSERT INTO quota_ledger (project, pt_date, bucket, units)
SELECT @project::text, @pt_date::date, @bucket::text, @units::int
WHERE @units::int <= @daily_limit::int
ON CONFLICT (project, pt_date, bucket) DO UPDATE SET
    units = quota_ledger.units + EXCLUDED.units,
    updated_at = now()
WHERE quota_ledger.units + EXCLUDED.units <= @daily_limit::int
RETURNING units;

-- name: MarkQuotaExhausted :exec
-- Google answered quotaExceeded: raise the bucket to at least daily_limit
-- so every later pre-check for the same day fails without calling Google.
INSERT INTO quota_ledger (project, pt_date, bucket, units)
VALUES (@project::text, @pt_date::date, @bucket::text, @daily_limit::int)
ON CONFLICT (project, pt_date, bucket) DO UPDATE SET
    units = GREATEST(quota_ledger.units, EXCLUDED.units),
    updated_at = now();

-- name: GetQuotaUnits :one
SELECT COALESCE(
    (SELECT units FROM quota_ledger
     WHERE project = @project::text AND pt_date = @pt_date::date AND bucket = @bucket::text),
    0)::int AS units;
