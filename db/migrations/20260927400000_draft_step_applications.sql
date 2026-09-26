-- +goose Up
-- One row per AI step whose result was applied to a draft. Applying is
-- claimed here in the same transaction as the draft write, so accepting
-- the same inserting step twice (a double Tab, a retried POST) cannot
-- insert its text twice: the second claim finds the row and is refused.
CREATE TABLE draft_step_applications (
    step_id uuid PRIMARY KEY REFERENCES pipeline_steps (id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    draft_id uuid NOT NULL,
    draft_version bigint NOT NULL,
    applied_by uuid REFERENCES users (id) ON DELETE SET NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, draft_id) REFERENCES episode_drafts (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX draft_step_applications_tenant_draft_idx
    ON draft_step_applications (tenant_id, draft_id);

-- +goose Down
DROP TABLE draft_step_applications;
