# Phase 9b — TTS (EN/VI), subtitle alignment and the local Ollama LLM

Branch: `feat/tts-align-ollama` · Worktree: `.claude/worktrees/lane-b-9b` · Base: `main` @ `aa06c62`
Status: DONE_WITH_CONCERNS. Everything that does not need model weights is built and verified, including the live paths up to the point where weights or engine runtimes are needed. Every criterion that needs a downloaded model (or the multi-GB engine runtime wheels) is pending because **model downloads are paused by the user**.

## What shipped

- **Manifest entries** (`models/manifest.yaml`), all pinned from Hugging Face metadata only (tree API; no file content fetched):
  - `chatterbox` (EN TTS, MIT): the three safetensors files and `tokenizer.json`. The repo's `.pt` files, including the built-in `conds.pt` voice, are pickles and are left out, so every EN request clones a preset's reference audio.
  - `vieneu-v3-turbo` (VI TTS, Apache-2.0): the ONNX engine graphs, the speaker encoder and denoiser, and the MOSS audio codec's ONNX export from its own repository.
  - `whisper-align` (MIT): faster-whisper large-v3 (CTranslate2) plus `facebook/wav2vec2-base-960h` safetensors for EN word alignment. The wav2vec2 files carry their own Apache-2.0 licence.
  - `qwen3.5-9b` (Q8_0) and `gemma-4-12b` (Q6_K), both Apache-2.0, as the local LLM candidates. Each has a Modelfile in `models/ollama/`.
- **Manifest schema** (`api/internal/models`):
  - Small non-LFS config files have no sha256 on the Hub, so they are pinned by `git_sha1` (their git blob id at the pinned revision). The downloader verifies that id while streaming. `model_files.sha256` became `digest` (a migration accepts a sha256 hex or `git-sha1:<hex>`).
  - Files can carry their own `licence`, and the licence gate checks every one of them.
  - The allowed formats now include ONNX (`.onnx`, `.data`), `.npz`, and a `.bin` only when it is declared `format: ctranslate2`. Pickles are still refused.
  - The linter checks that each Ollama entry's Modelfile imports one of the entry's pinned GGUF files. Modelfiles are embedded next to the manifest.
- **Python engines** (`workers-python/src/loomtale_worker/engines/`: `chatterbox.py`, `vieneu.py`, `whisper_align.py` and shared modules):
  - All three engines are registered at startup. Each reports installed only when its pinned files are present, and imports its runtime lazily. An image without the runtime answers `engine_not_installed: … this worker image lacks the engine's runtime`.
  - Alignment matches the known script onto faster-whisper's timed words (difflib, NFC-normalised), so cues always show the script's own text. EN is refined to word level by a numpy CTC forced aligner over wav2vec2 frame log-probabilities. torchaudio's `forced_align` was avoided because it is removed in current releases. VI stays at segment level.
  - VieNeu runs the torch-free ONNX engine. Its codec is resolved through an offline Hugging Face cache view (symlinks into `HF_HUB_CACHE`) because the SDK fetches it by repo id.
- **gRPC servicers**:
  - `Synthesize` validates the language, text and output URL. Cloning (`reference_url`) is refused with `PERMISSION_DENIED voice_consent_required` unless `consent=granted`, which the phase 7 voice step sets from the preset's consent flag.
  - The engine loads before any input is fetched. Progress is relayed from the engine thread. The output is 16-bit PCM WAV uploaded to the presigned URL, and the metadata carries RTF, VRAM peak and word count.
  - `Align` uploads the cue JSON.
  - Errors map as follows. Invalid requests and 4xx URLs become `INVALID_ARGUMENT`, which is permanent. Network and 5xx failures become `UNAVAILABLE`, which is retried. OOM becomes `gpu_oom`. `workerconn.TranslateErr` maps `INVALID_ARGUMENT` and `PERMISSION_DENIED` to `pipeline.ErrValidation`.
- **Go worker wiring**:
  - The generic `pyworker` residency backend loads the engine a `ModelRef` names and proves it resident via `ListEngines`, after the licence and verified-files gate.
  - The Ollama backend imports a missing manifest LLM offline on first load. It uploads the verified GGUF as a blob (skipped when Ollama already holds that digest), then calls `/api/create` with the Modelfile's parameters. It unloads every model `/api/ps` lists and proves residency for the requested model.
- **Calibration** (`api/internal/speechrate`, table `voice_rate_calibrations`): stores measured words per minute per voice key. The key is the engine plus either the built-in voice name or the reference-audio digest. The tts suite writes it, and `Lookup` is what `duration.Estimate` will read (see deviation 3).
- **Bench suites** (`api/internal/bench/{tts,align,llm}_suite.go`, `voice_smoke.go`; CLI `loomtale bench --suite tts|align|llm|voice-smoke`; host wrapper `scripts/bench-voice.sh`, which also samples pyworker RSS). The suites drive the real worker through the residency manager. Audio passes through a token-guarded in-process HTTP sink that stands in for presigned storage URLs.
  - `tts`: 30 EN and 30 VI paragraphs. It records RTF, VRAM peak and switch time. It fits words per minute on 20 paragraphs, checks the prediction on the other 10, and stores the calibration.
  - `align`: builds about 5 minutes of audio per language from sentences with known offsets. It measures maximum and mean cue drift and alignment speed in minutes per hour of audio.
  - `llm`: records first-token latency and throughput for the Ollama model and the claude CLI, and writes the outputs plus a `ratings.csv` for 1–5 human ratings.
  - `voice-smoke`: 1 VI line, 1 EN line, a switch to Ollama and back, and 1 alignment.
  - Budgets are printed as within budget, over budget or not measured.
- **Deploy**:
  - `pyworker.Dockerfile` takes `PYWORKER_EXTRAS` (the locked extras `tts-en`, `tts-vi`, `align`). It defaults to empty in `.env.example` while downloads are paused.
  - pyworker gets `mem_limit: 8g`, `MODELS_DIR`, `HF_HUB_CACHE`, and an `LD_LIBRARY_PATH` pointing at the NVIDIA wheel libraries so CTranslate2 finds cuDNN and cuBLAS.
  - The `cli` service can reach pyworker, Ollama and the llm-cli sidecar.
  - `uv.lock` resolves all three extras (CUDA 12.8 torch 2.9.1). It was locked under a network cap: only metadata was fetched, about 52 MB, and no wheels.

## Verification

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` (final run) | pass. golangci-lint 0 issues, manifest lint OK (9 models, 4 workflows, 2 Modelfiles), ruff clean, all Go packages ok with `-race`, pytest **66 passed, 2 skipped** (the 2 skipped are `-m gpu` tests that need weights). Before this phase: 26 passed |
| Generated-code drift | `git status` clean after `gen` |
| New tests | Go: models +10 (git pins, formats, per-file licences, Modelfile lint, Ollama import), ollama +4, pyworker backend +2, bench voice/llm +11, speechrate +3. Python: +40 (text chunking, WAV, CTC aligner, script matching, cue building, engine catalog versus manifest, missing-runtime path, HF cache view, servicers: consent, validation, progress, uploads, status mapping, load-before-fetch) |
| `web`: typecheck, lint, vitest, `vite build`, `budget-check` | pass. 38 tests in 8 files; bundle budget passed (models chunk 2.39 KB) |
| Integration (`loomtale-b`, heavy lock) | **pass, 0 failures**. 64 `--- PASS` lines, including the new `TestVoiceRateCalibrationRoundTrip` and `TestModelFileDigestsAcceptSha256AndGitBlobPins`. The Go test run took 30.9 s; 288 s including stack start-up |
| Playwright e2e (`--workers=1`) | **2 passed** (smoke, model manager) |
| GPU overlay live (pyworker built without extras, comfyui scaled to 0) | pyworker healthy. `ListEngines`: chatterbox (tts, MIT, installed), vieneu-v3-turbo and whisper-align (not installed). A direct `Synthesize` returned `FAILED_PRECONDITION engine_not_installed: chatterbox: this worker image lacks the engine's runtime (chatterbox)`. A clone without consent returned `PERMISSION_DENIED voice_consent_required` |
| pyworker isolation | no DNS for huggingface.co, no TCP to 1.1.1.1:443, `HF_HUB_OFFLINE=1`, `TRANSFORMERS_OFFLINE=1`, uid 10001. Idle RSS 58 MiB of 8 GiB |
| Git-blob pin on real data | The Chatterbox files the phase 1b spike left on `loomtale_models` hash to the manifest pins offline: the 3 sha256 values and the tokenizer's git blob id `abd07c71…` all match. `loomtale models pull chatterbox` then **adopted** them in 4–10 s with every proxy pointed at a closed port, so no download was possible. `models list` showed it as installed |
| `loomtale bench --suite voice-smoke` (qwen3.5-9b) | exit 1 as expected. VieNeu and qwen3.5-9b: `engine_not_installed: … is not verified (install the model first)` from the Go gate. Chatterbox: passes the gate, then `this worker image lacks the engine's runtime`. VRAM budget measured 11567–12976 MB across runs (desktop load varies). pyworker RSS peak 59 MiB |
| `loomtale bench --suite llm` (with `COMPOSE_PROFILES=claude-cli`) | Ollama cases: not installed (honest). **claude CLI cases ran live**: outline-en 554 tokens in 9.6 s, draft-en 790 in 13.9 s, outline-vi 689 in 14.5 s, draft-vi 1066 in 24.4 s. The sidecar answers in one chunk, so first token equals total (9.6–24.4 s, over the 3 s budget, which targets the resident local model). The tokens-per-second figure was fixed afterwards to use the whole request for one-chunk answers. Outputs and `ratings.csv` were produced, and the VI outline reads as fluent Vietnamese |
| `pip-audit` on the engine extras (metadata only, OSV) | 15 advisories remain in 3 packages the inference path uses: torch 2.9.1 (4), transformers 5.2.0 (6), diffusers 0.29.0 (5). gradio and starlette (demo UIs no engine imports) were raised to fixed releases by override. See follow-ups |

## Success criteria

| Criterion | Status |
|---|---|
| EN and VI voice plus aligned subtitles for a real episode | **Pending: model downloads paused by the user.** It needs the VieNeu and whisper weights, and the `tts-en`, `tts-vi` and `align` runtime wheels (several GB of CUDA PyTorch). It also needs phase 7's voice and align steps, which are not yet merged. Chatterbox weights are already verified on the volume |
| Ollama answers AI actions with residency proven by `/api/ps`; phase 6/7 Ollama variants | **Pending: model downloads paused by the user** (a GGUF of about 9.5 GB). Import, load and `/api/ps` proof are implemented and unit-tested against a fake Ollama. `/settings/llm/test` with Ollama and the phase 6/7 Ollama variants need the model (and phase 6/7 on main) |
| Predicted vs actual episode duration within 10% after calibration | **Pending: model downloads paused by the user.** The tts suite fits on 20 paragraphs and checks the prediction on 10 (verified with a fake engine). Writing into `voice_presets.measured_wpm` and `duration.Estimate` waits for phases 6 and 7 (deviation 3) |
| Performance budgets (TTS RTF ≤0.3, align ≤10 min/h, LLM first token ≤3 s, switch ≤15 s, pyworker RSS ≤8g) | **Pending: model downloads paused by the user.** The suites measure and print each budget. pyworker idle RSS is 58 MiB |
| tts/align/llm suites plus human ratings | Suites are built. The llm suite ran live for claude CLI. The Ollama half and all ratings are **pending: model downloads paused by the user** (ratings need both models' outputs side by side) |

## Deviations

1. **VieNeu runs on ONNX Runtime (CPU), not PyTorch cu128.** The SDK's GPU path loads its codec with `trust_remote_code`, which executes Python from a model repository and so breaks the "weights pinned, no code from models" rule. The CPU RTF may miss the 0.3 target; measure it once downloads resume. Allowing pinned remote code for the GPU path is a user decision.
2. **Alignment uses faster-whisper plus a numpy CTC aligner, not the `whisperx` package.** WhisperX pins torch 2.8, and it needs pyannote VAD (pickle checkpoints, gated) and torchaudio's removed `forced_align`. The method is the same: recogniser timings, then wav2vec2 forced alignment for EN.
3. **Calibration is stored in `voice_rate_calibrations`, not `voice_presets.measured_wpm`.** Phase 7's `voice_presets` and phase 6's `duration.Estimate` are not on main. `speechrate.Lookup(voiceKey)` is ready to use: the phase 7 preset's reference-asset digest gives the same key.
4. **Ollama gets no `/models` mount.** The worker uploads the verified GGUF through Ollama's blob API, which is what `ollama create -f` does on the client side. This keeps the ollama container off the shared weights tree.
5. **Config files are pinned by git blob id** instead of sha256, because the Hub publishes none for non-LFS files and fetching them was out of bounds while downloads are paused. Verified on real data (see Verification).
6. **The Chatterbox Perth watermarker is kept.** Its checkpoint ships inside the hash-locked `resemble-perth` wheel and is loaded by `torch.load`, whose default is `weights_only=True` in torch 2.9. Model weights never go through `torch.load`.

## Follow-ups (when downloads resume)

- Build pyworker with `PYWORKER_EXTRAS="tts-en tts-vi align"`, install the models from the Model manager, and run `uv run pytest -m gpu` inside the image. Chatterbox, the whisper model and Ollama on `sm_120` are unverified: CTranslate2 ships no sm_120 kernels and relies on PTX JIT.
- Run `scripts/bench-voice.sh voice-smoke`, then `tts`, `align` and `llm` with `qwen3.5-9b` and `gemma-4-12b`. Fill in `ratings.csv`, record the rows here and in the 9c report, and replace the VRAM estimates with measured peaks.
- Check `ollama show --template` after import. The Modelfiles rely on the chat template embedded in the GGUF.
- Bump torch, transformers and diffusers past the open advisories together with the first GPU run, so the bump can be tested.
- Once phases 6 and 7 merge: wire `speechrate.Lookup` into `duration.Estimate` and `voice_presets.measured_wpm`, call `tts`/`align` from the voice and align steps (params `language`, `reference_url` plus `consent`, `output_key`), and set `OLLAMA_MODEL`.

## Unresolved questions

- Should the VieNeu GPU path (pinned `trust_remote_code` for the MOSS codec) be allowed if CPU RTF misses the budget?
- Does building the pyworker extras (CUDA PyTorch wheels, several GB) count as a "model download" under the pause? It was treated as one here.
- Which LLM becomes the default (qwen3.5-9b or gemma-4-12b)? This is the 9c sign-off, after benchmark ratings.
- Should the Perth watermark on Chatterbox output stay on (deviation 6)?
