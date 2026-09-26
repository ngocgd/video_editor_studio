-- name: ListCharactersBySeries :many
SELECT * FROM characters WHERE tenant_id = @tenant_id AND series_id = @series_id ORDER BY created_at, id;

-- name: GetCharacter :one
SELECT * FROM characters WHERE tenant_id = @tenant_id AND id = @id;

-- name: CreateCharacter :one
INSERT INTO characters (id, tenant_id, series_id, name_orig, name_en, name_vi, role, appearance_prompt,
                        negative_prompt, trigger_token, profile, pinned)
VALUES (@id, @tenant_id, @series_id, @name_orig, @name_en, @name_vi, @role, @appearance_prompt,
        @negative_prompt, @trigger_token, @profile, @pinned)
RETURNING *;

-- name: UpdateCharacter :one
UPDATE characters
SET name_orig = @name_orig, name_en = @name_en, name_vi = @name_vi, role = @role,
    appearance_prompt = @appearance_prompt, negative_prompt = @negative_prompt,
    trigger_token = @trigger_token, profile = @profile, pinned = @pinned, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: DeleteCharacter :execrows
DELETE FROM characters WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListPinnedCharacterProfiles :many
-- Everything storyctx pins into a series' LLM requests.
SELECT id, name_orig, name_en, name_vi, profile FROM characters
WHERE tenant_id = @tenant_id AND series_id = @series_id AND pinned AND profile <> ''
ORDER BY created_at, id;

-- name: ListCharacterRefsBySeries :many
SELECT r.* FROM character_refs r
JOIN characters c ON c.id = r.character_id AND c.tenant_id = @tenant_id
WHERE r.tenant_id = @tenant_id AND c.series_id = @series_id
ORDER BY r.created_at, r.id;

-- name: GetCharacterRef :one
SELECT * FROM character_refs WHERE tenant_id = @tenant_id AND id = @id;

-- name: CreateCharacterRef :one
INSERT INTO character_refs (id, tenant_id, character_id, asset_id, angle, approved, origin)
VALUES (@id, @tenant_id, @character_id, @asset_id, @angle, @approved, @origin)
RETURNING *;

-- name: UpdateCharacterRef :one
UPDATE character_refs SET angle = @angle, approved = @approved
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: DeleteCharacterRef :execrows
DELETE FROM character_refs WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListCharacterLorasBySeries :many
SELECT l.* FROM character_loras l
JOIN characters c ON c.id = l.character_id AND c.tenant_id = @tenant_id
WHERE l.tenant_id = @tenant_id AND c.series_id = @series_id
ORDER BY l.character_id, l.version;

-- name: GetCharacterLora :one
SELECT * FROM character_loras WHERE tenant_id = @tenant_id AND id = @id;

-- name: NextCharacterLoraVersion :one
SELECT COALESCE(MAX(version), 0) + 1 AS next_version FROM character_loras
WHERE tenant_id = @tenant_id AND character_id = @character_id;

-- name: CreateCharacterLora :one
INSERT INTO character_loras (id, tenant_id, character_id, version, dataset_asset_ids, trainer_params, status, step_id)
VALUES (@id, @tenant_id, @character_id, @version, @dataset_asset_ids, @trainer_params, @status, @step_id)
RETURNING *;

-- name: UpdateCharacterLoraStatus :one
UPDATE character_loras
SET status = @status, weights_asset_id = @weights_asset_id, weights_file = @weights_file, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: LatestReadyLoras :many
-- The newest ready LoRA of each given character: what an image step uses.
SELECT DISTINCT ON (character_id) * FROM character_loras
WHERE tenant_id = @tenant_id AND character_id = ANY(@character_ids::uuid[]) AND status = 'ready'
ORDER BY character_id, version DESC;

-- name: ListCharacterVoicesBySeries :many
SELECT v.* FROM character_voices v
JOIN characters c ON c.id = v.character_id AND c.tenant_id = @tenant_id
WHERE v.tenant_id = @tenant_id AND c.series_id = @series_id
ORDER BY v.character_id, v.lang;

-- name: UpsertCharacterVoice :one
INSERT INTO character_voices (id, tenant_id, character_id, lang, engine, voice_preset_id, params)
VALUES (@id, @tenant_id, @character_id, @lang, @engine, @voice_preset_id, @params)
ON CONFLICT (character_id, lang) DO UPDATE
SET engine = EXCLUDED.engine, voice_preset_id = EXCLUDED.voice_preset_id, params = EXCLUDED.params, updated_at = now()
WHERE character_voices.tenant_id = @tenant_id
RETURNING *;

-- name: ListNarratorVoicesBySeries :many
SELECT * FROM narrator_voices WHERE tenant_id = @tenant_id AND series_id = @series_id ORDER BY lang;

-- name: UpsertNarratorVoice :one
INSERT INTO narrator_voices (id, tenant_id, series_id, lang, engine, voice_preset_id, params)
VALUES (@id, @tenant_id, @series_id, @lang, @engine, @voice_preset_id, @params)
ON CONFLICT (series_id, lang) DO UPDATE
SET engine = EXCLUDED.engine, voice_preset_id = EXCLUDED.voice_preset_id, params = EXCLUDED.params, updated_at = now()
WHERE narrator_voices.tenant_id = @tenant_id
RETURNING *;

-- name: CharacterEpisodeAppearances :many
-- Which episodes each character of a series appears in, and in how many
-- scenes, aggregated from scenes.character_ids in one query.
SELECT ch.id AS character_id, e.id AS episode_id, e.idx AS episode_idx, count(*)::int AS scene_count
FROM characters ch
JOIN scenes s ON s.tenant_id = @tenant_id AND ch.id = ANY(s.character_ids)
JOIN episodes e ON e.id = s.episode_id AND e.tenant_id = @tenant_id
WHERE ch.tenant_id = @tenant_id AND ch.series_id = @series_id
GROUP BY ch.id, e.id, e.idx
ORDER BY ch.id, e.idx;
