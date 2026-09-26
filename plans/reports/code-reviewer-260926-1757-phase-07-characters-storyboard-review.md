# Independent verification: characters, storyboard and scene editor (round 4)

Branch `feat/characters-storyboard` @ `4fd2f53` (worktree `.claude/worktrees/lane-a-7`), reviewed as `git diff main...HEAD` (146 files). `main` (`4962ecd`, with phases 6, 9a and 9b) is already an ancestor of the branch through merge `076fb8f`, so no further merge of `main` is needed at this point. The code is unchanged since round 3 (`a9334ce`): the only newer commit is the round-3 report.

**Verdict: not ready to merge.** There are no Critical or High code findings, and every check that can run on the host passes, including golangci-lint, the tenant-query lint and a host regeneration with no drift. The Docker engine is still down, so the toolbox run, the integration suite, Playwright and the live split criterion still have not run on any commit since round 1.

## Docker status at 17:57

- `docker info` still returns 500 on `dockerDesktopLinuxEngine`. Docker Desktop was restarted at 17:29, but that did not fix it.
- `Get-Disk` still lists `C:\Users\ADMIN\AppData\Local\Docker\wsl\disk\docker_data.vhdx` as disk 1 ("Msft Virtual Disk", `IsReadOnly True`, `Online`). Windows still holds the Docker data disk, so the VM cannot mount it. Freeing space on C: (328 GB free now) does not release the disk.
- **Remedy (the user runs this in an elevated PowerShell):** quit Docker Desktop, run `Dismount-DiskImage -ImagePath 'C:\Users\ADMIN\AppData\Local\Docker\wsl\disk\docker_data.vhdx'`, run `wsl --shutdown`, and then start Docker Desktop again.
- This verifier did not detach the disk. Detaching needs elevation and the user's approval, and it affects every lane.

## Checks run this round

| Check | Result |
|---|---|
| Host gen: migrations and model assets copied, redocly 1.25.11 bundle, oapi-codegen, sqlc, `web npm run gen` | All exit 0 |
| Host drift: `git diff --ignore-cr-at-eol` over every generated path, plus `git status --porcelain` | No drift, and the tree is clean |
| `go vet ./...`, plus `go vet -tags integration,live` on `internal/integration` and `internal/workerpb` | exit 0 |
| `golangci-lint run ./...` (host 2.14.0) | 0 issues |
| `tools` vet, the `tenantctx` analyzer on `./...`, `scripts/lint-tenant-queries.sh` | All exit 0. The tenant-query lint prints "OK" |
| `go test ./... -count=1` in `api` (no `-race`, because the host has no C compiler) | exit 0. 41 packages ok |
| `go test ./...` in `tools` | ok |
| `workers-python` pytest, in a scratch venv because the worktree `.venv` is the toolbox's Linux venv | 66 passed, 2 skipped. The branch does not change `workers-python` or `proto` |
| Web `typecheck`, `lint` | exit 0 |
| Web `npm test` | 19 files, 91 tests passed |
| Web `npx vite build`, `npm run budget-check` | exit 0. "Bundle budget check passed", and CSS is 6.94 KB |
| `x-min-role` over the bundled spec | Every operation carries one. On the phase routes, every GET is viewer, every write is editor, and `POST /media/backfill` is owner |
| `scripts/tb.sh gen lint test` (with `-race`) | **Not run** (Docker down) |
| Integration suite (`loomtale-a`, heavy lock) | **Not run** (Docker down) |
| Playwright `--workers=1`, compose.yml only | **Not run** (Docker down) |
| Live claude-cli split on HEAD | **Not run** (Docker down) |

## Independent review (areas re-read this round)

- **Tenant isolation.**
  - Every query in `db/queries/{characters,media,presets,scenes,takes}.sql` filters on or writes `tenant_id`.
  - Cross-table links use composite `(tenant_id, id)` foreign keys (characters → series, refs/LoRAs/voices → characters, voices → voice presets, scenes → episodes and image styles).
  - Asset ids from a request body (the reference audio, character refs, the LoRA dataset) are loaded through the tenant-scoped `GetAssetByID` before they are stored. The plain `REFERENCES assets (id)` foreign keys therefore cannot link another tenant's asset.
  - `SelectTake` checks that the take belongs to the path's scene.
- **Authorization.** The `x-min-role` table above matches the phase: people with the viewer role can read, and every mutation needs editor or higher.
- **SSRF and injection.**
  - The ffmpeg runner forces `-f` from an allowlist and uses `-protocol_whitelist` `https,tls,tcp` only for URLs whose host equals the configured internal store (no userinfo). It uses `file` only for clean absolute paths strictly inside the step temp directory.
  - A concat list must be local, and it gets `-safe 1`. Output muxers and codecs are allowlisted, and the arguments include `-nostdin -loglevel error`.
  - `voiceparams` rejects the server-owned keys (`reference_url`, `consent`, `output_key`, `language`) and any unknown key.
  - Character names that the LLM returns are resolved against the series roster, never trusted as ids, and the split input carries a taint flag.
- **Data loss.** The re-split guard is unchanged since round 3. It reads the scenes with `ListScenesForResplit ... FOR UPDATE OF s`, refuses with 409 when an edited scene or a take would be dropped, and the web client asks before it re-sends with `discardWork`. The integration test for it has not run yet.
- **Other lanes (board).** No lane has merged a shared-file change into `main` since this branch took `main` in. Lanes b, c and d have announced later changes to `models/manifest.yaml`, `openapi/root.yaml`, `api/cmd/api/*`, `api/cmd/loomtale/*`, the web route tree and `scripts/lint-tenant-queries.sh`. This branch also edits `TENANT_TABLES` in `scripts/lint-tenant-queries.sh`, and lane d plans to extend that same line. Whichever branch merges second has to take the union of both table lists.

## Findings

### Critical / High

None.

### Medium

- **M1 (open since round 1). The image step ignores inputs that its stale hash includes.**
  - `inputs.go:169,176` hashes the style's negative prompt and sampler, and each character's negative prompt.
  - `steps_image.go:104-126` sends only `prompt`, `seed`, `steps`, `width`, `height` and one LoRA.
  - Editing a negative prompt therefore marks images stale, but regenerating them cannot change the result.
  - This does not block the merge. Either send the declared parameters or remove them from the hash.

### Low (unchanged, not blocking)

- **L1.** A confirmed LLM split stores `discardWork: true` in the step input. If the step runs, or retries, after the confirmation, it can delete edits made in the meantime.
- **L2.**
  - The media rate-limit bucket runs before the session middleware.
  - `GenerateMissing` has no guard against a double submit.
  - `scenes.Changed` on a split lists only the kept ids.
- **L3.**
  - `selectTake` and `RecordTake` can race.
  - The LoRA version can race.
  - Assets can be orphaned.
  - There is no GIN index on `character_ids`.
- **L4 (new, minor).** `voiceparams.isFloat` accepts `NaN` and `Inf`. The engine enforces the ranges, so the impact is only an engine-side error.
- The security-checklist boxes in the phase file are still unchecked, although the code meets each item as described above.

## Success criteria

| Criterion | Status |
|---|---|
| A 6k-word draft splits into scenes, with dialogue attributed to ≥2 voiced characters | **Pending (Docker down).** It was observed live on `805e9b5`. `ApplySplit` has changed since, so it must be repeated on HEAD |
| A narration edit marks only that scene's voice, align and render pieces stale, and regenerating queues exactly one GPU step | **Pending (Docker down).** The integration suite has not run |
| The grid and timeline stay at 60fps with 300+ scenes | **Pending (Docker down).** Playwright has not run |
| Storyboard route chunk ≤120 KB gzip | Met. The budget check passes, as measured in round 3 at 16.31 KB |
| Ollama variant | Deferred to phase 9c by design |

## Blocking items

1. The Docker data disk is still attached to Windows, so the engine cannot start. The user has to detach it (see the remedy above).
2. Once Docker is up, these must run and pass: `scripts/tb.sh gen lint test`, the integration suite under the heavy lock (including `TestResplitAfterANarrationEditNeedsConfirmation`), Playwright `--workers=1`, and the live split on HEAD.

## Unresolved questions

- Did the user's disk-space fix include detaching `docker_data.vhdx`? Windows still reports it as attached, so it seems not. Is a Docker Desktop reset or a VHDX repair also needed after the interrupted compaction?
