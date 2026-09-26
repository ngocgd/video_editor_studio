-- +goose Up
-- Import keeps the source chapter as its own draft rather than mislabeling
-- it "en": a Chinese-language source is stored under lang='zh' so the word
-- count/duration estimate and the writer's language toggle reflect what was
-- actually imported. AI-action targets and the writer's translate toggle
-- still only offer en/vi (see openapi's TargetLanguage); zh is reachable
-- only as the read/patch/put lang path param (DraftLanguage) and as
-- CommitImport's own source draft.
ALTER TABLE episode_drafts DROP CONSTRAINT episode_drafts_lang_check;
ALTER TABLE episode_drafts ADD CONSTRAINT episode_drafts_lang_check CHECK (lang IN ('en', 'vi', 'zh'));

-- +goose Down
ALTER TABLE episode_drafts DROP CONSTRAINT episode_drafts_lang_check;
ALTER TABLE episode_drafts ADD CONSTRAINT episode_drafts_lang_check CHECK (lang IN ('en', 'vi'));
