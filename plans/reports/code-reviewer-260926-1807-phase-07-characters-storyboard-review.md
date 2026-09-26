# Independent verification: characters, storyboard and scene editor (round 5)

Branch `feat/characters-storyboard` @ `1d5b7b7` (worktree `.claude/worktrees/lane-a-7`), reviewed as `git diff main...HEAD` (147 files). `main` is still `4962ecd` (phases 1-6, 9a and 9b), and `git rev-list HEAD..main` is empty, so `main` is already merged into the branch and nothing new needs reconciling. The code is unchanged since round 3 (`a9334ce`). The only newer commits are review and cook-report documents.

**Verdict: not ready to merge.** This round found no Critical or High issues, and every check that can run on the host passes. The Docker engine is still down, so the toolbox run, the integration suite, Playwright and the live split criterion have not run on any commit since round 1. The merge is withheld.

## Docker status at 18:07

- `docker info` still returns HTTP 500 from `dockerDesktopLinuxEngine`. The Docker Desktop processes have been running since 17:29, and the `docker-desktop` WSL distro reports that it is running.
- `Get-Disk` still lists `C:\Users\ADMIN\AppData\Local\Docker\wsl\disk\docker_data.vhdx` as Windows disk 1 (read-only, online). The user's note says the disk space was dealt with, but Windows still holds the data disk. As long as it does, the VM cannot mount the disk and the engine cannot start.
- **Remedy (the user runs this in an elevated PowerShell):**
  1. Quit Docker Desktop.
  2. Run `Dismount-DiskImage -ImagePath 'C:\Users\ADMIN\AppData\Local\Docker\wsl\disk\docker_data.vhdx'`.
  3. Run `wsl --shutdown`.
  4. Start Docker Desktop again.
- This verifier did not detach the disk. That needs elevation and the user's approval, and it affects every lane.

## Checks run this round (host toolchain)

| Check | Result |
|---|---|
| Host gen: migrations and model assets copied, redocly 1.25.11 bundle, oapi-codegen, sqlc, `web npm run gen` | All exited 0 |
| Drift: `git status --porcelain` and `git diff --ignore-cr-at-eol` after gen | Clean, no drift |
| `go vet ./...` in `api`, and `go vet -tags integration,live` on `internal/integration` and `internal/workerpb` | Exit 0 |
| `golangci-lint run ./...` in `api` (2.14.0), and again with `--build-tags integration,live` on `internal/integration` | 0 issues |
| `go vet ./...` in `tools`, the `tenantctx` analyzer on `api/./...`, `scripts/lint-tenant-queries.sh` | Exit 0. The tenant-query lint prints "OK" |
| `loomtale models lint` | OK (9 models, 4 workflows, 2 Modelfiles) |
| `go test ./... -count=1` in `api` (no `-race`, because the host has no C compiler) | Exit 0, 41 packages ok |
| `go test ./... -count=1` in `tools` | Exit 0 |
| `workers-python` `ruff check` and `pytest -q`, run in a scratch venv | Ruff passes. Pytest: 66 passed, 2 skipped |
| Web `typecheck`, `lint` | Exit 0 |
| Web `npm test` | 19 files, 91 tests passed |
| Web `npx vite build` and `npm run budget-check` | Exit 0. "Bundle budget check passed", with CSS at 6.94 KB |
| `scripts/tb.sh gen lint test` (with `-race`) | **Not run** (Docker down) |
| Integration suite (`loomtale-a`, heavy lock) | **Not run** (Docker down) |
| Playwright `--workers=1`, compose.yml only | **Not run** (Docker down) |
| Live split on HEAD | **Not run** (Docker down) |

## Independent review (areas re-read this round)

- **Tenant isolation.**
  - Every query in `db/queries/{characters,media,presets,scenes,takes}.sql` references `tenant_id`.
  - Every `UPDATE` and `DELETE` in `scenes.sql` and `takes.sql` filters on `tenant_id` in its `WHERE` clause.
  - `ListTakes` joins `assets` on the tenant as well.
  - Every asset id that arrives in a request is loaded through the tenant-scoped `GetAssetByID` before it is stored or used. This covers character refs, the LoRA dataset, preset reference audio and the voice-preview reference.
  - `UpdateScene` accepts segment speakers and `characterIds` only when they belong to the scene's series. It accepts an image style only when `GetImageStyle` finds it in the caller's tenant.
- **Authorization (`x-min-role`).** In `openapi/paths/{characters,scenes,presets,media}.yaml`, every GET requires viewer, every create, update or delete requires editor, and `backfillMedia` requires owner.
- **SSRF and injection (`api/internal/media/ffmpeg`).**
  - Protocol whitelists come from one constant file. A remote input must be https to exactly the configured internal host, with no userinfo. A local input must be a clean absolute path strictly inside the temp directory.
  - The demuxer is forced with `-f` from an allowlist.
  - A concat list must be a local file, is read with `-safe 1`, and contains only plain file names (no separators, quotes, colons or newlines).
  - `GetAssetVariant` builds variant keys only from the tenant's stored `variants` JSON, never from request text.
- **LLM output.** Speaker names from the split are resolved against the series roster by case-folded name. An unknown name falls back to the narrator and is flagged. The LLM never supplies ids, and the paragraph taint carries through to the scenes.
- **Consent.** A preset that clones a reference voice cannot be stored without consent. When consent is given for a new reference, an audit entry is written.
- **Other lanes (board).** No lane has merged a shared-file change into `main` since the branch took `main` in. Lanes b, c and d have announced edits to `models/manifest.yaml`, `openapi/root.yaml`, `api/cmd/api/*`, `api/cmd/loomtale/*`, the web route tree and `scripts/lint-tenant-queries.sh`. This branch also changes `TENANT_TABLES` in `scripts/lint-tenant-queries.sh`, and lane d extends that same list. Whichever branch merges second must keep the union of both lists and regenerate `openapi.gen.yaml` and the generated code, not hand-merge them.

## Findings

### Critical / High

None.

### Medium

- **M1 (open since round 1). The image step ignores inputs that its stale hash includes.**
  - `scenes/inputs.go` hashes the style's negative prompt and sampler, and each character's negative prompt.
  - `scenes/steps_image.go` sends only the prompt, seed, steps, size and one LoRA.
  - So editing a negative prompt marks the images stale, but regenerating them cannot change the result.
  - This does not block the merge. Either send those parameters or drop them from the hash.

### Low (not blocking)

- **L1-L4** from round 4 are unchanged:
  - A confirmed split stores `discardWork` in the step input.
  - The media rate-limit bucket runs before the session middleware.
  - `GenerateMissing` has no guard against a double submit.
  - `scenes.Changed` on a split lists only the kept ids.
  - There are races between take selection and `RecordTake`, and on the LoRA version.
  - Assets can be orphaned.
  - There is no GIN index on `character_ids`.
  - `voiceparams.isFloat` accepts NaN and Inf.
- **L5 (new).** `UpdateScene` accepts `segments` and `narration` together without checking that they agree.
  - The voice step and the voice stale hash read the segment text.
  - The displayed narration, `text_hash` (and so the re-split keep decision) and the duration estimate read `narration`.
  - A client that sends both with different text therefore gets audio that does not match the narration shown.
  - Either derive the narration from the segments when both are sent, or reject a mismatch.
- The security-checklist boxes in the phase file are still unchecked, although the code meets each item as described above.

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

- The user reports that the disk space was dealt with, but `docker_data.vhdx` is still mounted in Windows. Did the fix include detaching it? Does Docker Desktop also need a reset or a VHDX repair after the interrupted compaction?
