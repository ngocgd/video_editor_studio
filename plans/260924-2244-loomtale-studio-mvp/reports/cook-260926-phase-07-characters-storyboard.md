# Cook report: phase 7, characters, storyboard and scene editor

Branch `feat/characters-storyboard` (worktree `.claude/worktrees/lane-a-7`), based on `main` @ `367c0d7`. Not merged or pushed.
Status: DONE_WITH_CONCERNS. Every requirement is built and verified. The live-LLM split criterion passes with the claude CLI. The real GPU engines (image, TTS, align, LoRA) are exercised only through test doubles, because their weights and pyworker engines belong to phases 9a–9c.

## What shipped

- **Schema** (`db/migrations/2026092630{0000,0100,0200}_*.sql`; the timestamps sort after lane B's `20260926200000`):
  - `voice_presets` stores the consent time and user, and a CHECK refuses a cloned voice that has no consent.
  - `image_styles`.
  - `characters`, with names `{orig,en,vi}`, prompts, trigger token, profile and `pinned`.
  - `character_refs`, `character_loras`, `character_voices` (which keep the latest preview line) and `narrator_voices`.
  - `series_storyboard_settings`: default style, cadence (20–40s) and segment gap (150ms).
  - `scenes`, with segments jsonb, `text_hash` and `tainted`, and a deferred unique `(episode, lang, idx)` so a re-split can renumber rows.
  - `scene_takes`, with a single selected take per kind and component hashes in `params`.
  - Every foreign key includes `(tenant_id, id)`. The tenant-query lint now also covers the new tables.
- **FFmpeg runner** (`api/internal/media/ffmpeg`):
  - Argv is built only from typed values. The flags `-hide_banner -nostdin -loglevel error` are always set.
  - One whitelist constant file: remote inputs get `https,tls,tcp` and must use the internal host, checked in Go. Local inputs get `file` and must be inside the step's temp dir.
  - Input formats are forced with `-f`, and concat lists run with `-safe 1` and plain names only.
  - stderr is capped and URL-scrubbed. Progress is read from `-progress pipe:1`.
  - A pinned static ffmpeg 7.1.1 (`mwader/static-ffmpeg@sha256:11a4…`, with libwebp and libaom) ships in the worker and toolbox images. The worker gets a 1 GB `/tmp` tmpfs, and its root filesystem stays read-only.
- **Media steps** on the cpu queue:
  - `media.variants` makes WebP and AVIF images at 320, 640 and 1280 px (never upscaled beyond 320) and records their width and height.
  - `media.peaks` makes 100 min/max pairs per second, stored as JSON, and records the duration.
  - `POST /media/backfill` (owner only) runs both steps for assets that are missing them.
  - `GET /assets/{id}/variants/{variant}` redirects to a 10-minute browser URL, so list responses carry no presigned URLs.
- **Scenes** (`api/internal/scenes`):
  - **Deterministic paragraph split.** Paragraphs are grouped by the cadence. A quote is attributed to the only character named in its paragraph.
  - **`llm.scene_split`.** The model sees labels P1..Pn and Q1..Qm and a roster of names, never ids. Scene boundaries come only from start labels, so every paragraph lands in exactly one scene. Names are case-folded and matched against the series' own characters in all three languages. An unknown name, or a forged UUID, becomes the narrator with an "unrecognised" flag.
  - **Re-split** keeps any scene whose `text_hash` is unchanged, with its id, edits and takes.
  - **Input hashes** are built from named components, so a stale pip can say which input changed:
    - image: prompt, style, model, and each character's look and LoRA version
    - voice: segment text, voices and gap
    - align: the voice take and the text
  - **Rollup:** one query per episode with LATERAL joins for the latest step and the selected take of each kind. It feeds the pip states, the worst state, the filter counts, stage progress and "Generate missing (N)".
  - **`scenes.Changed` hook registry.** It fires on each edit, take selection (including a new take) and split.
  - **SSE:** `scene.updated` events go out on the step's run topic.
- **Per-scene steps:**
  - `image.generate` runs the style's manifest scene workflow through `comfyui.Engine`. Prompts are JSON values in the server's own template, and LoRA comes from the character or else the style.
  - `voice.synthesize` makes one TTS call per segment with a presigned PUT, then concatenates the results through the ffmpeg runner with a silence gap. It sets the measured duration, queues peaks, and queues align when it ran on its own.
  - `align.subtitles` produces the cue JSON asset.
  - "Generate missing" enqueues one batch run at priority 3, with align depending on voice. Regenerate queues exactly one step at priority 2.
  - Per-kind estimates size the chunks, and the episode inputs are memoized for each batch enqueue.
- **Characters and presets:**
  - CRUD for characters, refs, voices and narrator voices.
  - Character sheet (`image.character_sheet`, image-edit workflow).
  - LoRA training (`train.lora`, priority 4, Train RPC with a dataset manifest of presigned URLs).
  - Preview line (`voice.preview`).
  - Voice presets: cloning requires the consent flag, which is audited as `voice_reference_consented`. A preset still in use cannot be deleted.
  - Image styles are checked against the manifest's scene models.
  - **storyctx:** pinned character profiles are added as their own budgeted `characters` block. The switch is `STORY_PIN_CHARACTERS` (default on, the rollback flag). `EstimateTokens` feeds the profile token counts, and there is a `scene_split` template.
- **Web:**
  - **Storyboard route:**
    - Filter chips with server counts, text search and stage counters.
    - A virtualized grid: columns come from a ResizeObserver, the tile height is computed from the width, tiles are memoized, and one roving focus stop uses `aria-activedescendant`.
    - Tiles use AVIF/WebP `srcset` and `sizes`.
    - The inspector covers image takes, prompt, style, characters, narration edit (E), assigning unrecognised speakers, voice and subtitle takes, and motion (M).
    - A `TakesStrip` supports A/B compare and revert.
    - A canvas `Timeline` has V1 clips, an A1 waveform fetched only for the visible window, an S1 lane, zoom and pan, J/K and Space.
    - Scene settings cover cadence, gap and default style.
  - **Characters page:** ref sheet grid with upload, approve, angle and regenerate sheet; LoRA card; voice cards with preview line; profile token count and pin; narrator voices.
  - **Settings:** Voices and Styles pages.

## Verification

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` | exit 0. golangci-lint 0 issues, tenantctx and tenant-query lint OK, manifest lint OK, ruff clean, 40 Go packages ok with `-race`, pytest 26 passed |
| Generated-code drift | `git status` clean after `gen` |
| New Go unit tests | scenes 21 (split and dialogue attribution, name mapping, a forged UUID ignored, ambiguous names, cadence, LLM normalisation, re-split plan, re-segmentation, hash stability, narration-only staleness, stale reasons, pip precedence, worst state, filters, the batch plan chaining align after voice, voice planning, WAV); ffmpeg 7 (argv, 17 refusal cases including `file:` outside the temp dir, a foreign host, `concat:`, `http`); media 3; storyctx +1 |
| Web | typecheck and lint clean. vitest 18 files, 88 tests (new: grid keyboard roving, filter counts, takes A/B, storyboard model, timeline math). `vite build` and `budget-check` pass. Storyboard route chunk 15.9 KB gzip (limit 120 KB), characters 4.5 KB |
| Integration (`loomtale-a`, heavy lock) | **72 passed, 0 failed, 0 skipped, 44s** |
| — per-scene steps on test doubles | Fake ComfyUI HTTP and fake TTS/align gRPC. Image, voice (one TTS call per segment, ffmpeg concat with gap) and align takes were produced. The live worker's real ffmpeg made the variants and peaks. The peaks window is 50 values for 500 ms |
| — stale propagation | A narration edit left the image `done`. Voice and align went `stale` with "Narration edited after this take was generated". The other scenes were untouched. The stale filter returns exactly that scene. A stale `expectedVersion` gets 409 |
| — `engine_not_installed` | A fake TTS FAILED_PRECONDITION shows as a failed voice pip with code `engine_not_installed` and its message |
| — `scenes.Changed` | Emitted once per edit, once per take selection and once per split (3 events for 3 mutations) |
| — batching | 48 scenes produced 144 steps and **3 residency switches** (`comfyui:z-image-turbo` → `pyworker:chatterbox` → `pyworker:whisper-align`), through the real GPU executor in River order |
| — lavfi fixtures | `testsrc2` 1280×720 → webp-320 of 3,122 bytes, and AVIF with the `ftypavif` signature. A 3 s `sine` produced about 300 peaks. Width, height and duration were recorded |
| — rollup | 400 scenes: median **6.65 ms** (min 6.06, max 9.21). `GET /episodes/{id}/scenes` for 400 scenes took 68 ms over HTTP |
| — security | A clone without consent gets 422. With consent it gets 201 and one audit row. An image offered as a voice reference gets 422. Deleting a preset in use gets 409. Scenes, characters, voices and asset variants return 404 to another tenant. Another tenant's character as a scene character gets 400 |
| Playwright e2e (`--workers=1`, heavy lock) | **4 passed** (models, smoke, writer and import, and the new storyboard/characters spec) |
| — 450 scenes, 2:45:41 of timeline | Grid scroll **60.1 fps** (avg 16.64 ms, p95 18.9 ms), at most **14 mounted tiles**. Keyboard move **median 4.2 ms**, max 12.6 ms. Timeline pan and zoom **59.9 fps** (p95 18.3 ms) |
| Live LLM (claude-cli profile, `INTEGRATION_TAGS=integration,live`) | See the first success criterion below |

Screens are in `reports/phase-07-screens/` (grid, timeline, inspector edit, in-queue filter, characters, voices, styles).

## Success criteria

| Criterion | Status |
|---|---|
| A 6k-word draft splits into scenes, with dialogue attributed to ≥2 characters that have voices assigned | **Met (live).** Seven claude-cli Continue steps took the draft from 171 to 6,438 words (e.g. `01a0dc66-cdf9-744b-8c50-01468672ba07`, `01a0dc69-82f7-7dd2-b74f-d33fb6809cbe`). `llm.scene_split` step `01a0dc69-ecaf-794e-beb6-7169a1cd0189` (run `01a0dc69-ecaf-7950-9a56-233fb4ae2908`), provider claude-cli, took 8m06s and produced **76 scenes**. Dialogue segments: Lin Mo 148, Elder Qiu 197 (both voiced). 10 quoted lines were flagged "speaker not recognised" for manual assignment |
| Editing one scene's narration marks only that scene's voice, align and render pieces stale; regenerating queues exactly one GPU step | **Met** for voice and align (integration). Render pieces do not exist until phase 8, which subscribes to `scenes.Changed` (emitted once per mutation, verified). Regenerate queued exactly one gpu step at priority 2 |
| Grid and timeline at 60fps with 300+ scenes | **Met (quick check)** at 450 scenes (numbers above). The Playwright trace belongs to phase 12 |
| Ollama variant | **Deferred to phase 9c** by design (recorded in the 9c report) |

## Deviations

1. **Remote ffmpeg inputs are not used.** The internal MinIO is plain HTTP, and the whitelist allows only https remote inputs. The steps therefore download inputs into the step temp dir and run ffmpeg with `-protocol_whitelist file`. The remote path (https to the internal host, checked in Go) is implemented and tested for phase 8's use.
2. **ffmpeg comes from a pinned static build image**, copied into the distroless worker and the toolbox. There is no Debian package.
3. **One TTS engine per scene.** A GPU step has a single ModelRef, so a scene that mixes engines fails with a clear validation error.
4. **The LoRA step has no ModelRef**, because the trainer is not a residency model until 9c. The kind is `train.lora`, to pick up the existing 2-hour timeout. Until the 9c engine exists, it fails with `engine_not_installed`.
5. **The worker now serves the `cpu` queue** (the media steps need it). Engine integration tests that ran their own River clients on `cpu` were racing the live worker (2 failures). They now use the `render` queue through a `testQueue` constant.
6. **`llm.scene_split` gets a 30-minute timeout** in `KindTimeoutOverrides`. The live split took 8 minutes, close to the llm queue's 10.
7. **`compose.yml` passes `API_RATE_LIMIT_PER_MINUTE` through** (default 100). The e2e script raises it to 1000 without touching `.env`. Otherwise the new spec used up the shared per-IP budget and the phase 6 spec got a 429.
8. **File layout:**
   - Additional files: `db/queries/{takes,media}.sql`, `openapi/schemas/{characters,presets}.yaml` and `openapi/paths/media.yaml`.
   - Shared components changed: `SceneCard` (srcset/sizes, placeholder, active) and `VirtualGrid` (gap, `aria-activedescendant`, Home/End). They had no other users.
9. **The generated client grows the shell bundle.** The authenticated shell is 166.76 KB against the 160 KB target (200 KB hard cap passes). All the generated query helpers live in one module, which Rollup keeps in the entry chunk.

## Follow-ups

- Once 9a and 9b install weights and engines, run image, voice and align on the GPU stack for a real episode. Check TTS parameter names against the 9b engines (`language`, `reference_url` with `consent=granted`, `output_key`, which match lane B's servicers today).
- Manual merge and split of scenes (named in the risk mitigation), and per-segment re-takes, are not built. A re-split and narration or segment edits are available.
- Intermediate per-segment voice objects under `derived/` are not deleted after the concat.
- The GPU steps have no admission check. On a stack without a GPU worker they stay "Queued for GPU" rather than failing.
- The 10 unrecognised speakers from the live split point to prompt tuning. They are flagged, not guessed.

## Unresolved questions

- Should the rollup's `pipeline_steps_scope_latest_idx` index stay in this phase's migration, since the pipeline tables are owned by phase 3? It is needed for the 60 ms budget.
- Is 166.76 KB for the authenticated shell acceptable, or should the generated client be split per domain?
