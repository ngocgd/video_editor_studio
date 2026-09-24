# Phase 06: Series, story bible, outline → draft writer, AI actions, EN/VI, import, LLM settings UI

## Context links
- [plan.md](plan.md) · [contract §2 AC1–3, AC8; §4 sitemap (Projects, Story Bible, Episodes, Import)](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- Wireframes: [story-writer](../../docs/wireframe/story-writer.html) (high fidelity), [settings-models](../../docs/wireframe/settings-models.html) (LLM providers panel)
- [design-guidelines §7 inline AI actions and diff proposals, §8 writer keys](../../docs/design-guidelines.md)
- [channel policy: avoid 1:1 verbatim scripts, inauthentic content](../reports/researcher-260924-2145-narration-voice-tts-channels.md)
- Depends on phases 3 (steps, SSE), 4 (LLM registry) and 5 (UI library).

## Overview
- Priority: P1 · Status: pending · Effort: 24h <!-- RT#15 re-estimate -->
- This phase delivers the whole writing workflow: series → story bible → episode outline (beats) → draft. It includes streaming AI actions shown as reviewable diffs, EN and VI variants of the same episode, a live word-to-duration estimate, chapter import with encoding detection, chapter splitting and translation, and the LLM provider settings screen. It is fully testable with the real claude CLI and needs no local model; the Ollama variant of the success criteria runs once phase 9b has pulled the local LLM. <!-- RT#7 -->

## Requirements
- Series settings: title, genre, target language(s), target episode minutes, planned episode count, style notes. A **new series from settings** flow generates the bible and the episode outlines through LLM steps.
- Story bible sections: world, cultivation realms, arcs, style guide, running summary, and a glossary (term → EN/VI renderings, which matters for Chinese names). Each section is versioned.
- Episodes: `outline` (ordered beats with id, summary and target words) and `episode_drafts(episode_id, lang, paragraphs jsonb [{id, text}], version, word_count)`, one row per language. There is also an append-only revisions table that keeps the last 50 per draft.
- AI actions:
  - Outline episode, Expand beat, Continue, Rewrite, Expand, Shorten, Tone ("Make it tenser")
  - Translate EN↔VI and ZH→EN/VI, glossary-aware
  - Summarise episode, which feeds "Previously"
  - Each action is a pipeline step at **priority 1 (interactive)** on the queue resolved at enqueue (`registry.QueueFor`: Ollama → `gpu`, others → `llm`), with tokens streamed over SSE into a `DiffProposal`. When it waits behind a GPU chunk, the proposal shows "Waiting for GPU slot (#n)". Nothing is applied until Accept (Tab), then a PATCH with a version check. Esc rejects. <!-- RT#6 -->
- Context assembly: `storyctx.Build(episode, action)` → a fixed server system template, then **every piece of story content as a nonce-fenced `DataBlock`** (bible excerpt, pinned character profiles — the hook stays empty until phase 7 —, "Previously" summaries, the target text). Nothing derived from story content is ever placed in `System`. It is token-budgeted per provider context window and truncates by priority. <!-- RT#12 -->
- **Provenance** <!-- RT#12 -->: `story_bibles` sections, `episode_drafts` paragraphs and summaries carry `origin` (`user|import|model`) and `tainted bool`. Import text is tainted; any LLM output whose inputs included a tainted block is stored tainted; a human edit of a paragraph does not clear taint (only an explicit "Mark reviewed" by an editor, audited). Taint travels into scene text (phase 7) and publish metadata (phase 10).
- Duration estimate: `duration.Estimate(words, lang, voicePreset)` uses defaults of EN 150 wpm and VI 165 wpm, marked "uncalibrated" until phase 9b writes measured rates. It is shown live in the editor footer and the episode list. A 30-min EN target is ≈4,500 words.
- Import:
  - Upload `.txt` or `.md` (≤10MB) through presigned POST.
  - Encoding detection covers UTF-8, UTF-16 BOM and GB18030/GBK (`golang.org/x/text`).
  - Chapter split uses regex presets (`第.{1,9}章`, `Chapter \d+`, `Chương \d+`) with a preview and manual adjustment.
  - Chapters then become episodes, with an optional translation step.
  - A persistent banner on imported projects shows the user-owned content-rights notice (contract §7). It informs only and does not block.
- LLM settings page: default provider/model, per-action override, API key entry (write-only), a Test button with the latency result, and the claude CLI status (version, auth state, "Tools disabled"). This closes AC8 in the UI.
- Autosave: debounced 1s, sending paragraph-level JSON patch ops (`upsert` and `delete` by paragraph id, `move`). A version mismatch returns 409 and shows a merge prompt.

## Architecture
- Tables: `series`, `story_bibles` (sections jsonb with per-section `origin`/`tainted` + version), `episodes`, `episode_drafts` (paragraph-level `origin`/`tainted`), `episode_draft_revisions`, `imports` (asset, encoding, chapters jsonb, status).
- Step kinds: `llm.outline`, `llm.expand_beat`, `llm.continue`, `llm.rewrite`, `llm.translate`, `llm.summarise`, `llm.bible_seed`. Each is a thin handler around `registry.Resolve(tenant, action)` plus a prompt template (`api/internal/story/prompts/*.tmpl`, embedded, version-stamped into the step output for provenance).
- Flow: the editor selection goes to `POST /episodes/{id}/ai-actions {action, lang, paragraphIds, range, instruction}`, which creates a step. SSE sends `llm.delta` events (batched every 100ms) to the `DiffProposal`. Accept sends a `PATCH /episodes/{id}/drafts/{lang}`.
- Imported and derived text is always a tainted `DataBlock`. The user's own instruction is sent as a separate, length-capped (500 chars) `DataBlock` with origin `user`; it is not concatenated into the system template. <!-- RT#12 -->
- Editor: TipTap (ProseMirror) in a lazy writer chunk. A paragraph node carries an `id` attribute, and proposals render as decorations (insert and strike). `lang="vi"` is set on VI drafts. The layout is Literata `read-lg`, max 68ch. ProseMirror's paste-HTML parsing goes through the phase 5 Trusted Types `dompurify` policy. <!-- RT#14 -->

## Related files
- Create:
  - `db/migrations/*_story.sql`, `db/queries/{series,bible,episodes,drafts,imports}.sql`
  - `api/internal/{story,storyctx,duration,importer}/`, `api/internal/story/prompts/*.tmpl`
  - `openapi/paths/{series,episodes,imports}.yaml`, `openapi/schemas/story.yaml`
  - `web/src/features/{series,bible,writer,import,settings-llm}/`
  - `web/src/routes/_app/projects/**`, `web/src/routes/_app/import.tsx`, `web/src/routes/_app/settings/llm.tsx`
- Modify: `openapi/root.yaml`. The handler constructors are exported, and the lead wires them into `cmd/worker`.

## Implementation steps
1. Write the schema and queries, plus the series, bible and episode CRUD endpoints (cursor lists).
2. Write the draft PATCH with paragraph ops, version check and revisions.
3. Implement `storyctx.Build` with data-block fencing, taint propagation and token budgeting (a tokenizer estimate per provider family) and golden tests.
4. Write the prompt templates and step handlers for each action, with JSON Schema outputs for outline and bible seed.
5. Implement the importer: encoding detection, chapter split presets, preview endpoint, then commit to episodes.
6. Build the writer UI: three panes (bible/context left, editor centre, beats/characters right), the AI toolbar on selection, the `DiffProposal`, the EN/VI toggle (`Ctrl Alt L`) and the duration footer.
7. Build the series create wizard (settings → bible seed → outlines) with live step progress through the shared `JobProgress`.
8. Build the Import screen and the Settings > LLM providers screen.

## Todo checklist
- [ ] Schema + CRUD + paragraph PATCH
- [ ] Context builder (data blocks, taint) + budgets
- [ ] AI action steps + templates + schemas
- [ ] Importer (encoding, split, translate)
- [ ] Writer UI + diff proposals + shortcuts
- [ ] Series wizard
- [ ] Import UI + LLM settings UI

## Performance budget checks
- The writer route chunk (TipTap included) is ≤180KB gzip and lazy-loaded. It stays out of the initial bundle, enforced by size-limit.
- Typing latency is ≤16ms per keystroke on a 12k-word draft (Playwright trace in phase 12). Autosave payload is ≤5KB for a single-paragraph edit.
- The first token of an AI action appears ≤3s after the request when its queue is idle (claude CLI spawn included), measured and logged; with Ollama behind a running GPU chunk the wait is bounded by the phase 3 chunk size and shown to the user. Diff rendering is ≤50ms for a 2k-word proposal.
- Episode list queries have no N+1: word counts and draft status are aggregated in one query.

## Security checklist
- [ ] All story content (imported, pasted, derived bible/summaries) passed only as nonce-fenced data blocks; taint stored and propagated; canary injection corpus on translate/rewrite/summarise paths, including a canary planted in an imported chapter that must not surface via a later summary
- [ ] LLM output rendered as text; the diff is computed client-side on plain strings (no HTML)
- [ ] Upload MIME sniffed as text; size capped; decoding errors reported, not guessed silently
- [ ] Editor role required for mutations; viewer read-only UI (disabled with reason)
- [ ] Rights notice shown on imported projects (informational, contract §7)

## Reuse points
- Reuse: `DiffProposal`, `JobProgress`, `InspectorPanel`, `EmptyState`, `sse-bridge`, `registry.Resolve`, `pipeline.StepHandler`, presigned upload.
- Create `storyctx.Build` (reused by scene split in phase 7 and metadata/thumbnail text in phase 10) and `duration.Estimate` (reused in phases 7, 8 and 10).

## Tests
- `scripts/tb.ps1 test` covers the paragraph ops, version conflicts, storyctx golden files (no story text in `System`), taint propagation, encoding detection (UTF-8, GB18030 and UTF-16 fixtures) and the chapter split presets.
- `scripts/tb.ps1 test-integration` covers the AI action step with a test-double provider in `_test.go`. With the build tag `claudecli` it runs the real claude CLI: outline a 5-beat episode, then translate one paragraph EN→VI.
- `cd web && npm run test` covers the DiffProposal accept and reject, and the paragraph op generation.

## Success criteria
- From a settings form, a series with a bible and ≥1 outlined episode is produced with the real claude CLI, and a ≥4,500-word EN draft is reachable through Expand/Continue with the estimate showing ≥30 min.
- The same episode has a VI draft through Translate, with glossary names applied.
- Importing a GB18030 Chinese chapter yields episodes and a translated EN draft (the AC3 writing half).
- Switching the provider in Settings changes the provider shown on the next AI action (AC8 in the UI).
- **Ollama variant** (run after phase 9b, recorded in the phase 9c report): the outline, Expand/Continue and EN→VI translate criteria above pass with Ollama as the provider, and a batch GPU job running concurrently does not block the interactive action by more than one chunk. <!-- RT#7 -->

## Risks + rollback
- Long-form VI quality from local LLMs is unknown (contract §6). The provider is switchable per action, and the local-LLM benchmark is in phase 9b.
- Context overflow on long series (Medium×Medium) is handled by the rolling "Previously" summaries and priority truncation.
- TipTap bundle weight (Low×Medium) is handled by the lazy chunk and budget. The fallback is a plain textarea with a side-by-side diff.
- Rollback: feature routes and tables only. Revert the PR and apply the goose down migration.

## Next steps
Phase 7 splits drafts into scenes and pins character profiles into `storyctx`.
