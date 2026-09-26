# Independent verification: characters, storyboard and scene editor (round 8)

Branch `feat/characters-storyboard` at `fdc1308` (worktree `.claude/worktrees/lane-a-7`). Before verifying, I merged `main` at `a20fd7c`, which carries the scoring, depth and trainer engines. The merge was clean and made merge commit `73ee21d`. The branch was reviewed as `git diff main...HEAD`. The only code change since round 7 is `0f13cf6`, which raises the general per-IP request budget.

**Verdict: not ready to merge.** The round 7 blocker (H1, e2e 429s) is fixed: the default Playwright run passed 4 of 4 with zero 429s. The toolbox, web checks and integration suite are green. However, the first live scene split of this round failed. The model's split attributed none of the 304 dialogue lines to a character. A re-run passed. Counting round 7, the split on this code has now failed 1 of 3 live runs, and when it fails it loses every speaker. I rate this High (H1 below).

## Checks run this round

| Check | Result |
|---|---|
| `git merge main` (`a20fd7c`) | Clean; merge commit `73ee21d` |
| `scripts/tb.sh gen` | Exit 0. `git status` is clean afterwards, so there is no generated-code drift after the merge |
| `scripts/tb.sh lint test` (with `-race`) | Exit 0. golangci-lint reported 0 issues and ruff "All checks passed!". 43 `ok` Go packages, with no FAIL and no DATA RACE. Pytest: 102 passed, 5 skipped |
| Web `typecheck`, `lint`, `npm test`, `npx vite build`, `budget-check` | All exit 0. Vitest: 19 files, 91 tests. Budget passed (CSS 6.94 KB) |
| Integration (`loomtale-a`, heavy lock, fresh `up -d --wait --build`) | **76 passed, 0 failed, 0 skipped** (47.5 s). The migrations applied to a real Postgres. The storyboard, re-split guard, media variants, 400-scene rollup, voice clone consent and cross-tenant tests all passed |
| Live split, first run (`COMPOSE_PROFILES=claude-cli`, `-run TestLiveLLMSceneSplit`) | **FAIL.** The draft grew to 6,574 words. Step `01a0de56-0ad9-7cb0-b195-8b1d032a42d8` produced 78 scenes with **304 unrecognised speakers**: Lin Mo 0, Elder Qiu 0 |
| Live split, re-run on a fresh stack (heavy lock) | **PASS.** The draft grew to 6,531 words. Step `01a0de77-a64f-7b8a-bc5c-be969c29e103` produced 76 scenes with 0 unrecognised speakers: Lin Mo 132, Elder Qiu 175, narrator 156 segments (read back from `scenes.segments`) |
| Playwright, `compose.yml` only, default limits, `--workers=1`, fresh stack | **4 passed.** The api log has 0 responses of 429. Grid scroll averaged 16.67 ms/frame (60.0 fps), with p95 18.0 ms and at most 14 tiles mounted. The timeline also averaged 60.0 fps, with p95 17.7 ms. Keyboard moves had a median of 1.9 ms |

Every stack was torn down with `down -v`. The e2e run rewrote the tracked screenshots under `reports/phase-06-screens` and `reports/phase-07-screens`, and I restored them with `git checkout`.

## Findings

### Critical

None.

### High

- **H1. The LLM scene split sometimes attributes no dialogue at all, because speaker names must match the roster exactly while the roster shows every name on one line.**
  - **Evidence.**
    - The first live run this round flagged all 304 quoted lines as unrecognised, and no segment got a character. That run is step `01a0de56-…`.
    - The re-run on unchanged code resolved every line, and round 7 did too. The failure rate is therefore 1 in 3 live runs.
    - The failure is all or nothing. That points to a systematic format mismatch rather than a few misread lines.
  - **Likely cause (not confirmed; the failing run's names were lost with its stack).**
    - `rosterText` (`api/internal/scenes/steps_split.go`) writes each character as one line, `- Lin Mo / 林默 / Lâm Mặc (role)`.
    - The `scene_split` system prompt (`api/internal/storyctx/templates.go`) asks the model to name each speaker "exactly as in the roster".
    - `NameIndex.Resolve` (`api/internal/scenes/split.go`) accepts only an exact folded single name. It rejects `Lin Mo / 林默 / Lâm Mặc` and `Lin Mo (disciple)`.
    - So a model that copies the whole roster line, which is a literal reading of the prompt, loses every attribution.
  - **Effect.** The user gets a storyboard where every line is voiced by the narrator, with hundreds of manual speaker assignments to fix. The phase's first success criterion is flaky.
  - **Fix directions.**
    - Resolve speaker and character names leniently: accept any of the " / "-separated names and ignore a trailing parenthesised role.
    - Or change the roster format and the prompt so that one canonical name per character is unambiguous.
    - Either way, add unit cases for the whole-line and role-suffixed forms in `NormalizeLLMSplit`.
    - Also record the distinct unrecognised names (or a sample of them) in the step output or log, so the next failure can be diagnosed.
  - **Acceptance.**
    - The unit tests cover the forms above.
    - Two consecutive live split runs attribute dialogue to both characters.

### Medium

- **M1 (open since round 1). The image step ignores inputs that its stale hash includes.**
  - `scenes/inputs.go` `ImageComponents` hashes the style's `NegativePrompt` and `Sampler` and each character's `NegativePrompt`.
  - `scenes/steps_image.go` sends none of them. So editing a negative prompt marks the images stale, but regenerating them cannot change the result.
  - This does not block the merge on its own.

### Low (not blocking, unchanged)

- L1–L4:
  - A confirmed split stores `discardWork` in the step input.
  - The media bucket is applied before the session middleware.
  - `GenerateMissing` has no double-submit guard.
  - `scenes.Changed` on a split lists only the kept ids.
  - There are races between take selection and `RecordTake`, and on the LoRA version.
  - Assets can be orphaned when takes cascade on a re-split.
  - There is no GIN index on `character_ids`.
  - `voiceparams.isFloat` accepts NaN and Inf.
- L5: `UpdateScene` accepts `segments` and `narration` together without checking that they agree.

## Independent review (re-checked this round)

- **Rate-limit fix (`0f13cf6`).**
  - The default `API_RATE_LIMIT_PER_MINUTE` is now 600 in `api/cmd/api/config.go` and `deploy/compose.yml`.
  - The limiter construction moved into `newRequestLimiters`. It still rejects budgets of zero or less, and it has unit tests.
  - Login brute-force protection does not depend on this bucket. `authapi` keeps its own DB-backed buckets: per IP+email 5/min, per IP 20/hour, per account 10/hour (`api/cmd/api/main.go` lines 255–256).
  - `.env` does not override the key, so the e2e run used the real default.
  - This is acceptable for a local-first, single-user install.
- **Authorization.**
  - All 32 new operations in `openapi/paths/{characters,scenes,presets,media}.yaml` declare `x-min-role`: 8 viewer (all GETs), 23 editor and 1 owner (`backfillMedia`).
  - RBAC is deny-by-default for a missing extension.
- **Tenant isolation.**
  - Every query in `db/queries/{scenes,takes,characters,presets,media}.sql` references `tenant_id`.
  - Handlers take the tenant from `tenant.MustFromCtx`, never from request input.
  - The asset variant redirect loads the asset through the tenant-scoped `GetAssetByID`, serves only `ready` assets, and takes variant keys only from the stored JSON.
  - The cross-tenant integration tests pass.
- **Injection, SSRF and data loss.** These are unchanged since round 7:
  - The ffmpeg argv is typed and allowlisted.
  - Local inputs are confined to the step temp dir, and remote inputs must be https to the internal host.
  - A re-split locks the episode's scenes and refuses to drop edited work without `discardWork`.
- **Merge with main.** The engines merge touched pyworker, models and bench files only. There were no textual conflicts and no generated-code drift, and all suites pass on the merged tree.
- **Board.** Round 7's per-IP budget change was announced (line 106), and branches built on this one inherit it. Lane h's k6 overlay raises the same key for load runs only. No other CHANGE line affects this branch.

## Success criteria

| Criterion | Status |
|---|---|
| A 6k-word draft splits into scenes, with dialogue attributed to ≥2 characters that have voices assigned | **Flaky, counted as failed.** It passed on the re-run (6,531 words, 76 scenes, Lin Mo 132 and Elder Qiu 175) and in round 7. It failed on this round's first run with 0 attributed out of 304 (H1) |
| Editing one scene's narration marks only that scene's voice, align and render pieces stale; regenerating queues exactly one GPU step | **Met** for voice and align (`TestStoryboardSplitStepsStaleAndTakes`). Render pieces arrive with the render pipeline phase |
| The grid and timeline meet the 60fps budgets with 300+ scenes | **Met** at 450 scenes (60.0 fps averages, p95 ≤ 18.0 ms) |
| Ollama variant | Deferred by design to the LoRA/scoring phase report |
| The default e2e suite passes with default limits (round 7 blocker) | **Met.** 4 passed, 0 responses of 429 |

## Blocking items

1. Fix H1 and add the unit tests.
2. Then run the live split twice under the heavy lock with compose project `loomtale-a`. Both runs must attribute dialogue to both characters.
3. The api changes, so the toolbox and integration checks must be re-run. Playwright may be re-run if the web or api surface changes.
4. After that, the branch can merge into `main` under the merge lock. The merge commit `73ee21d` is local only and was not pushed.

## Unresolved questions

- The failing run's unrecognised names were not captured before `down -v`. Should the split step output keep a short list of them, so the cause is confirmed rather than inferred?
- Should the e2e specs keep rewriting the tracked `reports/phase-0*-screens` screenshots on every run? Each verification run dirties the working tree.
