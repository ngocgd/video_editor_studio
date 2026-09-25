-- +goose Up
-- tenant_id is nullable here (unlike every business table) because the
-- audit log also records pre-tenant and account-level security events
-- (e.g. a failed login for an email that resolves to no user, or a login
-- before a tenant is chosen); every tenant-scoped action still sets it.
CREATE TABLE audit_log (
    id uuid PRIMARY KEY,
    tenant_id uuid REFERENCES tenants (id) ON DELETE SET NULL,
    actor_user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    action text NOT NULL,
    target_type text,
    target_id text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip inet,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_tenant_id_id_idx ON audit_log (tenant_id, id);

-- +goose StatementBegin
CREATE FUNCTION audit_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only: % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- A BEFORE trigger fires for every role including the table owner, so this
-- also blocks loomtale_owner from mutating history, not just loomtale_app.
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
