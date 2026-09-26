# Independent verification: characters, storyboard and scene editor (round 3)

Branch `feat/characters-storyboard` @ `a9334ce` (worktree `.claude/worktrees/lane-a-7`), reviewed as `git diff main...HEAD`. The merge base equals `main` (`4962ecd`), and the worktree was clean at the start.

**Verdict: not ready to merge.** The round-2 code findings are fixed, and every host check passes. The Docker engine is still down, so the toolbox, drift, integration, Playwright and live-LLM checks have not run on any commit since round 1. This round found the cause: the Docker data disk is still attached to Windows after an interrupted compaction. The user has to detach it with an elevated shell.

## Why Docker is down

- The Docker data disk `C:\Users\ADMIN\AppData\Local\Docker\wsl\disk\docker_data.vhdx` is still attached to the Windows host as disk 1. `Get-Disk` reports it as "File Backed Virtual", `IsReadOnly True`, `Online`. While Windows holds it, the Docker Desktop VM cannot mount it, so dockerd never starts. `docker ps` returns 500 on `dockerDesktopLinuxEngine`, and the backend log keeps printing `GET /ping ... context deadline exceeded`.
- The disk was attached by `compact-docker-vhdx.ps1`, which `compact-docker-under-locks.sh` in the shared session scratchpad started at 15:00:24 local (08:00:24 UTC). That script ran `attach vdisk readonly` and `compact vdisk` through diskpart. Its log stops at "12 percent completed" and never reaches the "after:" or "done" lines. No diskpart process is running now, so nothing detached the disk. The engine stopped answering at 08:00 UTC, the same minute.
- **Remedy (needs the user and an elevated PowerShell):**
  1. Quit Docker Desktop.
  2. Run `Dismount-DiskImage -ImagePath 'C:\Users\ADMIN\AppData\Local\Docker\wsl\disk\docker_data.vhdx'`. The diskpart equivalent is `select vdisk file=...` then `detach vdisk`.
  3. Run `wsl --shutdown`, then start Docker Desktop.
- This verifier did not detach the disk or restart Docker. Both need elevation and the user's approval, and both affect the parallel lane too.

## Checks

| Check | Result |
|---|---|
| Web `npm run typecheck`, `npm run lint` | exit 0 |
| Web `npm test` | exit 0. 91 tests passed, including the re-split confirmation dialog test |
| Web `npx vite build`, `npm run budget-check` | exit 0. "Bundle budget check passed". The storyboard route chunk is 16.31 KB gzip (limit 80 KB per route; the phase limit is 120 KB). CSS is 6.94 KB |
| Host `go vet ./...` and `go vet -tags integration,live ./internal/integration/` (Go 1.26.8) | exit 0 |
| Host `go test ./...` | exit 0. 41 packages ok, including `scenes` (with the new `resplit_guard_test.go`), `voiceparams`, `ratelimit`, `media` and `media/ffmpeg` |
| `x-min-role` on every operation in `openapi.gen.yaml` | All 86 operations carry one. On the phase 7 routes, every GET is viewer, every write is editor, and `POST /media/backfill` is owner |
| `scripts/tb.sh gen lint test` (golangci-lint, tenant-query lint, `-race`) | **Not run** (Docker down) |
| Generated-code drift (`git diff --exit-code` after the toolbox `gen`) | **Not run** (Docker down) |
| Integration suite (`loomtale-a`, heavy lock) | **Not run** (Docker down) |
| Playwright `--workers=1`, compose.yml only, default `API_RATE_LIMIT_PER_MINUTE` | **Not run** (Docker down). This run is still the acceptance test of the round-1 H2 fix |
| Live claude-cli split (`COMPOSE_PROFILES=claude-cli`) on HEAD | **Not run** (Docker down) |

Host `gofmt -l` (Go 1.26.8) flags some files, including files that are unchanged from `main` (`api/internal/pipeline/types.go`). It is therefore a host tool difference, not a finding. The toolbox golangci-lint run is still the gate.

## Round-2 findings: re-check

| Round-2 item | Status | Evidence |
|---|---|---|
| H1. A re-split silently deleted edited scenes and their takes | **Fixed in code. Its integration test has not run** | Details below |
| M1. The uncommitted Cache-Control change did not compile | **Resolved** | `b88e468` commits the spec header, the regenerated `server.gen.go` and the assertion. The header is `private, max-age=300`, which is half of the 10-minute `PresignGet` lifetime (`storage.MaxUploadTTL`), so a cached redirect never points at an expired URL. The host build and vet pass |
| M2. The image step ignores inputs that its stale hash includes | **Open (Medium)** | See M1 below |
| L1–L4 | Open. Low, and not blocking | Unchanged |

**How the H1 fix works.**
- A new column, `scenes.edited_at`, is set by `UpdateSceneEdit`. That is the only query for a person's scene edit.
- `ApplySplit` reads the scenes through `ListScenesForResplit`, which takes `FOR UPDATE OF s` and counts the takes.
- It then plans the split and computes `RiskOf`. If an edited scene or any take would be deleted and `discardWork` is not set, it returns a `*DropsWorkError`. The API answers that with a 409 carrying `droppedCount`, `editedCount` and `takeCount`.

**Why the fix holds.**
- **Concurrency.** A concurrent `InsertTake` needs a key-share lock on the scene row, which conflicts with the `FOR UPDATE` lock. It therefore waits and cannot slip past the count.
- **LLM split.** The upfront check counts every current scene, which gives an upper bound. If scenes are edited or get takes while the model runs, the step fails with `ErrValidation` and deletes nothing.
- **Web.** The web client re-sends the split with `discardWork: true` only after "Split and delete" in `ResplitConfirmDialog`. While the dialog is open, the 409 is not shown as an inline error.
- **Test.** `TestResplitAfterANarrationEditNeedsConfirmation` checks both modes, that a refused split changes nothing, and the confirmed split. It has not run yet.

## Review of the whole diff (spot checks this round)

- **Tenant isolation:**
  - Every new query in `scenes.sql` and `takes.sql` takes `tenant_id`.
  - The take-count subquery in `ListScenesForResplit` is also scoped by tenant.
  - The variant redirect looks up the asset by `(tenant, id)` and requires the `ready` status.
- **Secrets:** none are in the diff. The work did not read `secrets/claude_oauth_token.txt`.
- **Injection and SSRF:**
  - The ffmpeg runner, the image template parameters (JSON values, with undeclared parameters dropped) and the voice parameter allowlist are unchanged since round 2.
  - Those were verified by reading the code in round 2, and their unit tests pass on the host.
- **Migrations:**
  - `20260926300300_scene_edited_at.sql` is a nullable `ADD COLUMN` with a matching down migration.
  - It is copied into `api/internal/db/migrations` by the `gen-migrations-sync` convention.
  - No other local `feat/*` branch uses the `202609263*` timestamps.

## Findings

### Critical / High

None open in the code. The only blocking items are the Docker-based checks that have not run (see Blocking items).

### Medium

**M1 (open since round 1). The image step ignores inputs that its stale hash includes.**
- **Where:** `inputs.go:169,176` hashes the style's negative prompt and sampler, and each character's negative prompt. `ImageHandler.Run` (`steps_image.go:104-126`) sends only `prompt`, `seed`, `steps`, `width`, `height` and one LoRA. Character reference images are not used.
- **Impact:** Editing a negative prompt or the sampler marks images stale, but regenerating them cannot change the result.
- **Fix:** Send `negative_prompt` and `sampler` when the workflow declares them. Otherwise, remove them from the hash and record the use of references as a phase 9a follow-up.

### Low

- **L1 (new).** A confirmed LLM split stores `discardWork: true` in the step input. If River retries that step later, it can delete scenes that were edited, or that got takes, after the confirmation. Consider limiting the confirmation to the drop counts that were confirmed, or clearing the flag on retry.
- **L2 (round 2).** Unchanged:
  - The media rate-limit bucket runs before the session middleware.
  - `GenerateMissing` has no guard against a double submit.
  - `scenes.Changed` on a split lists only the kept ids.
- **L3 (round 1).** Unchanged:
  - The LoRA version race.
  - The `selectTake` race with `RecordTake`.
  - Orphaned assets.
  - There is no GIN index on `character_ids`.
  - The security checklist boxes in the phase file are unchecked.

## Success criteria

| Criterion | Status |
|---|---|
| A 6k-word draft splits into scenes, with dialogue attributed to ≥2 voiced characters | **Not re-observed on HEAD.** It was observed live on `805e9b5` (76 scenes, Lin Mo 148 and Elder Qiu 197 segments). Since then, `ApplySplit` and the split step have changed (the re-split guard), so the live run must be repeated |
| Editing one scene's narration marks only that scene's voice, align and render pieces stale; regenerating queues exactly one GPU step | **Not re-observed** (integration did not run) |
| Grid and timeline at 60fps with 300+ scenes | **Not re-observed** (Playwright did not run) |
| Ollama variant | Deferred to phase 9c by design |

The missing criteria are blocked by a local host fault (the attached VHDX), not by a genuinely external dependency.

## Blocking items

1. **Detach the Docker data disk and restart Docker.** This needs the user and an elevated shell (see "Why Docker is down").
2. Then run, on `a9334ce` or later, under `with-lock.sh heavy`, with `docker compose -p loomtale-a ... down -v` at the end of the same script:
   - `scripts/tb.sh gen lint test`, followed by a host `git diff --exit-code` on `api/internal/db/gen`, `api/internal/httpapi/gen`, `api/internal/db/migrations`, `openapi/openapi.gen.yaml` and `web/src/api/gen`.
   - The integration suite (`PROJECT=loomtale-a scripts/test-integration-toolbox.sh`). It must include `TestResplitAfterANarrationEditNeedsConfirmation`, `TestStoryboardSplitStepsStaleAndTakes`, `TestVoiceParamsCannotCarryServerControlKeys` and the Cache-Control assertion.
   - Playwright with compose.yml only, the default `API_RATE_LIMIT_PER_MINUTE`, and `--workers=1`. This is the round-1 H2 acceptance.
   - The live claude-cli split (`COMPOSE_PROFILES=claude-cli`) on HEAD. Record the step id, the scene count and the attributed characters.

## Unresolved questions

- Who ran the scratchpad VHDX compaction? Did it finish enough that the disk is consistent? It was a read-only attach, so data loss is unlikely, but Docker should be checked after the detach.
- Is a confirmation dialog the accepted re-split behaviour, or should edited scenes be kept and flagged? This is carried over from round 2.
- Should `pipeline_steps_scope_latest_idx` stay in this phase's migration? Is the 166.76 KB shell acceptable? Both are carried over.
