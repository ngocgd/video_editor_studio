# Independent verification, round 2: phase 6 (story writer, import, LLM settings)

Date: 2026-09-26 · Branch `feat/story-writer-import` at `acf13ad` · Diff `main...HEAD` (32 commits, 176 files) with a focused review of `082b22b..acf13ad` (the round-1 fixes, 35 files) · Verifier round 2.

**Verdict: pass, ready to merge.** No Critical or High findings remain. Round 1's H1 (long claude-cli generations failed) and H2 (Settings always showed claude-cli unavailable) are fixed and confirmed live. Every automated check is green. Criteria 1–3 were met live with the real claude CLI. Criterion 4 (provider switch) is pending because only one provider can complete an action in this environment. The Ollama variant is deferred to phase 9c by design. Three new Medium findings and one new Low finding are follow-ups; they do not block the merge.

## Checks re-run by the verifier

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` | Pass. golangci-lint reports 0 issues, tenantctx and lint-tenant-queries pass, `go test -race` passes (api and tools), ruff is clean, 26 pytest tests pass |
| Generated-code drift | None. On the host, `git status` is clean and `git diff --exit-code` exits 0 after `gen` |
| Web typecheck, lint, vitest | Pass (15 files, 69 tests) |
| `npx vite build` and `npm run budget-check` | Pass. The writer route chunk is 129.0KB gzip (limit 180KB), the shell is 163.6KB (limit 200KB), and CSS is 6.5KB |
| Integration suite (`-p loomtale-a`, compose.yml + integration overlay, `CI=1`, `-race`) | 63/63 top-level tests pass, with 0 failures and 0 skips |
| Playwright `--workers=1` (compose.yml with the claude-cli profile, seeded owner) | 2/2 pass: `smoke.spec.ts` and `writer-import-settings.spec.ts` |
| `TestClaudeCLIStatusComesFromTheWorker` with `LLMCLI_EXPECTED=1` | Pass (2.65s) on an idle claude-cli stack. An earlier run failed while the Playwright wizard's generations were still in flight (see M-A) |
| Live success criteria (`COMPOSE_PROFILES=claude-cli`, claude CLI `2.1.282 (Claude Code)`) | Criteria 1–3 met. Criterion 4 pending (single provider). Ollama variant deferred. See below |

On the first `up --wait` of the claude-cli stack, `minio-init` exited 1 again, and the retry succeeded. This start-up flake predates phase 6; both stacks came up on retry. Every stack was brought down with `down -v` inside the heavy lock.

## Live success-criteria evidence

The criteria were driven through the public API (`http://127.0.0.1:8080/api/v1`) by a verifier-only Python harness in the verifier's scratchpad, which was never committed. The harness ran as a separately seeded owner ("Live Verify" tenant) on stack `loomtale-a` with `COMPOSE_PROFILES=claude-cli`.

**Settings (AC8, UI half).** `GET /settings/llm/cli-status` returned `installed:true, authenticated:true, toolsDisabled:true, version:"2.1.282 (Claude Code)"`. `GET /settings/llm` listed claude-cli as available. Ollama showed "ollama has no model configured (see OLLAMA_MODEL) or the GPU worker is off", and anthropic-api and gemini-api showed "no API key configured for this provider". Test on claude-cli returned `ok:true` through the worker in about 2.8s. Test on ollama failed fast with its reason. Test returned no `latencyMs` (see M-B).

| Criterion | Status | Evidence |
|---|---|---|
| Settings form → series → bible → ≥1 outlined episode → ≥4,500-word EN draft through Expand/Continue, estimate ≥30 min | **Met** | Series `01a0dbf8-b350-7c9b-a25e-53c800e75785` (30-min target), generate run `01a0dbf8-b367-77f4-b7dd-65a879d5f122`: `llm.bible_seed` `01a0dbf8-b367-77f6-…` and `llm.outline` `01a0dbf8-b367-77f9-…` both done on queue `llm` in 93s. They produced 6 bible sections (`origin=model`) and episode `01a0dbfa-165e-768e-94d2-eaf239c39046` with 10 beats (5,460 target words). Eight `expand_beat` steps (`01a0dbfa-20b1-…` to `01a0dbfc-58bb-…`, 18–24s each, all `provider=claude-cli`, 543–741 words each) and one `continue` step `01a0dbfc-a79d-7972-b85b-810a517440d4` (338 words, 12s) were applied through `apply-step`. Result: EN draft v9 with **5,418 words** and an estimate of **36.12 min**, the same on the draft and in the episode list; all 71 paragraphs are `origin=model` |
| VI draft through Translate, with glossary names applied | **Met** | The glossary section was set to JSON `[林枫 Lin Feng→Lâm Phong, 青云宗 Azure Cloud Sect→Thanh Vân Tông, 赵云 Zhao Yun→Triệu Vân]`. Seven translate steps (`01a0dbfc-d6dc-…` to `01a0dc01-d245-…`, 32–56s each on claude-cli, about 800 EN words per call) were applied to the `vi` draft, giving VI draft v7 with **7,343 words** (44.5 min). Name counts: Lin Feng 55 in EN and Lâm Phong 55 in VI; Zhao Yun 44 and Triệu Vân 44; Azure Cloud Sect 2 and Thanh Vân Tông 3. **No English name is left** in the VI text |
| GB18030 Chinese chapter import → episodes → translated EN draft | **Met** | A 2-chapter GB18030 file (1,431 bytes, 724 characters, not valid UTF-8) was uploaded through presign → MinIO → finalize. Import `01a0dc02-5030-76d5-8126-0fa6881ed7b1` was detected as `gb18030` with preset `zh` and 2 chapters (第一章 山门, 367 chars; 第二章 外门, 336 chars). Commit run `01a0dc02-5043-7728-acd5-3b4b6a36f50d` ran translate steps `01a0dc02-5041-7f17-…` and `01a0dc02-5042-7afe-…` on claude-cli. Episodes `01a0dc02-5040-…` and `01a0dc02-5041-…` each have a tainted `zh` draft and an EN draft (263 and 244 words, all paragraphs `origin=model, tainted=true`), for example "The morning mist had not yet cleared when the mountain gate of the Azure Cloud Sect appeared…" |
| Switching the provider in Settings changes the provider shown on the next AI action | **Pending (external)** | Only claude-cli can complete an action here. Ollama has no model until phase 9b, and no Anthropic or Gemini key exists. The switch itself is observable. Shorten step `01a0dc02-73e1-…` ran on `provider=claude-cli`. After `PUT /settings/llm {default: ollama}`, the next shorten step `01a0dc02-83d4-73d0-bf5d-dd0ec247393e` was queued on `gpu` (Ollama's route) and stayed `queued`; it was canceled (still round-1 M1). After switching back, step `01a0dc02-a338-…` ran on claude-cli. Completing this criterion needs a second working provider |
| Ollama variant | Deferred to phase 9c by design | none |

## Round-1 blockers re-checked

- **H1 (fixed).** `api/cmd/llmcli/handler.go` now commits `200` and flushes the headers before the CLI runs, and every later failure travels in-band in the `result` line. The claude-cli adapter uses `SidecarHTTPClient()`, which keeps a dial timeout but has no response-header timeout. Unit tests cover a silent 3s CLI behind a 1s header timeout, and a check that `Build` wires the adapter to this client. Live: `bible_seed` took about 45s and translate calls took up to 56s, and all of them completed.
- **H2 (fixed).** The worker probes the sidecar's `/healthz` (with the `X-Claude-CLI-Version` header) on every 5s heartbeat and publishes the result in `worker_status.providers`. Settings reads availability and CLI status from that heartbeat. Test runs as an `llm.check.<provider>` step in the worker, so the api no longer needs a route to `llm_net`. Confirmed live, as above.
- **JSON schema and duration estimate fixes.** `GenerateStructured` appends the fixed schema to `System`. This is a server constant, so no story text enters `System`. The outline came back valid on the first try (10 beats). `durationEstimateMinutes` is filled for en/vi drafts and for the episode list.
- **Trusted Types race.** `main.tsx` awaits the policy before the first render. The Playwright writer spec, including its hard reload, passes.

## Medium (new)

### M-A. Test waits behind running generations, and the CLI-status integration test fails on a busy stack
`api/cmd/worker/main.go:144` (`llm` queue `MaxWorkers: WORKER_LLM_CONCURRENCY`, default 5), `api/cmd/llmcli/run.go:68` (sidecar semaphore, `LLMCLI_MAX_CONCURRENT=2`), `api/internal/settingsapi/routes.go` (`testCallTimeout = 30s`), `api/internal/integration/llm_provider_status_test.go:90` (the client timeout is `httpTimeout = 10s`).
- The probe step is claimed immediately, but inside the sidecar it queues behind any two in-flight generations. These can take 20–60s each. A healthy claude-cli is then reported as "the worker did not answer in time", or Test simply takes up to 30s.
- Reproduced: with the Playwright wizard's `bible_seed` and outline still running on the same stack, `TestClaudeCLIStatusComesFromTheWorker` failed with `Client.Timeout exceeded while awaiting headers` on `POST /settings/llm/test`. On an idle stack it passed in 2.65s.
- Fix: give the test a client timeout above `testCallTimeout`, or wait for LLM steps to finish first. In the product, consider letting a probe skip the sidecar semaphore (it is 8 tokens), or report a busy provider as "busy" rather than failed.

### M-B. Test never reports latency
`api/internal/settingsapi/routes.go` `TestLLMSettings` never sets `LatencyMs`, although `LLMSettingsTestResult.latencyMs` exists and `llm-provider-card.tsx:64` renders it. The phase requires "a Test button with the latency result". Live responses were `{"ok":true,"provider":"claude-cli"}`. Fix: time `Probe.Check` (or the step's `started_at`/`finished_at`) and return it.

### M-C. The tenant-query lint does not cover any of the new story tables
`scripts/lint-tenant-queries.sh` `TENANT_TABLES` still lists only the phase 2/3 tables. `series`, `story_bibles`, `episodes`, `episode_drafts`, `episode_draft_revisions` and `imports` are unchecked, so the "lint-tenant-queries: OK" result says nothing about them. Today every story query except `TrimDraftRevisions` filters on `tenant_id`, and that one is keyed by a `draft_id` the caller has already loaded under a tenant filter, so nothing is exploitable now. Fix: add the six tables, and add `tenant_id = @tenant_id` to `TrimDraftRevisions` (or an allow comment).

## Low (new)

- `claudecli.Stream` (`api/internal/providers/llm/claudecli/claudecli.go:132-160`) returns success with empty text when the NDJSON stream ends cleanly without a `result` line. Now that the sidecar commits `200` up front, a sidecar that exits mid-run can produce such a stream. The step would then be `done` with nothing to apply. Treat a missing `result` line as an error.
- Operational: every lane builds the same image tags (`loomtale/api:local`, `loomtale/worker:local`, `loomtale/web:local`). Each heavy-lock run rebuilds with `--build`, so this is safe under the lock. A `compose up` without `--build` would, however, run whichever lane built last.

## Still open from round 1 (not blocking; the lead accepted them as follow-ups in the cook report)

- M1: an action on an unavailable provider waits in its queue indefinitely. Re-observed live: step `01a0dc02-83d4-…` stayed `queued` on `gpu`. `PUT /settings/llm` accepts an unavailable default without a warning.
- M2 (double apply of inserting steps), M3 (the bible seed stores the glossary as free text, while the UI and phase expect a JSON array; the harness had to overwrite it), M4 (GB18030 fallback guesses, a preface before the first heading is dropped, per-rune offset map), M5 (unbounded, non-transactional draft writes), M6 (outline index race, 500-step fan-out), M7 (bible editor gaps), M8 (4,000-token cap on the Anthropic/Gemini paths), M9 (the `zh` draft cannot be opened in the writer), and M10 (polling instead of token streaming; the proposal is docked rather than inline; no LLM-level canary summary test).
- Lows: raw `error_msg` (internal hostnames) is shown to viewers and in the Test detail; LLM steps have an empty `provider_ref`; `PutLLMApiKey` accepts keyless providers; viewers see mutating controls outside the writer.

## Checks that passed

- Tenant isolation: all new story queries filter on `tenant_id` (except `TrimDraftRevisions`, see M-C). Composite `(tenant_id, parent)` foreign keys are in place. The AI-result and apply-step endpoints require an AI-action step on the same episode.
- RBAC (`x-min-role`): reads need viewer; story mutations and Test need editor; `PUT /settings/llm` and key writes need owner.
- Secrets: BYOK keys are sealed and write-only. The claude OAuth token stays in the sidecar, which only the worker can reach. The probe prompt is a fixed server string. No secret appears in the reviewed code paths or logs (secret files were checked by size only).
- SSRF and injection: import reads MinIO by the stored key. Story content is passed only as nonce-fenced data blocks, and the schema appended to `System` is a server constant.
- Provenance: imported and translated text is `tainted`, and generated text is `origin=model` (confirmed live).

## Blocking items

None.

## Unresolved questions

1. Will an Anthropic or Gemini key be provided so the provider-switch criterion can complete, or does it move to phase 9c together with the Ollama variant?
2. Should M1 (refuse or fail fast an action whose resolved provider is unavailable) be fixed before phase 7 builds more LLM actions on the same path?
3. Does the lead accept M10 (polling instead of token streaming, docked proposal) as the permanent design?
