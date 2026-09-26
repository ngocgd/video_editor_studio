-- name: ListVoicePresets :many
SELECT * FROM voice_presets WHERE tenant_id = @tenant_id ORDER BY name, id;

-- name: GetVoicePreset :one
SELECT * FROM voice_presets WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetVoicePresetsByIDs :many
SELECT * FROM voice_presets WHERE tenant_id = @tenant_id AND id = ANY(@ids::uuid[]);

-- name: CreateVoicePreset :one
INSERT INTO voice_presets (id, tenant_id, name, engine, ref_audio_asset_id, params, consented_at, consented_by)
VALUES (@id, @tenant_id, @name, @engine, @ref_audio_asset_id, @params, @consented_at, @consented_by)
RETURNING *;

-- name: UpdateVoicePreset :one
UPDATE voice_presets
SET name = @name, engine = @engine, ref_audio_asset_id = @ref_audio_asset_id, params = @params,
    consented_at = @consented_at, consented_by = @consented_by, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: DeleteVoicePreset :execrows
DELETE FROM voice_presets WHERE tenant_id = @tenant_id AND id = @id;

-- name: CountVoicePresetUses :one
SELECT
    (SELECT count(*) FROM character_voices cv WHERE cv.tenant_id = @tenant_id AND cv.voice_preset_id = @id)
  + (SELECT count(*) FROM narrator_voices nv WHERE nv.tenant_id = @tenant_id AND nv.voice_preset_id = @id) AS uses;

-- name: ListImageStyles :many
SELECT * FROM image_styles WHERE tenant_id = @tenant_id ORDER BY name, id;

-- name: GetImageStyle :one
SELECT * FROM image_styles WHERE tenant_id = @tenant_id AND id = @id;

-- name: FirstImageStyle :one
-- The tenant-wide fallback when neither the scene nor the series names a
-- style: the oldest one.
SELECT * FROM image_styles WHERE tenant_id = @tenant_id ORDER BY created_at, id LIMIT 1;

-- name: CreateImageStyle :one
INSERT INTO image_styles (id, tenant_id, name, style_prompt, negative_prompt, base_model, sampler, steps, width, height, loras)
VALUES (@id, @tenant_id, @name, @style_prompt, @negative_prompt, @base_model, @sampler, @steps, @width, @height, @loras)
RETURNING *;

-- name: UpdateImageStyle :one
UPDATE image_styles
SET name = @name, style_prompt = @style_prompt, negative_prompt = @negative_prompt, base_model = @base_model,
    sampler = @sampler, steps = @steps, width = @width, height = @height, loras = @loras, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: DeleteImageStyle :execrows
DELETE FROM image_styles WHERE tenant_id = @tenant_id AND id = @id;
