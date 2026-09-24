-- +goose Up
-- goose manages this table itself at runtime; it is declared here only so
-- sqlc can resolve the placeholder query in db/queries/health.sql against a
-- real schema, proving the sqlc -> pgx/v5 pipeline end to end.
CREATE TABLE IF NOT EXISTS goose_db_version (
    id serial NOT NULL,
    version_id bigint NOT NULL,
    is_applied boolean NOT NULL,
    tstamp timestamp NOT NULL DEFAULT now(),
    PRIMARY KEY (id)
);

-- +goose Down
DROP TABLE IF EXISTS goose_db_version;
