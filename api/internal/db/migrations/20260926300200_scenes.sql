-- +goose Up

-- Storyboard defaults per series: the image style new scenes use, the
-- image-change cadence the split aims for, and the silence inserted
-- between two speaker segments of one scene's voice track.
CREATE TABLE series_storyboard_settings (
    series_id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    image_style_id uuid,
    cadence_min_s integer NOT NULL DEFAULT 20 CHECK (cadence_min_s BETWEEN 5 AND 300),
    cadence_max_s integer NOT NULL DEFAULT 40 CHECK (cadence_max_s BETWEEN 5 AND 600),
    segment_gap_ms integer NOT NULL DEFAULT 150 CHECK (segment_gap_ms BETWEEN 0 AND 5000),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT series_storyboard_settings_tenant_series_fkey FOREIGN KEY (tenant_id, series_id)
        REFERENCES series (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT series_storyboard_settings_tenant_style_fkey FOREIGN KEY (tenant_id, image_style_id)
        REFERENCES image_styles (tenant_id, id) ON DELETE SET NULL (image_style_id),
    CONSTRAINT series_storyboard_settings_cadence_order CHECK (cadence_min_s <= cadence_max_s)
);

-- One scene of an episode's draft in one language. paragraph_ids point
-- into episode_drafts.paragraphs; segments is the voiced text split by
-- speaker: `[{speakerCharacterId|null, text, unrecognisedName?}]` where a
-- null speaker is the narrator. text_hash is the hash of the narration,
-- which re-split uses to keep a scene (and its takes) whose text did not
-- change. tainted is inherited from the source paragraphs.
CREATE TABLE scenes (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    episode_id uuid NOT NULL,
    lang text NOT NULL CHECK (lang IN ('en', 'vi')),
    idx integer NOT NULL,
    paragraph_ids text[] NOT NULL DEFAULT '{}',
    narration text NOT NULL DEFAULT '',
    segments jsonb NOT NULL DEFAULT '[]'::jsonb,
    image_prompt text NOT NULL DEFAULT '',
    character_ids uuid[] NOT NULL DEFAULT '{}',
    motion_preset text NOT NULL DEFAULT 'ken_burns' CHECK (motion_preset IN ('ken_burns', 'parallax', 'static')),
    image_style_id uuid,
    duration_ms integer NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    duration_measured boolean NOT NULL DEFAULT false,
    text_hash text NOT NULL,
    tainted boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT scenes_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT scenes_tenant_episode_fkey FOREIGN KEY (tenant_id, episode_id)
        REFERENCES episodes (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT scenes_tenant_style_fkey FOREIGN KEY (tenant_id, image_style_id)
        REFERENCES image_styles (tenant_id, id) ON DELETE SET NULL (image_style_id),
    -- Deferred so a re-split can renumber kept scenes inside one
    -- transaction without colliding with their own old positions.
    CONSTRAINT scenes_episode_lang_idx_key UNIQUE (episode_id, lang, idx) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX scenes_tenant_episode_idx ON scenes (tenant_id, episode_id, lang, idx);

-- Every generated image, voice track or subtitle alignment of a scene.
-- At most one take per (scene, kind) is selected; input_hash is the
-- scene's input hash for that kind when the take was produced, so a
-- selected take whose hash differs from the current one is stale.
-- params carries the component hashes the stale reason is derived from.
CREATE TABLE scene_takes (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    scene_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('image', 'voice', 'align')),
    asset_id uuid NOT NULL REFERENCES assets (id) ON DELETE CASCADE,
    params jsonb NOT NULL DEFAULT '{}'::jsonb,
    input_hash text NOT NULL,
    selected boolean NOT NULL DEFAULT false,
    step_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT scene_takes_tenant_scene_fkey FOREIGN KEY (tenant_id, scene_id)
        REFERENCES scenes (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX scene_takes_scene_idx ON scene_takes (tenant_id, scene_id, kind, created_at);
CREATE UNIQUE INDEX scene_takes_one_selected_idx ON scene_takes (scene_id, kind) WHERE selected;

-- The storyboard rollup reads the latest step of each kind per scene.
CREATE INDEX pipeline_steps_scope_latest_idx ON pipeline_steps (tenant_id, scope_kind, scope_id, kind, id DESC);

-- +goose Down
DROP INDEX pipeline_steps_scope_latest_idx;
DROP TABLE scene_takes;
DROP TABLE scenes;
DROP TABLE series_storyboard_settings;
