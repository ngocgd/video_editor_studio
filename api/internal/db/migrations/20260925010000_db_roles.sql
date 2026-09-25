-- +goose Up
-- The two application roles (loomtale_owner, loomtale_app) are created by
-- deploy/postgres/init-roles.sh at container init time from Docker secrets,
-- so their passwords never appear in a committed migration. This migration
-- only grants the privilege split: the owner role gets full DDL rights on
-- the schema (it runs every later migration), the app role gets DML rights
-- on tables the owner creates from now on, and no DDL rights at all. Grants
-- are idempotent so `goose up` stays replayable in dev.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_owner') THEN
        RAISE EXCEPTION 'role loomtale_owner is missing; deploy/postgres/init-roles.sh must run before migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_app') THEN
        RAISE EXCEPTION 'role loomtale_app is missing; deploy/postgres/init-roles.sh must run before migrations';
    END IF;
END
$$;
-- +goose StatementEnd

GRANT ALL ON SCHEMA public TO loomtale_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO loomtale_app;

-- Every table/sequence the owner creates from here on automatically grants
-- CRUD to the app role, so individual migrations never need a matching
-- GRANT statement. audit_log overrides this with an explicit REVOKE of
-- UPDATE/DELETE/TRUNCATE in its own migration.
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO loomtale_app;
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO loomtale_app;

-- +goose Down
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM loomtale_app;
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    REVOKE USAGE, SELECT ON SEQUENCES FROM loomtale_app;
REVOKE USAGE ON SCHEMA public FROM loomtale_app;
