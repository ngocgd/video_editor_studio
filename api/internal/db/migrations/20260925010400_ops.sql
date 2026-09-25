-- +goose Up
-- One row per nightly backup attempt; /readyz reports staleness from this.
CREATE TABLE backup_runs (
    id uuid PRIMARY KEY,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    status text NOT NULL CHECK (status IN ('running', 'success', 'failed')),
    detail text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX backup_runs_started_at_idx ON backup_runs (started_at DESC);

-- loomtale_backup is otherwise SELECT-only (db_roles migration), but it is
-- the one role that ever runs deploy/backup/backup.sh, which needs to
-- record its own run's outcome. This is the single, narrow write
-- exception, scoped to exactly this table.
GRANT INSERT, UPDATE ON backup_runs TO loomtale_backup;

-- +goose Down
REVOKE INSERT, UPDATE ON backup_runs FROM loomtale_backup;
DROP TABLE backup_runs;
