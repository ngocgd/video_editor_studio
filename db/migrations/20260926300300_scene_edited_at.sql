-- +goose Up
-- When a person last edited a scene (narration, speakers, prompt,
-- characters, motion or style). A re-split refuses to delete an edited
-- scene, or one with takes, unless the request confirms it.
ALTER TABLE scenes ADD COLUMN edited_at timestamptz;

-- +goose Down
ALTER TABLE scenes DROP COLUMN edited_at;
