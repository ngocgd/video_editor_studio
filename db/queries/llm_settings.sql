-- name: GetLLMSettings :one
SELECT * FROM llm_settings WHERE tenant_id = @tenant_id;

-- name: UpsertLLMSettings :one
INSERT INTO llm_settings (tenant_id, default_provider, action_overrides)
VALUES (@tenant_id, @default_provider, @action_overrides)
ON CONFLICT (tenant_id) DO UPDATE SET
    default_provider = EXCLUDED.default_provider,
    action_overrides = EXCLUDED.action_overrides,
    updated_at = now()
RETURNING *;
