# Cook report: phase 6, story writer, import and LLM settings

Branch `feat/story-writer-import`, finished 2026-09-26. The code is complete and verified against a live stack, but none of the live-LLM success criteria could be run: `secrets/claude_oauth_token.txt` is empty (the user has not run `claude setup-token` yet) and Ollama has no model until phase 9b. The code review's 2 Critical and 5 of its 8 High findings are fixed and re-verified (see "Code review"). H1, H2 and H5 were fixed afterwards (see "Review follow-up fixes"). The independent verification then found two blockers with the real claude CLI; both are fixed and the live success criteria now pass with `COMPOSE_PROFILES=claude-cli` (see "Verification blockers").

## What shipped

- Series, story bible (versioned sections with `origin`/`tainted`), episodes with outlines, per-language drafts with paragraph ops (`upsert`/`delete`/`move`), a version-checked PATCH, and revisions.
- `storyctx.Build`: a fixed system template, with all story content passed as nonce-fenced data blocks and taint propagated. Golden tests assert that no story text reaches `System`.
- LLM step kinds (`llm.outline`, `bible_seed`, `expand_beat`, `continue`, `rewrite`, `translate`, `summarise`) at interactive priority. Their prompt templates are embedded.
- Importer: `.txt`/`.md` up to 10MB via presigned upload; UTF-8, UTF-16 BOM and GB18030 detection; `第.{1,9}章` / `Chapter N` / `Chương N` split presets with preview; commit to episodes; an import-rights banner.
- Writer UI: three panes, TipTap editor, AI toolbar on selection, `DiffProposal`, EN/VI toggle (`Ctrl Alt L`), duration footer ("uncalibrated"). Also the series wizard, the Import screen, and Settings > LLM providers (default, per-action overrides, write-only API keys, Test, claude CLI status).
- **AI action results are polled, not streamed.** `GET /episodes/{id}/ai-actions/{stepId}` returns the step status and, once done, the full text. The writer polls every 400ms until a terminal status, shows "Generating…", then shows the diff or the step error with Retry. This replaces the planned `llm.delta` token stream: `StepContext.Progress` is numeric-only, so no per-token write path exists. The unused client-side delta bus was removed.

## Fixed in this session (beyond the resume checklist)

| Issue | Impact | Fix |
|---|---|---|
| The writer mounted TipTap before the draft loaded and autosaved the empty doc over it | **Data loss**: every imported chapter lost its text on first open (reproduced: v0 with 2 paragraphs became v1 with 1 empty paragraph) | The editor mounts only after its draft loads, seeds from it, and is remounted by key on language switch, conflict reload and AI Accept. The same stale-document flaw also affected those three paths. An e2e test now commits an import, opens the episode and reloads |
| The AI result endpoint only checked the tenant | Any step id in the tenant (another episode's, or a non-AI step's output) was readable through any episode URL | Requires an AI-action step scoped to that episode; covered by an integration test |
| Accept was active while generating | Tab while pending would have replaced the selection with "" | Accept appears only when text is present |
| `MEDIA_ORIGIN` defaulted to empty | CSP `connect-src 'self'` blocked the presigned MinIO upload, so import failed with "Failed to fetch" on a default stack | Defaults to `http://127.0.0.1:9000` |
| The integration suite exceeded the per-IP login bucket (20/h) and the general limiter (100/min) | 13 unrelated tests failed with 429; CI's integration job would too | The login helper clears only the per-IP login bucket; `API_RATE_LIMIT_PER_MINUTE` is configurable (default 100) and set to 1000 in the CI and integration overrides |
| The writer e2e spec had never run | It crashed on `__dirname` (ESM), used an ambiguous locator, and had misleading empty-state copy | Fixed |

## Verification

| Check | Result |
|---|---|
| `scripts/tb.sh ci` | lint and test pass. `gen-check` fails only on the known worktree git-path artifact; host `git diff --exit-code` on the generated paths gives exit 0 |
| `scripts/tb.sh lint test vuln audit` (after the Go changes) | pass: govulncheck reports no reachable vulns, pip-audit is clean, npm audit reports 0 |
| Integration suite on `-p loomtale-p6` (`-race`) | **65/65** before review, run twice back to back on the same stack. After the review fixes, on a fresh stack: 61 top-level tests pass, 0 fail, 0 skip (counted as `--- PASS` lines), including the new concurrent-commit and bible tests |
| Web typecheck, lint, vitest | pass: 67 tests after the review fixes, including the paragraph-id split tests and the autosave queue tests (serialization, retry, conflict, merge) |
| Web build and bundle budget | pass. The writer route is 130.5KB gzip (budget 180KB) |
| Playwright `smoke.spec.ts` and `writer-import-settings.spec.ts` | both pass with `--workers=1`, re-run after the review fixes; the writer spec now also presses Enter, types, waits for "Saved" and reloads. With parallel workers the shared demo owner hits the per-account login limit (10/h), which is the limiter working as intended |
| Live AI action, no provider | the step goes queued → failed; the writer showed "Generating…", then the error with Retry/Dismiss, and nothing was applied (screens 08, 09) |

Screens are in `phase-06-screens/`: 01 series list, 02 wizard progress, 03 imported episode in the writer, 04 import preview, 05 LLM settings, 06 import committed, 07 AI toolbar, 08 generating, 09 failed with no provider.

## Deviations from the phase spec

- There is no token streaming: results are polled, as described above. The "Waiting for GPU slot (#n)" hint is not shown in the writer; the proposal shows "Generating…" until the step finishes.
- The proposal is docked below the editor rather than shown as inline ProseMirror decorations. Accepted text is split into paragraphs on blank lines by the server, not mapped back onto the original paragraph boundaries.
- "Outline" at episode scope returns 404; outlines are generated only by the series flow.
- "Mark reviewed" (the only way to clear taint) is not built. Taint therefore never clears, which is the safe default.
- There is no `claudecli`-tagged story integration test (outline a 5-beat episode, translate EN→VI). The provider-level live test with its canary exists.

## Success criteria status

| Criterion | Status |
|---|---|
| Series → bible → outlined episode → ≥4,500-word EN draft with the real claude CLI | **Met** (see "Verification blockers" for step ids) |
| VI draft through Translate with glossary names | **Met** |
| GB18030 chapter → episodes → translated EN draft | **Met** |
| Switching the provider in Settings changes the next action's provider | **Pending**: only claude-cli can complete an action. The switch itself is shown: the next action is routed to Ollama, and Settings explains why it cannot run |
| Ollama variant | Deferred to phase 9c by design |

## Code review

Review: `plans/reports/code-reviewer-260926-0703-phase-06-story-writer-review.md`. Verdict was **do not merge** (2 Critical, 8 High).

| Finding | Status |
|---|---|
| C1 Enter copies the paragraph id, so every later save 409s | **Fixed**: id not kept on split, duplicate ids re-assigned, server rejects empty or duplicate ids; unit and e2e coverage |
| C2 `POST /episodes` skips the series tenant check | **Fixed**: 404 for a foreign series; composite `(tenant_id, parent)` FKs on bibles, episodes, imports, drafts and revisions; integration test |
| H3 refetch moves the autosave base under a stale editor | **Fixed**: base set only from a settled fetch; a newer server version raises the conflict prompt |
| H4 AI Accept drops unsaved typing | **Fixed**: Accept builds on the editor's latest paragraphs and diffs against the saved base |
| H8 failed saves dropped, overlapping saves, polling drains the rate limit | **Fixed**: one save queue per draft (one PATCH in flight, retry with backoff, unsaved/retrying states, unload guard); polling backs off 400ms→2s and retries 429 |
| M1 pending save cancelled on language switch or unmount | **Fixed** with H8 (flush for the draft the edits belong to) |
| H6 import commit not atomic, repeatable, uncapped | **Fixed**: conditional claim plus one transaction, preview refuses committed imports, 500-chapter cap, over-size read is 422 (part of M2) |
| H7 bible section saves lose writes | **Fixed**: per-section `jsonb_set` update guarded by version; generation never overwrites a user-written section |
| "Keep editing mine" looped on 409 | **Fixed**: it now re-applies local changes on top of the latest version |
| H1 taint and provenance lost when AI output enters a draft | **Fixed**: `POST /episodes/{id}/drafts/{lang}/apply-step` stores the step's own text as `origin=model`, tainted when the step or the paragraphs it replaces or follows were; new paragraphs typed into a draft with tainted content inherit the taint; outline beats carry a taint flag; canary integration test |
| H2 Continue replaces text; Expand/Shorten/Tone run as plain rewrite; expand-beat does nothing | **Fixed**: the action is stored with the step; Continue and expand-beat insert after the anchor, Continue works from the caret, Expand/Shorten/Tone send a default intent, expand-beat works from the beat summary, Tone and Translate are in the toolbar |
| H5 no way to create drafts for outlined or manual episodes; translate output never saved; Chinese imports stored as `en` | **Fixed**: `PUT /episodes/{id}/drafts/{lang}` plus a Create draft button; translations go to the other language's draft (import-triggered ones fill an empty target draft); Chinese chapters are stored as a `zh` draft with per-character word counts |
| M2 (GB18030 guess, preface dropped), M3–M7, Lows | **Open**: follow-ups |

## Review follow-up fixes

Decisions taken (2026-09-26): draft creation and saved translations are in scope; an import keeps its source language as its own draft (`zh` added to the draft language check); new paragraphs in a draft with tainted content inherit the taint until "Mark reviewed" exists. The AI accept path is now server-side, so the client never writes model text through the generic PATCH.

Verification after these fixes (2026-09-26): `scripts/tb.sh gen lint test` green; web typecheck, lint, 69 vitest tests, build and bundle budget green; integration suite on a fresh stack 62 top-level tests pass, 0 fail, 0 skip (new: imported canary stays tainted through an accepted rewrite, a continuation and a split); both Playwright specs pass, including creating a draft on a manually created episode and a paragraph split that survives reload. Two earlier attempts failed before any test ran (a Docker build-cache snapshot error and a `minio-init` start-up flake); a third identical run passed.

Known limits: accepting the same finished step twice applies it twice (the client clears the proposal after one Accept); an interactive Translate appends to the target draft rather than aligning paragraph by paragraph.

## Verification blockers

Verifier report: `plans/reports/code-reviewer-260926-0950-phase-06-story-writer-verification-review.md` (do not merge: H1, H2, live criteria not met). Outcome of each blocking item:

| Item | Outcome |
|---|---|
| H1 claude-cli fails any generation over 15s | **Fixed.** The sidecar now commits `200` and flushes the headers as soon as it accepts a run; errors still travel in the final `result` line. The worker's claude-cli adapter uses its own client with a dial timeout but no response-header timeout, bounded by the step context and `LLMCLI_TIMEOUT`. Tests: a fake CLI that stays silent for 3s behind a 1s header timeout, and a check that `Build` gives claude-cli that client. `--include-partial-messages` was not added (optional; results are polled, not streamed) |
| H2 Settings always shows claude-cli unavailable | **Fixed.** The sidecar's `/healthz` now reports the CLI version in `X-Claude-CLI-Version`. The worker probes it on every 5s heartbeat and publishes installed, authenticated, version and reason in `worker_status.providers` (no migration: it is jsonb). `GET /settings/llm` and `/settings/llm/cli-status` read the worker's view for every provider; a key-based provider is also available when the tenant stored a key. Test now runs as an `llm.check.<provider>` pipeline step in the worker (permanent on failure, 30s wait, canceled on timeout), and an unavailable provider fails fast with the worker's reason. The api no longer dials `llm-cli`. Unit tests cover the worker probe, the settings availability rules, CLI status and Test; the integration test `TestClaudeCLIStatusComesFromTheWorker` checks that the CLI status and claude-cli availability agree, and with `LLMCLI_EXPECTED=1` that the sidecar reports its version and Test succeeds |
| Live criteria re-run | **Met.** Two more defects surfaced and were fixed first. (1) No adapter sent `JSONSchema` to the model, so `llm.outline` came back with `word_count` instead of `targetWords` and failed validation twice; `GenerateStructured` now appends the schema to `System` (a fixed server constant). (2) The API never filled `durationEstimateMinutes`; drafts and the episode list now return `duration.Estimate` for en/vi (not for character-counted zh) |
| Browser e2e | A Trusted Types race surfaced: the default policy was installed at idle time, and Radix ScrollArea's injected `<style>` crashed the writer on a hard reload ("Something went wrong", `TrustedHTML` error in the trace). `main.tsx` now awaits the policy before the first render; DOMPurify stays in its own chunk |

Live evidence (claude CLI 2.1.282, run under `COMPOSE_PROFILES=claude-cli` on `-p loomtale-a`, driven through the public API by the verifier's harness, which was never committed):

- Settings: `cli-status` returned installed, authenticated, tools disabled, version `2.1.282 (Claude Code)`. Test on claude-cli returned `ok:true` through the worker. Ollama showed `available:false` with "ollama has no model configured (see OLLAMA_MODEL) or the GPU worker is off", and anthropic-api and gemini-api showed "no API key configured for this provider".
- Criterion 1: series `01a0dbd2-36db-7a4d-91c6-1138bbb4dd26`, run `01a0dbd2-36e0-7e42-893c-98f7711715d2`. `llm.bible_seed` step `01a0dbd2-36e0-7e44-87aa-7c55ebe38f4f` finished (6 sections), and `llm.outline` step `01a0dbd2-36e0-7e47-bf7f-b00e5ea843e9` produced episode `01a0dbd3-2b65-7269-b5e6-046eeb45d79c` with 9 beats. Nine expand_beat steps (`01a0dbd3-2d40-…` to `01a0dbd9-338f-…`), each 34–58s on claude-cli, produced an EN draft of 15,428 words. The API estimate is 102.85 min, on both the draft and the episode list. Continue was not needed in this run; an earlier run on the same build appended text through 13 expand_beat steps (8,227 words), and the verifier's probe showed Continue working (311 words).
- Criterion 2: 21 translate steps of about 700 words each (`01a0dbd9-ff26-…` to `01a0dbe8-7acd-…`, 26–54s each), applied through apply-step, produced a VI draft of 20,681 words (125.34 min). With the glossary set to Lin Feng→Lâm Phong, Azure Cloud Sect→Thanh Vân Tông and Zhao Yun→Triệu Vân, the VI text contains Lâm Phong 158 times, Thanh Vân Tông once and Triệu Vân 62 times, matching the EN counts, with no English name left untranslated.
- Criterion 3: a 2-chapter GB18030 upload (923 characters), import `01a0dbe8-e0c1-7df4-86e5-a5d937740e00`, was detected as `gb18030` with preset `zh`. Commit run `01a0dbe8-e0cf-7b5a-94dd-a9f0241356fa` translated both chapters on claude-cli (steps `01a0dbe8-e0ce-72ad-…` and `01a0dbe8-e0ce-7e9d-…`). Episodes `01a0dbe8-e0cc-…` and `01a0dbe8-e0ce-72bb-…` each have a `zh` draft (435 and 488 characters) and an EN draft (314 and 336 words, all paragraphs `origin=model` and tainted), for example "The morning mist had not yet cleared when the gates of the Azure Cloud Sect emerged…".
- Criterion 4 (pending): before the switch, shorten ran on claude-cli. After `PUT /settings/llm {default: ollama}`, the next shorten step `01a0dbe9-13be-7983-91c1-cb4624a45083` was queued on the `gpu` queue, which is Ollama's route, and was canceled through `/steps/{id}/cancel`. Test on ollama returned `ok:false` with the reason. After switching back, shorten step `01a0dbe9-61f4-730f-b323-e32a1fef9f71` ran on claude-cli. A second working provider (an Anthropic or Gemini key, or Ollama in phase 9b/9c) is needed to see an action complete on the switched provider.

Checks after these fixes: `scripts/tb.sh gen lint test` green with no generated-code drift; web typecheck, lint, 69 vitest tests, build and bundle budget green; integration suite with `CI=1` on a fresh stack: 63 top-level tests pass, 0 fail, 0 skip; `TestClaudeCLIStatusComesFromTheWorker` also passes with the sidecar running and `LLMCLI_EXPECTED=1`; the live harness passes; both Playwright specs pass. Screen 05 was re-captured and shows the worker's reasons.

Still open from the verifier report (not blocking): M1 in part (an action on an unavailable provider still waits in its queue; Settings now shows the reason and Test fails fast, but `CreateAiAction` does not refuse it), M2 to M10, and the Lows. LLM steps still store an empty `provider_ref`; the provider appears in the step output.

## Follow-ups

- The writer shows the raw step `error_msg`, which exposes internal hostnames (for example, `lookup llm-cli on 127.0.0.11:53`). Map it to a user-safe message and keep the detail in logs.
- With no provider available, resolution still dials the absent `llm-cli` sidecar, and the failure takes about 30s. It should fail fast with "no LLM provider available".
- Add the `claudecli` story integration test and a "Mark reviewed" action (audited, editor role).

## Unresolved questions

- Will an Anthropic or Gemini key be provided so the provider-switch criterion can complete, or does it move to phase 9c together with the Ollama variant?
- Should `CreateAiAction` refuse an action whose resolved provider the worker reports unavailable (the rest of M1)? That needs a new response code in the OpenAPI contract.
- Does the lead accept polling in place of token streaming (M10)?
