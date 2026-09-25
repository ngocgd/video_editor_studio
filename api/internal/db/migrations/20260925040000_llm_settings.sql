-- +goose Up
-- Per-tenant LLM provider selection: a default provider plus optional
-- per-action overrides (outline, draft, rewrite, translate, scene_split,
-- summary). API keys for anthropic-api/gemini-api are stored separately
-- in the existing envelope-encrypted secrets table (kind='llm_api_key',
-- owner_ref=provider name); this table only ever holds provider *names*.
CREATE TABLE llm_settings (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    default_provider text NOT NULL,
    action_overrides jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE llm_settings;
