-- +goose Up

-- Voice presets: a TTS engine plus its tuning, optionally cloned from a
-- reference audio asset. A reference voice is only usable once someone
-- confirmed it is their own or licensed (consented_at/consented_by); the
-- confirmation is also written to audit_log by the API.
CREATE TABLE voice_presets (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name text NOT NULL,
    engine text NOT NULL,
    ref_audio_asset_id uuid REFERENCES assets (id) ON DELETE SET NULL,
    params jsonb NOT NULL DEFAULT '{}'::jsonb,
    consented_at timestamptz,
    consented_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT voice_presets_tenant_id_id_key UNIQUE (tenant_id, id),
    -- A cloned voice can never exist without a recorded consent.
    CONSTRAINT voice_presets_ref_needs_consent CHECK (ref_audio_asset_id IS NULL OR consented_at IS NOT NULL)
);

-- Image styles: the prompt prefix and sampler settings a scene image is
-- generated with. base_model names a models/manifest.yaml entry; loras
-- is `[{name, strength}]` with names of LoRA files on the models volume.
CREATE TABLE image_styles (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name text NOT NULL,
    style_prompt text NOT NULL DEFAULT '',
    negative_prompt text NOT NULL DEFAULT '',
    base_model text NOT NULL,
    sampler text NOT NULL DEFAULT '',
    steps integer NOT NULL DEFAULT 8 CHECK (steps BETWEEN 1 AND 150),
    width integer NOT NULL DEFAULT 1920 CHECK (width BETWEEN 64 AND 4096),
    height integer NOT NULL DEFAULT 1080 CHECK (height BETWEEN 64 AND 4096),
    loras jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT image_styles_tenant_id_id_key UNIQUE (tenant_id, id)
);

-- +goose Down
DROP TABLE image_styles;
DROP TABLE voice_presets;
