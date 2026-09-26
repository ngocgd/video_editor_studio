-- +goose Up
-- Seed the local Ollama LLM as the default provider of every tenant that
-- has never chosen one (no llm_settings row, so it runs on the
-- deployment's fallback default). Tenants with an explicit choice keep
-- it. Which Ollama model answers is deployment config
-- (LOOMTALE_OLLAMA_MODEL), not a per-tenant setting, so only the provider
-- name is written here.
--
-- The seed only happens when one of the manifest's local LLM candidates
-- (models/manifest.yaml, engine ollama) is installed: a deployment without
-- local LLM weights keeps its fallback default instead of being switched
-- to a provider that cannot answer. Re-running the section is a no-op for
-- tenants that already have a row.
--
-- llm_default_seeds records which tenants this seed switched and when, so
-- the down migration can restore exactly those tenants to the fallback
-- default without touching a choice a tenant made afterwards.
CREATE TABLE IF NOT EXISTS llm_default_seeds (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    seeded_at timestamptz NOT NULL
);

WITH seeded AS (
    INSERT INTO llm_settings (tenant_id, default_provider, created_at, updated_at)
    SELECT t.id, 'ollama', now(), now()
    FROM tenants t
    WHERE NOT EXISTS (SELECT 1 FROM llm_settings s WHERE s.tenant_id = t.id)
      AND EXISTS (
          SELECT 1 FROM model_installs m
          WHERE m.status = 'installed'
            AND m.name IN ('qwen3.5-9b', 'gemma-4-12b')
      )
    ON CONFLICT (tenant_id) DO NOTHING
    RETURNING tenant_id, updated_at
)
INSERT INTO llm_default_seeds (tenant_id, seeded_at)
SELECT tenant_id, updated_at FROM seeded
ON CONFLICT (tenant_id) DO NOTHING;

-- +goose Down
-- Put seeded tenants back on the fallback default, unless they changed
-- their LLM settings after the seed (updated_at moved on).
DELETE FROM llm_settings s
USING llm_default_seeds d
WHERE s.tenant_id = d.tenant_id
  AND s.default_provider = 'ollama'
  AND s.updated_at = d.seeded_at;

DROP TABLE IF EXISTS llm_default_seeds;
