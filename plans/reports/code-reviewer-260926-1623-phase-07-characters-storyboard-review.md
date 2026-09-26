# Independent verification: characters, storyboard and scene editor (round 2)

Branch `feat/characters-storyboard` @ `97e7821` (worktree `.claude/worktrees/lane-a-7`), reviewed as `git diff main...HEAD`. The merge base is now `main` itself (`4962ecd`), so the branch is up to date with main.

**Verdict: not ready to merge.** The round-1 High findings are fixed in code, but none of the Docker-based checks could run, one new High data-loss finding is open, and the worktree holds uncommitted work that does not compile.

## State of the worktree at the start of this round

- Three files were modified but not committed: `openapi/paths/media.yaml`, `api/internal/media/http.go` and a `Cache-Control` assertion in `api/internal/integration/scenes_test.go`. This is part (3) of the round-1 H2 fix, as the cook report says.
- The generated server code was not regenerated. `http.go` sets `GetAssetVariant302ResponseHeaders.CacheControl`, but `server.gen.go` at HEAD has only `Location`. The dirty tree therefore does not compile.
- For this round the three files were set aside with a tagged stash (`lane-a-7-verifier-r2-cachecontrol-wip`) so that HEAD could be verified, and they were restored afterwards. No code was changed.

## Checks

| Check | Result |
|---|---|
| Web `npm run typecheck`, `npm run lint` | exit 0 |
| Web `npm test` | exit 0. 89 tests passed, including the new `use-settled-value` test |
| Web `npx vite build`, `npm run budget-check` | exit 0. The storyboard route chunk is 15.89 KB gzip (limit 120 KB). The episode route first paint is 277.60 KB (limit 320 KB, above the 160 KB target), as on main |
| `scripts/tb.sh gen lint test` | **Not run.** The Docker engine is down (see below) |
| Generated-code drift (`git diff --exit-code` after `gen`) | **Not run** (needs the toolbox) |
| Integration suite (`loomtale-a`, heavy lock) | **Not run** (Docker down) |
| Playwright `--workers=1`, compose.yml only, default rate limit | **Not run** (Docker down). This run is the acceptance test of the round-1 H2 fix |
| Live LLM split (`COMPOSE_PROFILES=claude-cli`) | **Not run** (Docker down) |

**Docker engine.** Docker Desktop's engine has not answered since about 08:00 UTC (15:00 local). `docker ps` returns "500 Internal Server Error" on the `dockerDesktopLinuxEngine` pipe. The backend log keeps printing "still waiting for init control API to respond" (over 1 h at 09:05 UTC), and the WSL distro `docker-desktop` reports Running. It was polled until 16:34 local without recovery. Restarting the user's Docker Desktop was not attempted without the user's approval. The prepared heavy script (integration, e2e with default limits, live split with `-timeout 45m`, each followed by `down -v`) is ready to run once the engine is back.

## Round-1 findings: re-check

| Round-1 item | Status now | Evidence |
|---|---|---|
| H1. Voice params could carry `reference_url` and `consent` | **Fixed in code.** Tests were not run in this round | `voiceparams.Validate` allows only `exaggeration`, `cfg_weight`, `temperature`, `seed` and `voice`, and refuses the four control keys, unknown keys and malformed values. It runs on preset create and update and on character and narrator voice writes, which answer 422 (`presets.go:103`, `characters/http.go:475,495`). `MergedParams` keeps only tuning keys (`voiceparams.Tuning`), so older rows cannot reach the worker with control keys. `reference_url` and `consent=granted` are still set only from a consented reference asset. The built-in voice name is limited to `^[A-Za-z0-9][A-Za-z0-9 _.-]{0,63}$`, so it cannot contain a path separator |
| H2. The storyboard used up the per-IP budget | **Parts 1 and 2 fixed in code. Part 3 is uncommitted. The acceptance run was not done** | The takes fetch waits for the selection to rest for 250 ms and reuses a strip for 60 s. GET and HEAD of `^/api/v1/assets/[^/]+/variants/[^/]+$` draw from a separate per-IP bucket (default 1200 per minute). The general bucket stays at 100. `compose.yml` passes `API_MEDIA_RATE_LIMIT_PER_MINUTE` through, and no e2e script raises `API_RATE_LIMIT_PER_MINUTE` any more |
| M1. Branch behind main, and TTS voice contract drift | **Fixed** | The merge base equals main. `residency.go` keeps main's `OllamaPreparer` and adds `gpuClients`. The TTS `voice` field now carries `BuiltinVoice()` (the assignment's or the preset's `voice` param), and nothing when a reference clip is cloned. It is part of the voice stale hash |
| M2. A re-split deletes takes without a confirmation | **Open, and raised to High** (new H1 below), because of new evidence | |
| M3. The image step ignores hashed inputs | **Open** (M2 below) | |
| L1–L5 | Open. None was in the round-1 scope of fixes | |

## Findings

### High

**H1. Any re-split silently deletes every scene the user has edited, together with the manual work on it and all its takes.**
- **Where:**
  - `api/internal/scenes/service.go:307`: `UpdateScene` rewrites `text_hash` from the edited narration.
  - `service.go:132-190`: `ApplySplit` keeps only scenes whose hash matches a new draft, and `DeleteScenesExcept` deletes the rest, with their takes cascading.
  - `web/src/features/storyboard/storyboard-view.tsx:233-240`: "Split by paragraphs" and "Re-split with AI" run on one click, with no confirmation.
- **New evidence since round 1:** Round 1 described lost takes, which a GPU run can regenerate. More is lost:
  - A narration edit changes the stored `text_hash`. A draft made from the source paragraphs never has that hash, so **every edited scene is dropped on every re-split**, whether by paragraphs or by the LLM.
  - The same scene rows carry work only a person can redo. That includes edited narration, hand-written image prompts, the character list, the motion preset, and the manual assignment of "speaker not recognised" lines, which this phase requires to be done by hand.
  - The response (`sceneCount`, `keptCount`) and the banner never say how many scenes were deleted.
- **Scenario:**
  1. The user splits with the LLM and fixes 10 unrecognised speakers and 20 prompts.
  2. The user edits a line in the writer and clicks "Split by paragraphs" to pick it up.
  3. Every edited scene, and most others, are deleted with their takes and assignments. There is no undo, and the orphaned assets are not reachable from the UI (L3).
- **Fix (either):**
  - Add a dry run (or return `droppedCount` together with the number of edited scenes and takes that would be dropped), and require an explicit confirmation in the UI when it is above zero.
  - Or keep a scene that has user edits or takes when no draft matches it, and flag it for review instead of deleting it.
- **Test:** Add an integration test showing that a re-split after a narration edit either asks for confirmation (409 or a dry-run flag) or keeps the edited scene.

### Medium

**M1. Uncommitted, non-compiling Cache-Control work is left in the worktree.**
- **What:** `openapi/paths/media.yaml` declares a required `Cache-Control` response header. `media/http.go` sets it, and `scenes_test.go` asserts `private, max-age=300`. Without `tb.sh gen`, the tree does not compile.
- **Risk:** A later `tb.sh gen` or a `git add -A` in this worktree would commit it half-verified. Leaving it uncommitted means the cook report's H2 part (3) is not delivered.
- **Fix:** Run `tb.sh gen`, commit the change with the regenerated code, and run the integration test. Or drop the change and remove part (3) from the report.

**M2 (round-1 M3, still open). The image step ignores inputs that its stale hash includes.**
- **What:** `ImageComponents` hashes the style's and the characters' negative prompts and the sampler, but `ImageHandler.Run` (`steps_image.go:100-114`) sends none of them. Character reference images are not used either.
- **Impact:** Editing a negative prompt marks images stale, but regenerating them cannot change the result.
- **Fix:** Pass them when the workflow declares them. Otherwise, drop them from the hash and record ref usage as a phase 9a follow-up.

### Low

- **L1.** The media bucket is applied before the session middleware. An unauthenticated client gets 1200 requests a minute per IP on the variant path, and each request costs a session lookup. This is acceptable for local mode. Consider it before a SaaS mode.
- **L2.** `GenerateMissing` has no guard against a double submit. Two quick clicks both see `none` or `stale` pips and queue duplicate batches, because `PlanMissing` skips only steps that are already queued or running.
- **L3.** `ApplySplit` emits `scenes.Changed` with the kept ids only. The ids of deleted scenes are not announced, so phase 8's supersede handler must treat a `split` reason as episode-wide.
- **L4.** The round-1 lows L1–L5 are unchanged: the LoRA version race and the enqueue outside the transaction, the `selectTake` race with `RecordTake`, orphaned assets, the missing GIN index on `character_ids`, and the unchecked security-checklist boxes in the phase file.

## Verified as correct (code reading, this round)

- **Authorisation:** all 32 new operations carry `x-min-role`. Every GET is viewer, every write is editor, and `backfillMedia` is owner.
- **Tenant isolation:**
  - Asset, preset, style, episode, series, take and character lookups all take the tenant id.
  - The variant redirect looks up the asset by `(tenant, id)` and requires `ready`.
  - Take selection checks that the take belongs to the scene.
- **FFmpeg runner:**
  - One whitelist file. Inputs get a forced `-f`, concat is local only with `-safe 1`, and the https remote host must equal the configured host.
  - Local paths must be clean, absolute and strictly inside the temp dir. Only an integer is interpolated into the filter.
  - Muxers and codecs are allowlisted, and stderr is capped and scrubbed.
- **Image step:** the prompt travels as a JSON value in a server-side template, and undeclared params are dropped. Style LoRA names must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`, which allows no path separator.
- **Voice presets:** cloning needs a ready audio asset of the tenant plus explicit consent. Consent is recorded in the same transaction as the `voice_reference_consented` audit row.

## Success criteria

| Criterion | Status |
|---|---|
| A 6k-word draft splits into scenes, with dialogue attributed to ≥2 voiced characters | **Not re-observed on HEAD.** Round 1 observed it live on `805e9b5` (split step `01a0dca8-1d28-7870-b954-37e9c567c695`, 72 scenes). The fixes since then touch voice params and rate limits, not the split. The live run must still be repeated once Docker is back |
| Editing one scene's narration marks only that scene's voice, align and render pieces stale; regenerating queues exactly one GPU step | **Not re-observed** (the integration suite could not run) |
| Grid and timeline at 60fps with 300+ scenes | **Not re-observed** (the e2e run could not run) |
| Ollama variant | Deferred to phase 9c by design |

Docker being down is a local host fault, not a genuinely external dependency, so these criteria are not "pending for an external reason".

## Blocking items

1. **Restore the Docker engine**, which needs the user's approval to restart Docker Desktop. Then run, on the committed tree:
   - `scripts/tb.sh gen lint test` and the host `git diff --exit-code` drift check
   - the integration suite
   - the standard Playwright run with the **default** `API_RATE_LIMIT_PER_MINUTE` (the H2 acceptance)
   - the live claude-cli split
2. **Fix H1** (re-split data loss): add a confirmation with dropped and edited counts, or keep edited scenes, and add a test.
3. **Resolve M1:** regenerate and commit the Cache-Control change with its test, or discard it and correct the cook report.

## Unresolved questions

- Who restarts Docker Desktop: the user or the orchestrator? Two rounds are now blocked on it.
- Is keeping user-edited scenes on a re-split the preferred behaviour, or is a confirmation dialog enough?
- These are unchanged from round 1:
  - Should `pipeline_steps_scope_latest_idx` stay in this phase's migration?
  - Is the shell above its 160 KB target acceptable until the generated client is split per domain?
