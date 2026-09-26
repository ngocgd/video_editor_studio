-- +goose Up

-- Render settings per episode and language. Rows are created on first
-- save; a missing row means the defaults below. subtitle_style is
-- `{font, sizePx, position, shadowPx}`. The loudness target is stored in
-- tenths (-140 = -14.0 LUFS, -10 = -1.0 dBTP) so the values stay exact.
CREATE TABLE render_settings (
    episode_id uuid NOT NULL,
    lang text NOT NULL CHECK (lang IN ('en', 'vi')),
    tenant_id uuid NOT NULL,
    width integer NOT NULL DEFAULT 1920 CHECK (width BETWEEN 320 AND 3840 AND width % 2 = 0),
    height integer NOT NULL DEFAULT 1080 CHECK (height BETWEEN 180 AND 2160 AND height % 2 = 0),
    fps integer NOT NULL DEFAULT 30 CHECK (fps IN (24, 25, 30, 60)),
    encoder text NOT NULL DEFAULT 'auto' CHECK (encoder IN ('auto', 'h264_nvenc', 'libx264')),
    subtitles text NOT NULL DEFAULT 'both' CHECK (subtitles IN ('burn', 'srt', 'both')),
    subtitle_style jsonb NOT NULL DEFAULT '{"font": "Literata", "sizePx": 42, "position": "bottom", "shadowPx": 2}'::jsonb,
    default_motion text NOT NULL DEFAULT 'ken_burns' CHECK (default_motion IN ('ken_burns', 'parallax', 'static')),
    crossfade_ms integer NOT NULL DEFAULT 600 CHECK (crossfade_ms BETWEEN 0 AND 3000),
    loudness_lufs_x10 integer NOT NULL DEFAULT -140 CHECK (loudness_lufs_x10 BETWEEN -300 AND -50),
    true_peak_dbtp_x10 integer NOT NULL DEFAULT -10 CHECK (true_peak_dbtp_x10 BETWEEN -90 AND 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (episode_id, lang),
    CONSTRAINT render_settings_tenant_episode_fkey FOREIGN KEY (tenant_id, episode_id)
        REFERENCES episodes (tenant_id, id) ON DELETE CASCADE
);

-- A frozen render input. Every render step reads only its manifest, never
-- live scene rows, so a take selected mid-render cannot leak into it.
-- settings is the resolved settings snapshot; scenes is
-- `[{sceneId, idx, imageAssetId, imageSha256, voiceAssetId, voiceSha256,
-- alignAssetId, motion, durationFrames, placeholder}]`; hash covers both
-- and is the compose input hash.
CREATE TABLE render_manifests (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    episode_id uuid NOT NULL,
    lang text NOT NULL CHECK (lang IN ('en', 'vi')),
    settings jsonb NOT NULL,
    settings_hash text NOT NULL,
    scenes jsonb NOT NULL,
    hash text NOT NULL,
    run_id uuid REFERENCES pipeline_runs (id) ON DELETE SET NULL,
    -- Set when a scene edit superseded the previous render run: the UI
    -- says "Render restarted after edit (N segments reused)".
    restarted_after_edit boolean NOT NULL DEFAULT false,
    reused_segments integer NOT NULL DEFAULT 0 CHECK (reused_segments >= 0),
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT render_manifests_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT render_manifests_tenant_episode_fkey FOREIGN KEY (tenant_id, episode_id)
        REFERENCES episodes (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX render_manifests_episode_idx ON render_manifests (tenant_id, episode_id, lang, created_at DESC);
CREATE INDEX render_manifests_run_idx ON render_manifests (run_id);

-- Content-addressed segment cache: one encoded object per input hash
-- (scene body, transition, audio master, subtitles, preview). A cache
-- hit is valid only while the stored object's checksum equals
-- assets.sha256; deleting the asset drops the cache row with it.
CREATE TABLE render_segments (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    input_hash text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('body', 'transition', 'audio', 'subtitles', 'preview')),
    episode_id uuid NOT NULL,
    lang text NOT NULL CHECK (lang IN ('en', 'vi')),
    asset_id uuid NOT NULL REFERENCES assets (id) ON DELETE CASCADE,
    duration_frames integer NOT NULL DEFAULT 0 CHECK (duration_frames >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, input_hash),
    CONSTRAINT render_segments_tenant_episode_fkey FOREIGN KEY (tenant_id, episode_id)
        REFERENCES episodes (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX render_segments_asset_idx ON render_segments (asset_id);
CREATE INDEX render_segments_last_used_idx ON render_segments (tenant_id, last_used_at);

-- The segment input hashes a manifest's DAG needs, written at freeze
-- time. TTL cleanup never deletes a segment pinned by the latest
-- manifest of its episode/lang or by a manifest that produced a render.
CREATE TABLE render_manifest_segments (
    manifest_id uuid NOT NULL REFERENCES render_manifests (id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL,
    input_hash text NOT NULL,
    PRIMARY KEY (manifest_id, input_hash)
);
CREATE INDEX render_manifest_segments_hash_idx ON render_manifest_segments (tenant_id, input_hash);

-- A finished episode render. report is the QC report (loudness, true
-- peak, subtitle drift, missing scenes, stream checks, per-scene image
-- scores, sha256) that publishing reads.
CREATE TABLE renders (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    episode_id uuid NOT NULL,
    lang text NOT NULL CHECK (lang IN ('en', 'vi')),
    manifest_id uuid NOT NULL,
    settings_hash text NOT NULL,
    asset_id uuid NOT NULL REFERENCES assets (id) ON DELETE RESTRICT,
    srt_asset_id uuid REFERENCES assets (id) ON DELETE RESTRICT,
    preview_asset_id uuid REFERENCES assets (id) ON DELETE SET NULL,
    sha256 text NOT NULL,
    duration_ms integer NOT NULL CHECK (duration_ms >= 0),
    encoder text NOT NULL,
    report jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT renders_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT renders_manifest_key UNIQUE (manifest_id),
    CONSTRAINT renders_tenant_manifest_fkey FOREIGN KEY (tenant_id, manifest_id)
        REFERENCES render_manifests (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT renders_tenant_episode_fkey FOREIGN KEY (tenant_id, episode_id)
        REFERENCES episodes (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX renders_episode_idx ON renders (tenant_id, episode_id, lang, created_at DESC);

-- Library retention per tenant: unreferenced render segments and
-- unselected takes older than these many days are removed by the daily
-- cleanup step.
CREATE TABLE library_settings (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    segment_ttl_days integer NOT NULL DEFAULT 14 CHECK (segment_ttl_days BETWEEN 1 AND 365),
    take_ttl_days integer NOT NULL DEFAULT 30 CHECK (take_ttl_days BETWEEN 1 AND 365),
    last_cleanup_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE library_settings;
DROP TABLE renders;
DROP TABLE render_manifest_segments;
DROP TABLE render_segments;
DROP TABLE render_manifests;
DROP TABLE render_settings;
