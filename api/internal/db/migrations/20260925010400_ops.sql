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

-- +goose Down
DROP TABLE backup_runs;
