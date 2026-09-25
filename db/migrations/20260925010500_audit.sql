-- +goose Up
-- tenant_id is nullable here (unlike every business table) because the
-- audit log also records pre-tenant and account-level security events
-- (e.g. a failed login for an email that resolves to no user, or a login
-- before a tenant is chosen); every tenant-scoped action still sets it.
--
-- tenant_id/actor_user_id are plain uuid columns, deliberately NOT foreign
-- keys to tenants/users: audit rows must outlive the entities they
-- reference. A FK's ON DELETE SET NULL/CASCADE action runs as an UPDATE
-- (or DELETE) against audit_log, which the immutability trigger below
-- correctly refuses (verified: it fires even when zero rows would
-- actually change, since the trigger is statement-level) — so any FK here
-- would make it impossible to ever delete a tenant or user. actor_email
-- denormalizes the actor's email at write time so an entry stays readable
-- after the user row itself is gone.
CREATE TABLE audit_log (
    id uuid PRIMARY KEY,
    tenant_id uuid,
    actor_user_id uuid,
    actor_email text,
    action text NOT NULL,
    target_type text,
    target_id text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip inet,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_tenant_id_id_idx ON audit_log (tenant_id, id);
CREATE INDEX audit_log_actor_user_id_idx ON audit_log (actor_user_id);

-- +goose StatementBegin
CREATE FUNCTION audit_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only: % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- A BEFORE trigger fires for every role including the table owner, so a
-- direct UPDATE/DELETE/TRUNCATE by loomtale_owner is blocked exactly like
-- loomtale_app's. It does not stop loomtale_owner from first running
-- ALTER TABLE ... DISABLE TRIGGER (table ownership always carries that
-- right in Postgres); the mitigation for that is credential separation,
-- not the trigger itself — nothing routine holds owner credentials except
-- the migrate step, and the backup service connects as the read-only
-- loomtale_backup role instead (see the db_roles migration).
CREATE TRIGGER audit_log_immutable_trigger
    BEFORE UPDATE OR DELETE OR TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_immutable();

-- loomtale_app's default grant (from the db_roles migration) included
-- UPDATE/DELETE; narrow it back down to INSERT/SELECT only for this table.
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM loomtale_app;

-- +goose Down
-- Intentionally refuses: audit history must never be dropped by a routine
-- rollback. Restore audit_log from a backup if this migration must be
-- reverted.
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'the audit_log migration refuses to roll back by design; restore from backup instead';
END
$$;
-- +goose StatementEnd
