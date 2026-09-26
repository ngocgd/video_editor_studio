# Independent verification: characters, storyboard and scene editor (round 6)

Branch `feat/characters-storyboard` @ `f7e663b` (worktree `.claude/worktrees/lane-a-7`), reviewed as `git diff main...HEAD` (148 files). `main` is still `4962ecd`, and `git rev-list HEAD..main` is empty. So `main` is already merged into the branch, and nothing new needs reconciling. The code has not changed since `a9334ce`. The only newer commits are review and cook-report documents.

**Verdict: not ready to merge.** This round found no Critical or High issues, and every check that can run on the host passes. The Docker engine is still down, though. The toolbox run, the integration suite, Playwright and the live split criterion therefore still cannot run, and the merge is withheld.

## Docker status at 18:17

- `docker info` and `docker version` still return HTTP 500 from `dockerDesktopLinuxEngine`.
- The Docker Desktop and `com.docker.backend` processes have been running since 17:29. WSL reports the `docker-desktop` distro as Running.
- `Get-Disk` still lists a 1 TB "Msft Virtual Disk" as Windows disk 1. It is read-only and online. This is `docker_data.vhdx`, which Windows still holds.
- The user's latest message says the disk space problem was handled. Free space was never the cause, though. The engine cannot start while Windows keeps the data disk attached.
- **Remedy (the user runs this in an elevated PowerShell):**
  1. Quit Docker Desktop.
  2. Run `Dismount-DiskImage -ImagePath 'C:\Users\ADMIN\AppData\Local\Docker\wsl\disk\docker_data.vhdx'`.
  3. Run `wsl --shutdown`.
  4. Start Docker Desktop again.
- This verifier did not detach the disk. Detaching needs elevation and the user's approval, and it affects every lane.

## Checks run this round (host toolchain, re-run independently)

| Check | Result |
|---|---|
| Host gen: migrations and model assets copied, redocly 1.25.11 bundle, oapi-codegen, sqlc, `web npm run gen` | All exited 0 |
| Drift: `git status --porcelain` and `git diff --ignore-cr-at-eol` after gen | Clean, no drift |
| `go vet ./...` in `api`, and `go vet -tags integration,live` on `internal/integration` and `internal/workerpb` | Exit 0 |
| `golangci-lint run ./...` in `api` (2.14.0), and again with `--build-tags integration,live` on `internal/integration` | 0 issues on both runs |
| `tools`: `go vet` and `golangci-lint` | Exit 0, 0 issues |
| `tenantctx` analyzer on `api/./...` | Exit 0 |
| `scripts/lint-tenant-queries.sh` | Prints "OK" |
| `loomtale models lint` | OK (9 models, 4 workflows, 2 Modelfiles) |
| `go test ./... -count=1` in `api` and in `tools` (no `-race`, because the host has no C compiler) | Exit 0 in both |
| `workers-python` `ruff check`, and `pytest -q` in a scratch venv | Ruff passes. Pytest: 66 passed, 2 skipped |
| Web `typecheck` and `lint` | Exit 0 |
| Web `npm test` | 19 files, 91 tests passed |
| Web `npx vite build` and `npm run budget-check` | Exit 0. "Bundle budget check passed", with CSS at 6.94 KB |
| `scripts/tb.sh gen lint test` (with `-race`) | **Not run** (Docker down) |
| Integration suite (`loomtale-a`, heavy lock) | **Not run** (Docker down) |
| Playwright `--workers=1`, compose.yml only | **Not run** (Docker down) |
| Live split on HEAD | **Not run** (Docker down) |

`workers-python/.venv` was created by the Linux toolbox, and `uv` on Windows cannot replace it. For that reason pytest ran with `UV_PROJECT_ENVIRONMENT` set to a scratch directory.

## Independent review (areas re-checked this round)

- **Authorization.**
  - Every one of the 32 new operations in `openapi/paths/{characters,scenes,presets,media}.yaml` declares `x-min-role`.
  - Every GET requires viewer.
  - Every POST, PUT, PATCH and DELETE requires editor, except `backfillMedia`, which requires owner.
  - `rbac.BuildMinRoles` denies by default: startup fails if an operation declares no role.
- **Re-split data safety** (`scenes/service.go` `ApplySplit`).
  - The existing rows are read with `FOR UPDATE OF s` inside the same transaction.
  - A kept scene is matched by its narration hash and keeps its id, edits and takes.
  - The whole transaction is refused with a `DropsWorkError` when it would delete an edited scene or any take and `discardWork` is false.
  - Renumbering depends on `scenes_episode_lang_idx_key` being `DEFERRABLE INITIALLY DEFERRED`.
  - `DeleteScenesExcept` filters on the tenant, the episode and the language.
  - One edge case: if two first splits run at the same time on an empty episode, there are no rows to lock. The deferred unique key then fails one of them at commit, so it ends in an error rather than lost data.
- **Asset variant redirect.**
  - The asset is loaded through the tenant-scoped `GetAssetByID`.
  - Variant keys come only from the stored `variants` JSON.
  - The redirect's `Cache-Control: private, max-age` is half of the presign TTL (`storage.MaxUploadTTL`), so a cached redirect cannot outlive its URL.
- **Unchanged since round 5, still valid by the diff.** These conclusions carry over because the code is byte-identical:
  - tenant filters in `db/queries/{characters,media,presets,scenes,takes}.sql`;
  - tenant-scoped asset loads for character refs, the LoRA dataset and preset or preview reference audio;
  - the ffmpeg protocol and path allowlists;
  - speaker resolution of the LLM split output against the series roster;
  - the consent audit for cloned voices.
- **Other lanes (board, read at start and at finish).**
  - No other lane has merged into `main` since the branch took it in.
  - Lanes b, c and d have announced shared-file edits: `models/manifest.yaml`, `openapi/root.yaml`, `api/cmd/api/*`, `api/cmd/loomtale/*`, the web route tree and `scripts/lint-tenant-queries.sh` `TENANT_TABLES`. This branch also edits `openapi/root.yaml`, the route tree and `TENANT_TABLES`.
  - Whichever branch merges second must keep the union of those lists and regenerate the generated code rather than hand-merge it.
  - Lane a's phase 8 branch was cut from this branch, so it inherits the verdict here.

## Findings

### Critical / High

None.

### Medium

- **M1 (open since round 1, re-confirmed). The image step ignores inputs that its stale hash includes.**
  - `scenes/inputs.go:169` hashes the style's negative prompt and sampler, and `inputs.go:176` hashes each character's negative prompt.
  - `scenes/steps_image.go` never sends them.
  - So editing a negative prompt marks the images stale, but regenerating them cannot change the result.
  - This does not block the merge.

### Low (not blocking, unchanged from round 5)

- L1-L4:
  - A confirmed split stores `discardWork` in the step input.
  - The media rate-limit bucket runs before the session middleware.
  - `GenerateMissing` has no guard against a double submit.
  - `scenes.Changed` on a split lists only the kept ids.
  - There are races between take selection and `RecordTake`, and on the LoRA version.
  - Assets can be orphaned when takes cascade on a re-split.
  - There is no GIN index on `character_ids`.
  - `voiceparams.isFloat` accepts NaN and Inf.
- L5: `UpdateScene` accepts `segments` and `narration` together without checking that they agree.
- The security-checklist boxes in the phase file are still unchecked, although the code meets each item.

## Success criteria

| Criterion | Status |
|---|---|
| A 6k-word draft splits into scenes, with dialogue attributed to ≥2 voiced characters | **Pending (Docker down).** It was observed live on `805e9b5`. `ApplySplit` has changed since, so it must be repeated on HEAD |
| A narration edit marks only that scene's voice, align and render pieces stale, and regenerating queues exactly one GPU step | **Pending (Docker down).** The integration suite has not run |
| The grid and timeline stay at 60fps with 300+ scenes | **Pending (Docker down).** Playwright has not run |
| Storyboard route chunk ≤120 KB gzip | Met. The budget check passes |
| Ollama variant | Deferred to phase 9c by design |

## Blocking items

1. The Docker data disk is still attached to Windows as disk 1, so the engine cannot start. The user has to detach it (see the remedy above).
2. Once Docker is up, these must run and pass:
   - `scripts/tb.sh gen lint test`
   - the integration suite under the heavy lock, including `TestResplitAfterANarrationEditNeedsConfirmation`
   - Playwright with `--workers=1`
   - the live split on HEAD

   After that the branch can be merged into `main` under the merge lock.

## Unresolved questions

- Did the user's disk fix include detaching `docker_data.vhdx` from Windows? `Get-Disk` still shows it attached at 18:17.
- After the interrupted compaction, does Docker Desktop also need a VHDX repair or a reset?
