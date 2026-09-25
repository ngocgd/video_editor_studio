-- +goose Up
-- The application roles (loomtale_owner, loomtale_app, loomtale_backup)
-- are created by deploy/postgres/init-roles.sh at container init time
-- from Docker secrets, so their passwords never appear in a committed
-- migration. This migration only grants the privilege split: the owner
-- role gets full DDL rights on the schema (it runs every later
-- migration), the app role gets DML rights on tables the owner creates
-- from now on and no DDL rights at all, and the backup role gets
-- SELECT-only rights (it never needs to write, and never needs DDL) so
-- the nightly backup sidecar does not have to hold owner credentials —
-- which would otherwise be able to ALTER TABLE ... DISABLE TRIGGER on
-- audit_log. Grants are idempotent so `goose up` stays replayable in dev.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_owner') THEN
        RAISE EXCEPTION 'role loomtale_owner is missing; deploy/postgres/init-roles.sh must run before migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_app') THEN
        RAISE EXCEPTION 'role loomtale_app is missing; deploy/postgres/init-roles.sh must run before migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_backup') THEN
        RAISE EXCEPTION 'role loomtale_backup is missing; deploy/postgres/init-roles.sh must run before migrations';
    END IF;
END
$$;
-- +goose StatementEnd

GRANT ALL ON SCHEMA public TO loomtale_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO loomtale_app;
GRANT USAGE ON SCHEMA public TO loomtale_backup;

-- Every table/sequence the owner creates from here on automatically grants
-- CRUD to the app role and SELECT to the backup role, so individual
-- migrations never need a matching GRANT statement. audit_log overrides
-- the app grant with an explicit REVOKE of UPDATE/DELETE/TRUNCATE in its
-- own migration; loomtale_backup's SELECT-only grant already excludes
-- those, so audit_log needs no matching override for it.
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO loomtale_app;
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO loomtale_app;
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    GRANT SELECT ON TABLES TO loomtale_backup;
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    GRANT SELECT ON SEQUENCES TO loomtale_backup;

-- goose_db_version (and its serial column's sequence) predates this
-- migration (goose creates it on its own first connection, before the
-- baseline migration even runs), so the default-privilege grants above —
-- which only apply to objects created AFTER this point — never cover it.
-- pg_dump takes an ACCESS SHARE lock on every table and reads every
-- sequence's value, goose_db_version included, so loomtale_backup needs
-- an explicit grant on both pre-existing objects.
GRANT SELECT ON goose_db_version TO loomtale_backup;
GRANT SELECT ON goose_db_version_id_seq TO loomtale_backup;

-- +goose Down
REVOKE SELECT ON goose_db_version_id_seq FROM loomtale_backup;
REVOKE SELECT ON goose_db_version FROM loomtale_backup;
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    REVOKE SELECT ON TABLES FROM loomtale_backup;
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    REVOKE SELECT ON SEQUENCES FROM loomtale_backup;
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM loomtale_app;
ALTER DEFAULT PRIVILEGES FOR ROLE loomtale_owner IN SCHEMA public
    REVOKE USAGE, SELECT ON SEQUENCES FROM loomtale_app;
REVOKE USAGE ON SCHEMA public FROM loomtale_app;
REVOKE USAGE ON SCHEMA public FROM loomtale_backup;
