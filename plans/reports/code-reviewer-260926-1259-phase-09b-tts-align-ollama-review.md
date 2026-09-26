# Review: TTS (EN/VI), subtitle alignment and the local Ollama LLM (independent verification, round 1)

- Branch: `feat/tts-align-ollama` at `a7c85d5` (base `aa06c62`). Worktree: `.claude/worktrees/lane-b-9b`.
- Scope: `git diff main...HEAD` (89 files, about 8.2k added lines): Python engines and servicers, the Go residency, Ollama import, manifest, digest migration, speech-rate calibration, bench suites and deploy changes.
- Verdict: **PASS.** There are no Critical or High findings. Every check is green. Every success criterion that needs model weights is pending, and the only reason is that the user paused model downloads.

## Verification (re-run independently)

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` | Exit 0. golangci-lint reported 0 issues. The manifest lint passed (9 models, 4 workflows, 2 Modelfiles). ruff passed. All Go packages passed with `-race`. pytest: 66 passed and 2 skipped (the skipped tests are `-m gpu` and need weights) |
| Generated-code drift (host `git diff --exit-code` after `gen`) | Clean: exit 0 and an empty `git status` |
| Web: `typecheck`, `lint`, `npm test`, `npx vite build`, `budget-check` | All passed. 38 tests in 8 files. The bundle budget check passed |
| Integration (`loomtale-b`, heavy lock) | `INTEG_EXIT=0` with 64 `--- PASS` lines and no FAIL. The new tests `TestVoiceRateCalibrationRoundTrip` and `TestModelFileDigestsAcceptSha256AndGitBlobPins` both passed. The stack was brought down with `down -v` |
| Playwright e2e (`--workers=1`) | 2 passed (smoke and model manager). The stack was brought down with `down -v` |
| Merge with current `main` (phase 6 merged), in a temporary worktree | The textual merge is clean. After regenerating the migrations, models, sqlc and proto copies there is no drift. `go build`, `go vet` and `go test ./...` all pass on the merged tree |
| Live pyworker probe (GPU overlay, image without extras, heavy lock) | `ListEngines` shows chatterbox (installed), vieneu-v3-turbo and whisper-align (not installed). `LoadModel chatterbox` fails with `FAILED_PRECONDITION engine_not_installed: … lacks the engine's runtime`, and every other engine returns `engine_not_installed`. Synthesize without a language returns `INVALID_ARGUMENT`. A clone without consent returns `PERMISSION_DENIED voice_consent_required`. A clone with consent reaches the honest runtime refusal. Align returns `engine_not_installed`. A wrong bearer token returns `UNAUTHENTICATED` |
| pyworker isolation | DNS lookups for huggingface.co and pypi.org are blocked, and TCP to 1.1.1.1:443 is blocked. `HF_HUB_OFFLINE=1`, `TRANSFORMERS_OFFLINE=1`, uid 10001. The cgroup memory.max is 8 GiB and idle usage is 59 MiB. `/models/tts/chatterbox` holds only the 4 pinned files (no `conds.pt`) |
| `loomtale bench --suite voice-smoke --ollama-model qwen3.5-9b` | Exit 1, as expected: every case is refused by the Go load gate (`… is not verified (install the model first)`) and every budget is reported as "not measured". The VRAM budget is 12977 MB, which is above both LLM estimates (11000 and 11300 MB). `models list` shows the 5 new entries as `not_installed`. An unknown suite lists the new suite names |
| Commit hygiene | Commits are conventional, carry no AI references, and add no plan or finding IDs in code, tests or migrations |

No model weights, engine extras or Ollama image were downloaded. The pyworker image was built without extras.

## Findings

### Critical / High
None.

### Medium (not blocking)
1. **An expired output URL makes a finished synthesis a permanent failure** (`workers-python/src/loomtale_worker/servicers/streaming.py` `push_output`, `fetch_input`; `api/internal/providers/workerconn/dial.go`).
   - What happens: any 4xx on the presigned PUT becomes `INVALID_ARGUMENT`, and `TranslateErr` maps that to `pipeline.ErrValidation`, which is never retried. The output URL is presigned before the job starts. `storage.Internal.PresignGet/Put` takes a TTL chosen by the caller. A long narration (hours of audio) can therefore finish after its URL has expired. MinIO answers 403, the audio is thrown away, and the step is never retried with a fresh URL.
   - Fix: map 403 on output upload to `UNAVAILABLE` (retryable). If that is not done, the phase 7 voice and align steps must presign with a TTL longer than the step timeout, and that contract must be written down. No caller exists yet, so this does not block the merge.
2. **Calibration is not yet read by `duration.Estimate`** (`api/internal/duration/duration.go` on main, `api/internal/speechrate`). The requirement says measured wpm lands in `voice_presets.measured_wpm` and that `Estimate` drops the "uncalibrated" label. Phase 6 is now on main, and its `Estimate` keeps a `voicePreset` parameter reserved for this. The voice key still needs phase 7's voice presets (the digest of the reference asset), so the wiring stays a phase 7 follow-up (deviation 3 in the cook report). When this branch merges main, keep `speechrate.Lookup` as the planned source.
3. **The engine-extras lock carries 15 open advisories** (torch 2.9.1: 4, transformers 5.2.0: 6, diffusers 0.29.0: 5 in `workers-python/uv.lock`). None of these packages is in the image built today, because `PYWORKER_EXTRAS` is empty. `make vuln` runs pip-audit only on the base environment, with `|| true`, so CI will never report them. Bump these packages before the first image is built with extras.

### Low / info
4. The `TransferTooLargeError` text embeds the presigned URL, and the servicer returns it as the gRPC status detail. The Go pipeline scrubs error text (`scrub.Text` in `pipeline/commit.go`), so stored step errors are safe. Only bench output would print the URL, and there it is the bench sink's token URL.
5. Config files are pinned by git blob id, which is SHA-1. This is weaker than sha256 against collisions. The risk is bounded: such pins are limited to config extensions of 16 MB or less at a pinned revision, and all weights keep sha256. Accept as documented.
6. `ChatterboxTTS.from_local` would load a `conds.pt` that appeared in `tts/chatterbox/` through `torch.load(weights_only=True)`, and `LocalFiles.assert_safe` checks only the pinned files. The directory was observed to hold only the pinned files, and only the worker writes that volume. Consider refusing to load when unpinned files are present.
7. A cancelled RPC does not cancel the engine thread. `ModelManager.run` holds its lock until the job ends, so a cancelled long synthesis blocks the next job. This is acceptable for one GPU slot, but worth noting for the phase 7 timeouts.
8. The `cli` service now receives `llmcli_bearer_token` (the llm bench) and joins `loomtale_llm`. This is an on-demand operator tool, and the compose comments were updated to match.
9. `loomtale bench --out` must be a mounted directory, because the cli root filesystem is read-only. `scripts/bench-voice.sh` handles this. A bare `run cli bench --out /tmp/x` fails with `mkdir: read-only file system`.

## Spec conformance

- Engines: Chatterbox (EN, clones the preset reference, requires consent), VieNeu v3 Turbo (VI) and whisper-align (EN word level, VI segment level) are registered. They report their install state from the pinned files, and when the runtime is missing they fail with an honest `engine_not_installed`.
- Weights pinned, pickles refused, pyworker offline: confirmed live.
- CUDA OOM maps to `gpu_oom`.
- Ollama: the GGUF is imported offline from the verified file through the blob API. The Modelfile parser refuses any FROM that is not the pinned GGUF, and ADAPTER, MESSAGE and LICENSE directives. Residency is proven through `/api/ps` and `Unload` releases every running model. All of this is unit-tested against a fake Ollama.
- The bench suites `tts`, `align`, `llm` and `voice-smoke` exist and report every budget.
- Accepted deviations, recorded by the implementer as user decisions:
  - VieNeu runs on ONNX on the CPU. The GPU path would need `trust_remote_code`.
  - Alignment uses faster-whisper plus a numpy CTC aligner instead of `whisperx`.
  - Calibration is stored in `voice_rate_calibrations` until phase 7 lands.
  - Ollama has no `/models` mount; this isolates it more strictly than the plan asked for.

## Success criteria

| Criterion | Status |
|---|---|
| EN and VI voice plus aligned subtitles for a real episode | Pending: model downloads paused by the user. It also needs phase 7's voice and align steps on main |
| Ollama answers AI actions, with residency proven by `/api/ps`; the phase 6/7 Ollama variants | Pending: model downloads paused by the user (the GGUF is about 9.5 GB). The phase 7 variants also need phase 7 |
| Predicted vs actual duration within 10% after calibration | Pending: model downloads paused by the user. The fit on 20 paragraphs and check on 10 is verified with a fake engine in unit tests |
| tts, align and llm suites plus human ratings; performance budgets | Pending: model downloads paused by the user. The suites run and report every budget as "not measured" (observed live) |

## Unresolved questions

- Should output-URL expiry be retryable in pyworker, or should phase 7 guarantee the TTL? (Finding 1.)
- The implementer's open questions still stand for the user: VieNeu on GPU with pinned remote code, whether building the extras counts as a download, which LLM becomes the default, and whether to keep the Perth watermark.
