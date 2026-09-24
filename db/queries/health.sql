-- name: GetSchemaVersion :one
-- Placeholder query proving the sqlc -> pgx/v5 pipeline against goose's own
-- version table. Later phases add domain queries here and in sibling files.
SELECT version_id, is_applied, tstamp
FROM goose_db_version
ORDER BY id DESC
LIMIT 1;
