# Independent verification: characters, storyboard and scene editor (round 9)

Branch `feat/characters-storyboard` at `55cc1af` (worktree `.claude/worktrees/lane-a-7`). That head already contains `main` at `a68f5f6` (merge commit `55cc1af`, made by the fixer), so no further merge was needed before verifying. The branch was reviewed as `git diff main...HEAD`. The code changes since round 8 are `c761faf` (lenient roster-name resolution and a tighter `scene_split` prompt) and `216955f` (unrecognised speaker names recorded in the split step output), plus the conflict resolutions in the main merge.

**Verdict: ready to merge.** The round 8 blocker (H1, a live split that attributed no dialogue) is fixed and its acceptance is met: three consecutive live splits on fresh stacks attributed dialogue to both characters with zero unrecognised speakers. The toolbox, generated-code drift check, web checks, integration suite and default Playwright suite are all green. No Critical or High finding remains. One new Medium finding about migration ordering is recorded below; it is systemic to the team's migration ranges and does not block this merge on its own.

## Checks run this round

| Check | Result |
|---|---|
| `scripts/tb.sh gen` | Exit 0. `git status --porcelain` and `git diff` were empty afterwards, so there is no generated-code drift |
| `scripts/tb.sh lint test` (with `-race`) | Exit 0. golangci-lint reported 0 issues, ruff "All checks passed!", 44 `ok` Go packages, no FAIL and no DATA RACE. Pytest: 108 passed, 5 skipped |
| Web `typecheck`, `lint`, `npm test`, `npx vite build`, `budget-check` | All exit 0. Vitest: 95 tests passed. "Bundle budget check passed", CSS 6.94 KB |
| Integration (`loomtale-a`, heavy lock `a-verify9`, fresh `up -d --wait --build`) | **84 passed, 0 failed, 0 skipped** in 104 s. The phase 7 migrations applied to a real Postgres |
| Playwright, `compose.yml` only, default limits, `--workers=1`, fresh stack | **4 passed** (18.7 s). The api log has 0 responses of 429 (139 × 200, 14 × 201, 6 × 401, 3 × 404, no 429). Grid scroll 60.0 fps with p95 18.0 ms and at most 14 tiles mounted; keyboard move median 1.9 ms (max 4.1 ms); timeline pan/zoom 60.0 fps with p95 17.6 ms |
| Live split 1 (`COMPOSE_PROFILES=claude-cli`, `-run TestLiveLLMSceneSplit`, fresh stack) | **PASS.** 6,739 words, step `01a0de9b-026f-74a7-9771-a0dcd1b86de9`, 100 scenes, 0 unrecognised speakers. Lin Mo 144, Elder Qiu 199 |
| Live split 2 (fresh stack) | **PASS.** 6,371 words, step `01a0dea5-33e6-766c-9e71-b3a40e63ef21`, 85 scenes, 0 unrecognised speakers. Lin Mo 128, Elder Qiu 188 |
| Live split 3 (fresh stack) | **PASS.** 6,656 words, step `01a0deb0-56e2-7114-ab91-065a577d3e8f`, 76 scenes, 0 unrecognised speakers. Lin Mo 120, Elder Qiu 178 |

Every stack was torn down with `down -v --remove-orphans`; afterwards no `loomtale-a` container or volume remained. The e2e run's rewritten screenshots were restored with `git checkout`, and the worktree was clean before the report was added.

## Findings

### Critical

None.

### High

None. Round 8's H1 is closed:

- `NameIndex.Resolve` (`api/internal/scenes/split.go`) first tries the exact folded name, then splits roster-style forms (`" / "` or full-width slash separated names, a trailing parenthesised role or alias, a leading list marker) and accepts a match only when every known part points at the same character. Parts that disagree (`Lin Mo / Elder Qiu`, `Lin Mo (Elder Qiu)`) resolve to nobody, so the change cannot mis-attribute a line to the wrong character. Empty parts cannot match, because `NewNameIndex` skips empty keys.
- A string that looks like a UUID is still treated only as a name, so the "names, never ids" rule holds.
- The `scene_split` system prompt now describes the roster line format and asks for exactly one name without the role.
- Unit tests cover the whole-line, role-suffixed, alias-in-parentheses, full-width and conflicting forms, both in `Resolve` and through `NormalizeLLMSplit`.
- The split step output now carries `unrecognisedNames`: at most 20 distinct names, each already bounded to 100 characters by the output schema, so a future failure can be diagnosed from the step itself.

### Medium

- **M1 (open since round 1, unchanged). The image step ignores inputs that its stale hash includes.** `scenes/inputs.go` hashes the style's negative prompt and sampler and each character's negative prompt, but `scenes/steps_image.go` sends none of them. Editing a negative prompt therefore marks images stale, while regenerating cannot change the result. Not blocking.
- **M2 (new, systemic). Lower-numbered migrations merged after a higher one are refused by goose on an existing database.**
  - `main` gained `20260927400000_draft_step_applications.sql` at 22:49 (and has since gained lane c's `202609273xxxxx` migrations the same way). This branch's migrations are `20260926300000` to `20260926300300`.
  - `api/cmd/loomtale/migrate.go` calls `goose.UpContext` without `goose.WithAllowMissing()`. In goose v3.24.1 (`up.go`), `UpToContext` returns "found N missing migrations before current version" when a database already has a higher version applied.
  - Effect: any database that applied `main`'s newer migration before this branch merges (a developer's long-lived `loomtale` stack, for example) will refuse to migrate after the merge until someone intervenes. Fresh volumes, as used by integration and e2e, are unaffected, which is why every suite passes. There is no data loss; the migrate step fails loudly.
  - Fix directions (a team decision, asked on the board to the lead): renumber migrations that merge later above the current `main` maximum, or opt into `goose.WithAllowMissing()` for this pre-release project.

### Low (not blocking, unchanged)

- L1–L4 as listed in round 8: `discardWork` stored in the split step input; the media bucket applied before the session middleware; no double-submit guard on `GenerateMissing`; `scenes.Changed` on a split lists only the kept ids; races between take selection and `RecordTake` and on the LoRA version; assets orphaned when takes cascade on a re-split; no GIN index on `character_ids`; `voiceparams.isFloat` accepts NaN and Inf.
- L5: `UpdateScene` accepts `segments` and `narration` together without checking that they agree.
- L6 (new, cosmetic): `Resolve` treats text inside a trailing parenthesis as a name. A speaker such as `Lin Mo (Elder Qiu's disciple)` still resolves correctly, but `Lin Mo (Elder Qiu)` resolves to nobody. That falls back safely to the narrator with a flag.

## Independent review (re-checked this round)

- **Authorization.** All 32 operations in `openapi/paths/{characters,scenes,presets,media}.yaml` declare `x-min-role` (32 operations, 32 declarations): 8 viewer, 23 editor, 1 owner. RBAC is deny-by-default for a missing extension.
- **Tenant isolation.** Every named query in `db/queries/{scenes,takes,characters,presets,media}.sql` references `tenant_id` (checked per query block). The merged `scripts/lint-tenant-queries.sh` `TENANT_TABLES` is the union of both sides (phase 7 tables and main's story, LLM settings and `draft_step_applications` tables), and the tenant lint passed in `tb.sh lint`.
- **Merge with main (`55cc1af`).** The conflicted files were resolved correctly: the generated `models.go`, `querier.go` and `server.gen.go` match a fresh `tb.sh gen` (no drift); `pipeline/worker.go` keeps both timeout overrides (`llm.scene_split` 30 min and `models.pull` 3 h); the lint script keeps both table lists.
- **Voice cloning consent.** `presets.saveVoicePreset` stores a reference voice only with a recorded consent and writes the audit entry; the integration consent test passed.
- **Injection, SSRF and data loss.** Unchanged since round 8 and still covered by passing unit and integration tests: typed, allowlisted ffmpeg argv with the single protocol whitelist, Go-side host and temp-dir checks, and a re-split that locks the episode's scenes and refuses to drop edited work without `discardWork`.
- **Board.** No CHANGE line from another lane touches this branch's files. Lane g asked lane a about `TestInteractiveStepWaitsAtMostOneChunkBehindBatch`; the helper's `testQueue = pipeline.QueueRender` is only served by a live worker from phase 8 on, so phase 7 is unaffected, and the answer on the board sends the fix to the phase 8 branch. `main` has moved to `cfe5f1a` (YouTube channels) since this verification, so the merger must merge `main` again and re-run the checks its merge touches.

## Success criteria

| Criterion | Status |
|---|---|
| A 6k-word draft splits into scenes, with dialogue attributed to ≥2 characters that have voices assigned | **Met.** Three consecutive live runs: 100, 85 and 76 scenes, Lin Mo 144/128/120 and Elder Qiu 199/188/178, 0 unrecognised speakers each |
| Editing one scene's narration marks only that scene's voice, align and render pieces stale; regenerating queues exactly one GPU step | **Met** for voice and align (`TestStoryboardSplitStepsStaleAndTakes` passed). Render pieces arrive with the render pipeline phase |
| The grid and timeline meet the 60fps budgets with 300+ scenes | **Met** at 450 scenes (60.0 fps averages, p95 ≤ 18.0 ms, ≤ 14 tiles mounted, keyboard median 1.9 ms) |
| Ollama variant | **Pending by design.** The plan records it in the LoRA/scoring phase report after phase 9b; it also needs local model weights, and model downloads are paused by the user |

## Blocking items

None. The branch can merge into `main` under the merge lock after `main` (`cfe5f1a` or later) is merged in and the affected checks are re-run.

## Unresolved questions

- M2: should later-merging lanes renumber their migrations above the current `main` maximum, or should `migrate` opt into goose's allow-missing mode? This affects lanes b, c and a alike and needs a lead decision.
- Should the e2e specs keep rewriting the tracked `reports/phase-0*-screens` screenshots on every run? Each verification run still dirties the working tree.
- Lane a's fixer run `a-fixsplit` (queued before this round) is still executing the same checks on the same head under the heavy lock; its results are redundant with this report.
