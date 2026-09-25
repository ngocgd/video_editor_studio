-- name: CreateTenant :one
INSERT INTO tenants (id, name)
VALUES (@id, @name)
RETURNING *;

-- name: GetTenantByID :one
SELECT * FROM tenants WHERE id = @id;

-- name: CreateMembership :one
INSERT INTO memberships (tenant_id, user_id, role)
VALUES (@tenant_id, @user_id, @role)
RETURNING *;

-- name: GetMembership :one
-- Every cross-tenant lookup goes through this query so an attacker probing
-- another tenant's resources gets the same "not found" as a real 404.
SELECT * FROM memberships WHERE tenant_id = @tenant_id AND user_id = @user_id;

-- name: ListMembershipsForUser :many
-- lint-tenant-queries:allow: by design this lists every tenant the user
-- belongs to (e.g. to populate the tenant switcher); it is scoped by
-- user_id, not tenant_id, because no single tenant is selected yet.
SELECT m.tenant_id, m.role, t.name AS tenant_name
FROM memberships m
JOIN tenants t ON t.id = m.tenant_id
WHERE m.user_id = @user_id
ORDER BY t.name;
