-- +goose Up
-- Story rows must point at a parent in their own tenant. The single-column
-- foreign keys only prove the parent exists somewhere, so a row of tenant B
-- could reference tenant A's series or episode. These composite keys make
-- the database enforce the same tenant on both sides.
ALTER TABLE series ADD CONSTRAINT series_tenant_id_id_key UNIQUE (tenant_id, id);
ALTER TABLE episodes ADD CONSTRAINT episodes_tenant_id_id_key UNIQUE (tenant_id, id);
ALTER TABLE episode_drafts ADD CONSTRAINT episode_drafts_tenant_id_id_key UNIQUE (tenant_id, id);

-- The unique constraints above cover these lookups.
DROP INDEX series_tenant_id_id_idx;
DROP INDEX episodes_tenant_id_id_idx;

ALTER TABLE story_bibles ADD CONSTRAINT story_bibles_tenant_series_fkey
    FOREIGN KEY (tenant_id, series_id) REFERENCES series (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE episodes ADD CONSTRAINT episodes_tenant_series_fkey
    FOREIGN KEY (tenant_id, series_id) REFERENCES series (tenant_id, id) ON DELETE CASCADE;
-- series_id is nullable on imports; MATCH SIMPLE skips the check while it is null.
ALTER TABLE imports ADD CONSTRAINT imports_tenant_series_fkey
    FOREIGN KEY (tenant_id, series_id) REFERENCES series (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE episode_drafts ADD CONSTRAINT episode_drafts_tenant_episode_fkey
    FOREIGN KEY (tenant_id, episode_id) REFERENCES episodes (tenant_id, id) ON DELETE CASCADE;
ALTER TABLE episode_draft_revisions ADD CONSTRAINT episode_draft_revisions_tenant_draft_fkey
    FOREIGN KEY (tenant_id, draft_id) REFERENCES episode_drafts (tenant_id, id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE episode_draft_revisions DROP CONSTRAINT episode_draft_revisions_tenant_draft_fkey;
ALTER TABLE episode_drafts DROP CONSTRAINT episode_drafts_tenant_episode_fkey;
ALTER TABLE imports DROP CONSTRAINT imports_tenant_series_fkey;
ALTER TABLE episodes DROP CONSTRAINT episodes_tenant_series_fkey;
ALTER TABLE story_bibles DROP CONSTRAINT story_bibles_tenant_series_fkey;

CREATE INDEX episodes_tenant_id_id_idx ON episodes (tenant_id, id);
CREATE INDEX series_tenant_id_id_idx ON series (tenant_id, id);

ALTER TABLE episode_drafts DROP CONSTRAINT episode_drafts_tenant_id_id_key;
ALTER TABLE episodes DROP CONSTRAINT episodes_tenant_id_id_key;
ALTER TABLE series DROP CONSTRAINT series_tenant_id_id_key;
