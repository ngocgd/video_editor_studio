-- name: InsertAuditLog :exec
INSERT INTO audit_log (id, tenant_id, actor_user_id, actor_email, action, target_type, target_id, metadata, ip, user_agent)
VALUES (@id, @tenant_id, @actor_user_id, @actor_email, @action, @target_type, @target_id, @metadata, @ip, @user_agent);

-- name: ListAuditLog :many
SELECT * FROM audit_log
WHERE tenant_id = @tenant_id AND id > @cursor
ORDER BY id
LIMIT @page_limit;
