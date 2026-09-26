# Phase 07: Characters, voice presets, image styles, storyboard and scene editor

## Context links
- [plan.md](plan.md) · [contract §2 AC1 (≥2 consistent characters), AC4 (per-scene rerun); §4 sitemap](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- Wireframes: [storyboard-scene-editor](../../docs/wireframe/storyboard-scene-editor.html) (high fidelity), [characters](../../docs/wireframe/characters.html)
- [design-guidelines §7 scene tile, regenerate/takes, §8 storyboard keys, §9 roving tabindex](../../docs/design-guidelines.md)
- [model research §3 character consistency](../reports/researcher-260924-2128-local-ai-models-rtx5060ti.md)
- Depends on phase 6 (drafts, storyctx) and, through it, phases 3, 4 and 5.

## Overview
- Priority: P1 · Status: implemented (see reports/cook-260926-phase-07-characters-storyboard.md) · Effort: 28h <!-- RT#15 re-estimate -->
- This phase delivers the scene layer: splitting a draft into scenes (image prompt, characters, speaker segments) and per-scene image, voice and subtitle-align steps with takes and A/B compare, all shown as stale when their inputs change. It also delivers character profiles, references, LoRA versions and voices, the virtualized storyboard grid with inspector and a 3-hour timeline, and the first media derivatives (image variants and waveform peaks). The GPU engines arrive in phases 9a (image), 9b (TTS, align) and 9c (LoRA, scoring). Until then, generate actions fail honestly with `engine_not_installed`.

## Requirements
- Characters:
  - Names `{orig, en, vi}`, role, appearance prompt, negative prompt, trigger token, a profile sent to the LLM (token count shown), and the episodes the character appears in.
  - `character_refs`: uploaded or generated images with an angle label and an approved flag.
  - `character_loras`: version, dataset asset ids, trainer params, status, weights asset.
  - `character_voices`: per language, an engine and voice preset with params such as exaggeration.
  - Actions: Regenerate sheet (image-edit workflow), Upload refs, Train LoRA (a `gpu` step at priority 4 through the phase 4 `train` client; the ai-toolkit pyworker engine comes in phase 9c), and Preview line (a TTS step).
- Presets: `voice_presets` (engine, reference audio asset, params) and `image_styles` (style prompt, base model ref, sampler, steps, resolution, LoRA list), each with a settings page.
- Scenes:
  - `scenes(episode_id, lang, idx, paragraph_ids[], narration, segments jsonb [{speaker_character_id|narrator, text}], image_prompt, character_ids[], motion_preset, duration_ms, text_hash, tainted)` (taint inherited from the source paragraphs, phase 6) <!-- RT#12 -->
  - `scene_takes(scene_id, kind image|voice|align, asset_id, params jsonb, input_hash, selected)`
- Split:
  - The `llm.scene_split` step produces JSON Schema output (boundaries by paragraph id, image prompt, characters present, dialogue attribution). **The LLM emits character names, never IDs**: the server maps each name (case-folded, against `{orig,en,vi}` of the series' characters only) to an ID; unknown names become `narrator` with a "speaker not recognised" flag for manual assignment. <!-- RT#12 --> The target scene length comes from an image-change cadence setting (default 20–40s).
  - A deterministic "Split by paragraphs" option is also available as a real, non-LLM algorithm.
  - Re-split keeps takes for scenes whose `text_hash` is unchanged.
- Per-scene steps:
  - `image.generate` (gpu; style + character LoRA/refs → ComfyUI workflow)
  - `voice.synthesize` (gpu; one call per segment, concatenated with a 150ms gap)
  - `align.subtitles` (gpu; depends on voice)
  - `media.variants` (cpu; WebP and AVIF at widths 320/640/1280)
  - `media.peaks` (cpu; min/max peaks JSON at 100 peaks per second)
- "Generate missing (N)" enqueues batch steps at priority 3, grouped by stage and model and chunked to ≈10 min by the phase 3 engine, to minimise residency switches. A single Regenerate is priority 2. <!-- RT#6 -->
- Stale detection: `input_hash` = hash(narration or prompt, preset params, model ref, character LoRA version). Pips show a stale warning with the reason tooltip.
- **Scene-change hook** <!-- RT#9 -->: every narration, prompt, segment, motion or take-selection change emits `scenes.Changed{episode, lang, scene_ids}` through an in-process hook registry. Phase 8 registers the render supersede handler on it (edit during an active render is allowed; the running render run is superseded). Phase 7 does not import the render package.
- The storyboard UI shows:
  - Filter chips All/Stale/Failed/Missing/In queue (server counts), text search, and per-stage progress counters.
  - A virtualized `SceneCard` grid and an inspector with narration edit (E), prompt, characters, motion (M), voice, takes strip (A/B, revert) and Regenerate image (I) / voice (V).
  - A timeline strip with clips sized by duration and a waveform drawn on canvas from the peaks for the visible window only. It also supports J/K navigation, Space to play scene audio, and Shift+Click range select.

## Architecture
- The shared FFmpeg runner (`api/internal/media/ffmpeg`) is created here and reused by phases 8 and 10 <!-- RT#13 RT#14 -->:
  - argv built from typed params only; `-hide_banner -nostdin -loglevel error`; stderr capped and passed through the phase 2 URL scrubber before logging.
  - **One protocol whitelist constant** in `ffmpeg/protocols.go`: remote inputs get `-protocol_whitelist https,tls,tcp` and must be internal presigned URLs whose host equals the configured internal MinIO endpoint (validated in Go before exec); local inputs get `-protocol_whitelist file` and must be paths inside the step's temp dir (validated in Go). No other protocol (`http`, `concat:`, `subfile`, `data`, `pipe` for inputs) is ever allowed.
  - **Forced input format** (`-f mp4|wav|flac|image2|concat` per input) so no demuxer is chosen by probing; concat lists use `-safe 1` with relative file names in the temp dir.
  - Runs with a timeout, parses `-progress pipe:1` into `StepContext.Progress`, and writes outputs to the temp dir before upload through `storage.Internal`.
- Image flow: the step reads the style and characters and builds `WorkflowParams`. It calls `residency.Ensure(comfy model)`, then `comfyui.Run`. The output goes to MinIO and becomes an `assets` row plus a take. `media.variants` is then enqueued (cpu), and SSE emits `scene.updated`.
- Voice flow: the step builds segments with the voices assigned per character and calls `tts.Synthesize` (gRPC with presigned PUT) once per segment. The Go side concatenates the segments (ffmpeg concat), sets `duration_ms`, enqueues `media.peaks` and the align step, and marks dependent render segments stale (the phase 8 hook, through `pipeline.MarkStaleDependents`).
- Scene state comes from one rollup query per episode that returns the pip states for all scenes (a LATERAL join on the latest step per kind), so there is no N+1.
- Grid virtualization: rows are virtualized, and columns are derived from the container width through a ResizeObserver. Tile height is fixed, so there is no measurement churn. Images use `srcset` with `sizes` and an in-viewport-only `loading=lazy`.

## Related files
- Create:
  - `db/migrations/*_characters.sql`, `*_scenes.sql`, `*_presets.sql`, and `db/queries/{characters,scenes,takes,presets}.sql`
  - `api/internal/{characters,scenes,presets,media,media/ffmpeg}/`
  - step handlers in `api/internal/scenes/steps_*.go`
  - `openapi/paths/{characters,scenes,presets}.yaml`, `openapi/schemas/scenes.yaml`
  - `web/src/features/{characters,storyboard,timeline,presets}/`, `web/src/routes/_app/projects/$seriesId/{characters,storyboard}/**`, `web/src/routes/_app/settings/{voices,styles}.tsx`
- Modify: `api/internal/storyctx` (pin character profiles; the hook was left open by phase 6), `openapi/root.yaml`.
- Create: `api/internal/scenes/hooks.go` (the `scenes.Changed` registry).

## Implementation steps
1. Write the characters, refs, LoRAs and voices schema, CRUD and the Characters page per the wireframe (ref sheet grid, LoRA card, voice card, profile tokens).
2. Write the voice presets and image styles CRUD and settings pages.
3. Write the scenes and takes schema, the split step (LLM plus paragraph mode, server-side name → ID mapping) and hash-preserving re-split.
4. Write the FFmpeg runner (whitelist constant, forced formats, host/path validation, scrubbed stderr), the `media.variants` and `media.peaks` steps, and backfill for existing assets.
5. Write the image, voice and align step handlers against the phase 4 interfaces, the batch grouping, and the take selection endpoint.
6. Write the `scenes.Changed` hook emission on every mutating path, then the rollup query and endpoints: `GET /episodes/{id}/scenes?lang&filter&cursor`, `PATCH /scenes/{id}`, `POST /episodes/{id}/generate-missing`, `POST /scenes/{id}/regenerate {kind}`.
7. Build the storyboard UI: grid, filters, inspector, takes strip, timeline canvas, shortcuts and roving focus.
8. Add a `storyctx` character pinning plus token counter.

## Todo checklist
- [x] Characters + refs + LoRA versions + voices (+ UI)
- [x] Voice presets + image styles
- [x] Scenes/takes schema + split + re-split
- [x] FFmpeg runner (single whitelist, forced -f, host/path checks) + variants + peaks
- [x] `scenes.Changed` hook
- [x] Image/voice/align steps + batches + takes
- [x] Rollup query + endpoints
- [x] Storyboard grid + inspector + timeline + keys

## Performance budget checks
- 300+ scenes: the grid scrolls at 60fps with ≤40 mounted tiles, and selection or keyboard moves take <16ms. A 3h timeline (≈400 clips) pans and zooms at 60fps, and peaks are fetched per visible window, with each chunk ≤200KB. All of this is measured in a Playwright trace (phase 12), with a quick check here.
- The storyboard route chunk is ≤120KB gzip. The rollup query takes ≤60ms for 400 scenes (EXPLAIN-checked).
- Tile images: 320w WebP ≤25KB median, no CLS (fixed aspect box).
- Batching: "Generate missing" for 48 scenes causes ≤3 residency switches (image → voice → align), as asserted in the test.

## Security checklist
- [ ] Reference and preset audio uploads sniffed (image/audio allowlist), size-capped
- [ ] FFmpeg argv built from typed params only (no string concatenation of user text); single whitelist constant (`https,tls,tcp` for internal MinIO URLs, `file` only inside the step temp dir), forced `-f`, `-safe 1`, `-loglevel error`, Go-side host and path validation
- [ ] ComfyUI prompt fields escaped as JSON values; workflow templates are server-side constants
- [ ] Voice cloning refs: consent checkbox on upload (own or licensed voice), recorded in audit log
- [ ] All draft/scene text sent to the LLM as nonce-fenced data blocks with taint; LLM-emitted character references resolved by name server-side, never trusted as IDs

## Reuse points
- Reuse: `SceneCard`, `PipelinePips`, `StatusChip`, `InspectorPanel`, `VirtualGrid`, `JobProgress`, `InlineError`, `sse-bridge`, `pipeline.*`, `comfyui`, `tts`, `align`, `storyctx`, `duration.Estimate`.
- Create: `media/ffmpeg` runner (reused by render and thumbnails), `media.variants`, `media.peaks`, the `TakesStrip` component and the `Timeline` canvas component (reused in Review for the spot-check player).

## Tests
- `scripts/tb.ps1 test` covers split validation, name → ID mapping (including a forged UUID in the LLM output being ignored), input hash stability, re-split preservation, the rollup worst-state logic, FFmpeg argv building and whitelist refusal (`file:` outside the temp dir, a foreign host, `concat:` input).
- `scripts/tb.ps1 test-integration` covers:
  - real FFmpeg variants and peaks on lavfi fixtures (`testsrc2`, `sine`)
  - image, voice and align steps against test-double gRPC and ComfyUI servers in `_test.go`
  - stale propagation after a narration edit, and `scenes.Changed` emitted once per mutation
  - `engine_not_installed` surfacing as a failed pip with a clear message
- `cd web && npm run test` covers the grid keyboard roving, filter counts and takes A/B.

## Success criteria
- A 6k-word draft splits into scenes, with dialogue attributed to ≥2 characters that have voices assigned.
- Editing one scene's narration marks only that scene's voice, align and render pieces stale. Regenerating it queues exactly one GPU step (AC4, per-scene).
- The grid and timeline meet the 60fps budgets with 300+ scenes of seeded data.
- **Ollama variant** (run after phase 9b, recorded in the phase 9c report): the scene split criterion above passes with Ollama as the provider. <!-- RT#7 -->

## Risks + rollback
- Scene split quality varies by provider (Medium×Medium). The paragraph mode is always available, and manual merge/split edits are supported.
- Multi-speaker concatenation causes prosody seams (Medium×Low). The gap is configurable, and a per-segment re-take is available.
- Rollback: feature tables and routes. Revert the PR and apply the goose down migration. `storyctx` pinning is feature-flagged.

## Next steps
Phase 8 renders the scenes and registers the render supersede hook. Phases 9a–9c install the engines that make image, voice, align and LoRA real.
