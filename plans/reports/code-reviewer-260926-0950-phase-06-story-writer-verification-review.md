# Independent verification: phase 6 (story writer, import, LLM settings)

Date: 2026-09-26 · Branch `feat/story-writer-import` at `082b22b` · Diff `main...HEAD` (21 commits, 154 files, merge base `5288d3c`) · Verifier round 1.

**Verdict: do not merge.** No Critical findings. There are 2 High findings, and both block the live success criteria. All automated checks are green. With the real claude CLI, only short actions (under about 15 seconds) complete. The series wizard, and every generation long enough to build a 4,500-word draft, fail.

## Checks re-run by the verifier

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test vuln audit` | Pass. golangci-lint reports 0 issues, tenantctx and lint-tenant-queries are OK, `go test -race` passes, 26 pytest tests pass, govulncheck finds 0 reachable vulnerabilities, and pip-audit and npm audit are clean |
| Generated-code drift | None. `git status` and `git diff --exit-code` on the host are clean after `gen` |
| Web typecheck, lint, vitest | Pass (15 files, 69 tests) |
| `npx vite build` and `npm run budget-check` | Pass. The writer route chunk is 129.0KB gzip (limit 180KB) and the shell is 163.7KB (limit 200KB) |
| Integration suite (`-p loomtale-a`, `CI=1` so nothing can skip, `-race`) | 62/62 top-level tests pass, with 0 failures and 0 skips |
| Playwright `--workers=1` (plain `compose.yml`, seeded owner) | 2/2 pass: `smoke.spec.ts` and `writer-import-settings.spec.ts` |
| Live success criteria (`COMPOSE_PROFILES=claude-cli`, claude CLI 2.1.282, model `claude-sonnet-5`) | **Failed.** See below |

On the first `up --wait`, `minio-init` raced MinIO twice ("connection refused" while MinIO reported healthy), and a retry fixed it. This start-up flake predates phase 6, but it matters for anyone running the suite: without `CI=1`, a failed `up` makes every integration test *skip* and the suite still reports success.

## Live success-criteria evidence

These criteria were driven through the public API by a verifier-only Go harness. It uses the `integration,livestory` build tags, was copied into the worktree only while it ran, and was never committed.

| Criterion | Status | Evidence |
|---|---|---|
| Settings form → series → bible → ≥1 outlined episode → ≥4,500-word EN draft through Expand/Continue, with the estimate ≥30 min | **Failed** | Series `01a0db8b-562f-7e1f-909f-12020280970e` was created and run `01a0db8b-5636-76b4-a112-b0697e9f2f0c` was started. Step `01a0db8b-5636-76b6-bd12-a5703f6144cd` (`llm.bible_seed`) failed with `claudecli: sidecar request failed: Post "http://llm-cli:8090/v1/run": net/http: timeout awaiting response headers`, so the dependent `llm.outline` step was canceled. No outline was produced, so the draft could not be reached (see H1) |
| VI draft through Translate, with glossary names applied | **Not reached at scale** (blocked by H1) | A short probe did pass. Translate step `01a0db90-4956-7f3b-b31e-2db872d12f7f` (claude-cli, 4s) turned "Lin Feng … Azure Cloud Sect" into "Lâm Phong … Thanh Vân Tông" |
| GB18030 chapter import → episodes → translated EN draft | **Met for a small chapter.** A realistic chapter is blocked by H1 | Import `01a0db90-8815-7323-aee4-ced033428bc6` was detected as `gb18030` with preset `zh` and 2 chapters. Run `01a0db90-8823-717e-a06b-58fcfe78d1a6` ran translate steps `…8821-7cba…` and `…8822-75fe…`, both done on claude-cli. Each episode has a `zh` draft (18 and 15 words) and an EN draft (14 and 9 words) with `tainted:true`, for example "Lin Feng stood before the mountain gate of Qingyun Sect." |
| Switching the provider in Settings changes the next action's provider | **Pending, but a defect was found** | Only claude-cli works: Ollama has no model until phase 9b, and there are no Anthropic or Gemini keys. Before the switch, shorten step `01a0db90-3994-7bee-b5c7-64536108bae5` ran on `provider=claude-cli`. After `PUT /settings/llm {default: ollama}`, the next shorten step `01a0db90-9fb6-77c6-9d9f-5c82f0f419b6` never reached a terminal state in 12 minutes (see M1) |
| Ollama variant | Pending by design (phase 9c) | none |

Other probe evidence: continue step `01a0db90-5907-7eeb-a086-c089b367f4c3` produced 311 words in 12s. The accepted shorten was stored with `origin=model`.

## High

### H1. The claude-cli path fails any generation whose output takes longer than 15 seconds
`api/internal/providers/bootstrap/bootstrap.go:36-73` (`providerResponseHeaderTimeout = 15s`, now also used by the worker's claude-cli adapter), `api/cmd/llmcli/main.go` (`writeJSONLine` sends headers only with the first NDJSON line), `api/cmd/llmcli/argv.go` (`-p --output-format stream-json` without `--include-partial-messages`).
- The sidecar does not write the response status and headers until the CLI emits its first stream-json line. Without partial messages, that line arrives when the whole assistant message is done. The worker's HTTP client therefore gives up after 15s, before any header arrives.
- Failure scenario (reproduced live): any `llm.bible_seed` step, and so the whole series wizard, fails. So does any expand_beat, continue or translate that produces more than a few hundred words, which rules out the 4,500-word draft and a full-episode VI translation. Short actions (4–12s) succeed.
- Fix: have the sidecar send `200` and flush headers as soon as it accepts the run (it already reports errors in-band through the `result` event), and/or give the claude-cli adapter a client without `ResponseHeaderTimeout`, bounded by the context and the sidecar's own `LLMCLI_TIMEOUT` instead. Consider `--include-partial-messages` so the progress heuristic and a future token stream get real deltas. Then re-run the live criteria.

### H2. Settings > LLM always reports the claude CLI as not installed and claude-cli as unavailable, even when it works
`deploy/compose.yml` (`api` is on `loomtale_core` only, and `llm-cli` is on `loomtale_llm`), `api/cmd/api/main.go:207` (the status client dials `llm-cli:8090` from the api), `api/internal/settingsapi/handler.go:159-165` (non-Ollama availability comes from the *api* process's own `Registry.Providers`, and the api has no llm-cli bearer token).
- Live evidence while claude-cli was serving the worker: `GET /settings/llm/cli-status` returned `installed:false, authenticated:false, detail:"llm-cli sidecar not reachable"`. `GET /settings/llm` listed claude-cli with `available:false` ("no adapter constructed…"). The Test button runs in the api, so it cannot succeed for claude-cli either.
- This breaks the phase requirement for "claude CLI status (version, auth state, Tools disabled)" and the UI half of AC8. A user sees the one working provider marked unavailable.
- Fix: report claude-cli availability and status from the worker's heartbeat (`worker_status`, as Ollama already does). Have the worker probe the sidecar's `/healthz` and CLI version, and route Test through a step or the worker. Alternatively, put the api on `loomtale_llm` with its own bearer token, but that widens the sidecar's exposure. Add an integration assertion that the status reflects a running sidecar.

## Medium

- **M1. An action on an unavailable provider never finishes.** After switching the default to Ollama with no model, the shorten step `01a0db90-9fb6-…` stayed non-terminal for 12 minutes and the writer would show "Generating…" indefinitely. `PUT /settings/llm` accepts any known provider, even one reported `available:false`. Fail fast at enqueue with "provider unavailable", or have the handler return an honest error that is not retried. Also warn in Settings before saving an unavailable default.
- **M2. Accepting an inserting step twice applies it twice.** `ApplyDraftStep` (`handlers_drafts.go:88-228`) has no idempotency. A second Tab while the first apply is in flight, or a retried POST, duplicates Continue, expand-beat and Translate text with new ids. Replace actions are safe, because the second call gets 409. Fix: record applied step ids (for example `applied_step_id` in the step output or a small table, updated conditionally) and return the current draft or 409 on a repeat. Also guard Accept in `writer-view.tsx` while a request is in flight.
- **M3. The glossary format does not match between generation and the UI.** `bible_seed` stores `glossary` as free text (`schemas.go` bibleSeedSchema `"glossary": {"type":"string"}`), but `GlossaryTable` expects a JSON array (`glossary-table.tsx` `parseGlossary`). A seeded glossary therefore renders as an empty table, and saving the table replaces the generated glossary with only the rows the user typed. Fix: make the seed schema an array of `{termZh,en,vi}` and store it JSON-encoded, or show unparseable content as text instead of an empty table.
- **M4. Import still guesses encodings, drops text, and is memory-heavy** (the earlier review's M2 is still open). `importer/encoding.go:68` accepts any GB18030-decodable stream, so Windows-1252 prose becomes silent mojibake, which breaks "decoding errors reported, not guessed". `split.go` `splitWithPattern` drops everything before the first heading (a preface). `byteOffsetToRuneOffset` builds a map entry per rune, roughly 5M entries for a 5MiB upload, on every preview. Fix: require a CJK-ratio threshold for GB18030, emit a preface chapter, and convert offsets with a running counter instead of a map.
- **M5. Draft writes are unbounded and not transactional** (the earlier review's M4 is still open). `ParagraphOp.text` and `paragraphId` have no `maxLength`, there is no cap on paragraph count, and the draft update, revision insert and trim are three separate statements. A revision is also written on every autosave, so 50 revisions cover only minutes of typing.
- **M6. Outline index race and fan-out** (the earlier review's M5 is still open). `runOutline` reads `NextEpisodeIdx` before a multi-second LLM call, so sibling outline steps collide on `(series_id, idx)`, fail after spending tokens, and retry. `GenerateSeries` allows 500 interactive-priority outline steps.
- **M7. Bible editor gaps** (the earlier review's M7 is still open). A series created without the wizard has no sections, so there is nothing to edit. After a conflict, "Reload latest" keeps the stale textarea, because the editor is keyed by section rather than version, and the next Save overwrites the other writer's text at the new version.
- **M8. A 4,000-token output cap on every episode action.** `ai_actions.go:441` (and `:127`, `:168`) hard-codes `MaxTokens: 4000`. The writer offers whole-draft Translate when nothing is selected. On the Anthropic and Gemini adapters, which return `ErrMaxTokensTruncated`, translating a 4,500-word draft fails. The claude-cli adapter ignores the cap. Fix: chunk long translations server-side by paragraph groups, or size the limit from the input.
- **M9. An imported Chinese source cannot be opened in the writer.** The language toggle offers only en/vi, so the `zh` draft is unreachable, and ZH→EN/VI translation exists only at import commit. The phase lists "Translate … ZH→EN/VI" as an AI action.
- **M10. Spec deviations need the lead's acceptance.** There is no token streaming over SSE: results are polled, so the "first token ≤3s" budget cannot be measured and "Waiting for GPU slot (#n)" is not shown. The proposal is docked below the editor rather than shown as inline decorations. Outline at episode scope returns 404. The security checklist's "canary planted in an imported chapter must not surface via a later summary" has no LLM-level test; only the storyctx fencing unit test and the taint-propagation integration test exist.

## Low

- `storyctx.truncateRunes` trims continuation bytes but keeps the orphaned lead byte of a cut rune, which leaves invalid UTF-8 at the cut for CJK text.
- LLM steps are stored with an empty `provider_ref` (all live steps show `provider_ref=`), so the Jobs UI cannot show which provider ran a step.
- `GetAiActionResult` returns the raw `error_msg` to viewers. It includes internal URLs, for example `http://llm-cli:8090/v1/run`.
- `zh` drafts use the 150-wpm fallback for a per-character count, so the duration estimate is meaningless for them.
- `PutLLMApiKey` accepts `ollama` and `claude-cli`, which never use a key.
- Carried from the earlier review: viewers see mutating controls outside the writer, the kin-openapi validation error may echo a submitted key into logs, and the worker mounting the master KEK should be recorded as an accepted risk.

## Checks that passed

- Tenant isolation: every new query filters by `tenant_id`. The composite `(tenant_id, parent)` foreign keys are in place. `CreateEpisode`, `CreateImport` and `CommitImport` check series ownership. `GetAiActionResult` and `ApplyDraftStep` require an AI-action step on that same episode.
- RBAC: reads need viewer, mutations need editor, and settings and key writes need owner.
- BYOK: keys are sealed with tenant/kind/provider AAD, are write-only, and are never logged. There is no SSRF surface: import reads MinIO by the stored key and pinned version. No story text is placed in `System`.
- Fixes from the previous review re-checked in the code: C1 (split ids), C2, H1 (server-side apply-step with taint and `origin=model`, confirmed live), H3, H4, H5 (create-draft, `zh` source draft, auto-applied import translation, confirmed live), H6, H7 and H8.

## Blocking items for round 2

1. H1: make long claude-cli generations complete, then re-run the live criteria: series → bible → outline → ≥4,500-word EN draft with ≥30 min, then a VI draft with glossary names.
2. H2: make Settings show the real claude-cli status and availability.
3. The provider-switch criterion stays pending until a second provider can complete an action. M1 should still be fixed so the switched action fails visibly instead of hanging.

## Unresolved questions

1. Does the lead accept polling in place of token streaming (M10), or should the SSE `llm.delta` path be built in this phase?
2. For the AC8 UI evidence, will an Anthropic or Gemini key be provided, or does the provider-switch criterion move to phase 9c alongside the Ollama variant?
