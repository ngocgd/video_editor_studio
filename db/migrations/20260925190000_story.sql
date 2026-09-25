-- +goose Up

-- A series is the top-level writing project: settings only. The bible,
-- episodes and imports below all hang off it.
CREATE TABLE series (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    title text NOT NULL,
    genre text NOT NULL DEFAULT '',
    target_languages text[] NOT NULL DEFAULT ARRAY['en']::text[],
    target_episode_minutes integer NOT NULL DEFAULT 30,
    planned_episode_count integer NOT NULL DEFAULT 1,
    style_notes text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'archived')),
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX series_tenant_id_id_idx ON series (tenant_id, id);

-- One row per series. `sections` holds every bible section keyed by name
-- (world, cultivation_realms, arcs, style_guide, running_summary,
-- glossary); each value is `{content, origin, tainted, version}` so a
-- single jsonb column carries the whole per-section provenance and
-- version history the writer UI needs, per storyctx.Build's DataBlock
-- fencing (api/internal/storyctx). `origin` is 'user'|'import'|'model'.
CREATE TABLE story_bibles (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    series_id uuid NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    sections jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX story_bibles_series_id_key ON story_bibles (series_id);
CREATE INDEX story_bibles_tenant_id_idx ON story_bibles (tenant_id);

-- `idx` is the ordering within the series (1-based); `outline` is the
-- ordered beat array `[{id, summary, target_words}]` produced by
-- llm.outline. word_count/status of each language's draft are
-- aggregated at query time from episode_drafts to keep the episode list
-- free of N+1s (db/queries/episodes.sql ListEpisodesWithDraftStatus).
CREATE TABLE episodes (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    series_id uuid NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    idx integer NOT NULL,
    title text NOT NULL DEFAULT '',
    outline jsonb NOT NULL DEFAULT '[]'::jsonb,
    status text NOT NULL DEFAULT 'planned'
        CHECK (status IN ('planned', 'outlined', 'drafting', 'draft', 'reviewed')),
    source_import_chapter_index integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX episodes_series_id_idx_key ON episodes (series_id, idx);
CREATE INDEX episodes_tenant_id_id_idx ON episodes (tenant_id, id);

-- One row per (episode, language). `paragraphs` is `[{id, text, origin,
-- tainted}]`, ordered. `version` is the optimistic-concurrency token the
-- draft PATCH checks (If-Match style, carried in the request body);
-- autosave conflicts return 409 when the client's version is stale.
-- `summary`/`summary_tainted` cache the last llm.summarise "Previously"
-- output so later actions do not recompute it every call.
CREATE TABLE episode_drafts (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    episode_id uuid NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    lang text NOT NULL CHECK (lang IN ('en', 'vi')),
    paragraphs jsonb NOT NULL DEFAULT '[]'::jsonb,
    version bigint NOT NULL DEFAULT 0,
    word_count integer NOT NULL DEFAULT 0,
    summary text NOT NULL DEFAULT '',
    summary_tainted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX episode_drafts_episode_id_lang_key ON episode_drafts (episode_id, lang);
CREATE INDEX episode_drafts_tenant_id_idx ON episode_drafts (tenant_id);

-- Append-only revision history. The application trims to the last 50
-- rows per draft after each insert (api/internal/story), rather than a
-- trigger, to keep the trim policy in one reviewable place.
CREATE TABLE episode_draft_revisions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    draft_id uuid NOT NULL REFERENCES episode_drafts (id) ON DELETE CASCADE,
    version bigint NOT NULL,
    paragraphs jsonb NOT NULL,
    word_count integer NOT NULL,
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX episode_draft_revisions_draft_id_version_idx
    ON episode_draft_revisions (draft_id, version DESC);

-- One row per uploaded manuscript. `chapters` is the split preview/result
-- `[{index, title, charStart, charEnd, wordCount}]`; the source text
-- itself lives in the asset (MinIO), never duplicated into this row.
-- Import text is always tainted (RT#12); episodes created from a chapter
-- carry that taint forward into their first draft paragraphs.
CREATE TABLE imports (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    series_id uuid REFERENCES series (id) ON DELETE CASCADE,
    asset_id uuid NOT NULL REFERENCES assets (id) ON DELETE CASCADE,
    encoding text NOT NULL DEFAULT '',
    split_preset text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'uploaded'
        CHECK (status IN ('uploaded', 'preview', 'committed', 'failed')),
    chapters jsonb NOT NULL DEFAULT '[]'::jsonb,
    error_msg text,
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX imports_tenant_id_id_idx ON imports (tenant_id, id);
CREATE INDEX imports_series_id_idx ON imports (series_id);

-- +goose Down
DROP TABLE imports;
DROP TABLE episode_draft_revisions;
DROP TABLE episode_drafts;
DROP TABLE episodes;
DROP TABLE story_bibles;
DROP TABLE series;
