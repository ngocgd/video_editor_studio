-- +goose Up
-- Free-form, caller-supplied parameters for a step's async Run, set once
-- at enqueue time and never mutated afterward (unlike `output`, which the
-- handler itself writes). River job args stay `{StepIDs}` only, so
-- anything a handler needs beyond its own scope_kind/scope_id (e.g. an
-- AI action's selected paragraph ids and the user's free-text
-- instruction) has to live here instead.
ALTER TABLE pipeline_steps ADD COLUMN input jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE pipeline_steps DROP COLUMN input;
