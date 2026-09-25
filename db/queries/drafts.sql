-- name: GetDraft :one
SELECT * FROM episode_drafts WHERE tenant_id = @tenant_id AND episode_id = @episode_id AND lang = @lang;

-- name: GetDraftByID :one
SELECT * FROM episode_drafts WHERE tenant_id = @tenant_id AND id = @id;

-- name: CreateDraft :one
INSERT INTO episode_drafts (id, tenant_id, episode_id, lang, paragraphs, word_count)
VALUES (@id, @tenant_id, @episode_id, @lang, @paragraphs, @word_count)
RETURNING *;

-- name: UpdateDraftParagraphs :one
-- version = current_version + 1 is computed by the caller (story.Store)
-- after loading and CAS-checking the row inside the same transaction, so
-- the WHERE clause below is the actual optimistic-concurrency fence: a
-- concurrent writer's UPDATE affects zero rows and the caller reports 409.
UPDATE episode_drafts
SET paragraphs = @paragraphs,
    word_count = @word_count,
    version = @next_version,
    updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND version = @expected_version
RETURNING *;

-- name: UpdateDraftSummary :one
UPDATE episode_drafts
SET summary = @summary,
    summary_tainted = @summary_tainted,
    updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: InsertDraftRevision :exec
INSERT INTO episode_draft_revisions (id, tenant_id, draft_id, version, paragraphs, word_count, created_by)
VALUES (@id, @tenant_id, @draft_id, @version, @paragraphs, @word_count, @created_by);

-- name: TrimDraftRevisions :exec
-- Keeps only the newest 50 revisions per draft; called after each insert.
DELETE FROM episode_draft_revisions AS outer_rev
WHERE outer_rev.draft_id = @draft_id
  AND outer_rev.id NOT IN (
    SELECT inner_rev.id FROM episode_draft_revisions AS inner_rev
    WHERE inner_rev.draft_id = @draft_id
    ORDER BY inner_rev.version DESC
    LIMIT 50
  );

-- name: ListDraftRevisions :many
SELECT * FROM episode_draft_revisions
WHERE tenant_id = @tenant_id AND draft_id = @draft_id
ORDER BY version DESC
LIMIT @page_limit;
