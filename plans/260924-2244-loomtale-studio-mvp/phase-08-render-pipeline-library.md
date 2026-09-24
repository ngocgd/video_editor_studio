# Phase 08: Render pipeline (manifest, Ken Burns/parallax, subtitles, NVENC, chunked long render, preview) and Library

## Context links
- [plan.md](plan.md) · [contract §2 AC1 (≥30 min 1080p MP4, subtitles), AC4 (rerender one scene); §4 Render, Library](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- Wireframe: [render-queue](../../docs/wireframe/render-queue.html) (render settings panel, stage summary, estimate)
- [model research §5 (VI segment-level subtitles), §6 (static + motion default)](../reports/researcher-260924-2128-local-ai-models-rtx5060ti.md)
- Decision: **no background music** (contract §10). Narration only, and the UI states it.
- User decision 2026-09-24: editing during an active render is allowed; the running render run is superseded and a new run is created from a fresh manifest, reusing unchanged segments. <!-- RT#9 -->
- Depends on phase 3 (steps) and phase 7 (scenes, takes, FFmpeg runner, `scenes.Changed`). Runs in parallel with phases 9a–9c.

## Overview
- Priority: P1 · Status: pending · Effort: 28h <!-- RT#15 re-estimate -->
- This phase delivers a deterministic, segment-cached render driven by a frozen **render manifest**. Each scene body and each transition is an independently encoded segment with a closed GOP, and the final episode is a concat plus a loudness-normalised narration track plus subtitles. Editing one scene re-encodes only that scene and its two transitions. It also delivers the QC report read by phase 10, preview proxies, disk safety (watermark, TTL cleanup) and the Library page.

## Requirements
- `render_settings` per episode and language: 1920×1080, 30fps, encoder `auto` (probe `h264_nvenc` with a 1-frame test encode, else `libx264`), subtitles `burn|srt|both`, subtitle style (Literata 42px bottom, 2px shadow), default motion, crossfade 0.6s, loudness −14 LUFS / −1 dBTP.
- **Render manifest** <!-- RT#9 -->: `POST /episodes/{id}/renders` freezes `render_manifests(id, tenant_id, episode_id, lang, settings_hash, scenes jsonb [{scene_id, idx, image_asset_id, image_sha256, voice_asset_id, voice_sha256, align_asset_id, motion, duration_frames}], hash)` in one tx, then creates the run. Every render step reads only from its manifest, never from live scene rows, so takes cannot change under a running render. Compose hash = manifest hash.
- **Supersede on edit** <!-- RT#9 -->: phase 8 registers a `scenes.Changed` handler. If the episode/lang has an active render run, it calls `pipeline.SupersedeRun(old, new)`: the old run's queued steps are canceled, running steps receive ctx cancel (their outputs, if already committed, stay as reusable content-addressed segments), and a new run is created from a fresh manifest. Unchanged segments are cache hits. The UI shows "Render restarted after edit (N segments reused)". Rapid edits are debounced 5s per episode.
- Motion presets: `static`; `kenburns` (push-in/out and pan with eased expressions, seeded per scene; source pre-scaled 2×); `parallax25d` (two layers from a depth mask) — needs the phase 9c depth engine, disabled until then with the reason "depth model not installed".
- **CPU-only filters** <!-- RT#5 -->: all filtergraphs run on CPU; NVENC is used only as the encoder. No CUDA filters (`scale_cuda`, `overlay_cuda`, hwupload) while a model can be resident, so render never competes for model VRAM beyond the encoder session.
- Segments: `render.scene_body` (duration = voice duration − transition overlaps), `render.transition` (xfade tail i → head i+1). Identical codec params and timebase, forced keyframe at segment start, `-bf` compatible with concat.
- Audio: `render.audio_master` concatenates the manifest's voice takes on the exact timeline, runs two-pass `loudnorm`, AAC 192k.
- Subtitles: `render.subtitles` builds SRT/ASS from the manifest's alignment takes, offset by scene start. EN word-level grouped ≤42 chars × 2 lines; VI segment-level. Burn-in per scene body, so caching still works.
- Compose: `render.compose` downloads cached segments into its temp dir and runs the concat demuxer (`-f concat -safe 1`, local relative names) `-c copy` + audio mux + soft subtitle (`mov_text`) + `+faststart`. Output: `renders(episode_id, lang, manifest_id, settings_hash, asset_id, sha256, duration_ms, report jsonb)`.
- **Storage access** <!-- RT#8 RT#14 -->: workers read inputs through `storage.Internal` presigned URLs with TTL = step timeout + 10 min (never the 10-min browser presigner). Segment keys `t/{tenant}/render/{episode}/{lang}/seg/{input_hash}.mp4` are under the workers-only `render/` prefix. On a cache hit the segment is reused only if the object's stored `x-amz-checksum-sha256` equals `assets.sha256`; a mismatch deletes the asset row and re-encodes.
- Preview: 540p proxies per scene and for the full episode (range-request playback through browser presigned GET).
- QC report (jsonb, read by phase 10): integrated LUFS and true peak (`ebur128`), max subtitle drift, missing or placeholder scenes (must be 0), duration, stream checks (`ffprobe`), per-scene image score (filled by phase 9c), final render sha256.
- **Disk safety** <!-- RT#10 -->: an `AdmissionCheck` (`diskguard.Watermark`) registered with `pipeline.Enqueue` refuses render and model-pull enqueues when free space on the Docker data disk is below 40GB (warning chip at 60GB), accounting for Docker images and VHD growth rather than only app data. A periodic `library.ttl_cleanup` step (daily) deletes unreferenced segments older than 14 days and unselected takes older than 30 days (never anything referenced by a manifest, render or publication).
- The render page follows the wireframe: stage summary (Script/Scenes/Images/Voice/Subtitles/Compose/Encode), settings panel, estimate, "Render episode" disabled with the reason while prerequisites are missing or the disk watermark is hit.
- Library: virtualized asset table (kind, project, size, created, referenced-by), storage usage per project, manual cleanup (dry-run preview → confirm → audit, runs as a step), and the TTL settings.

## Architecture
Render DAG per manifest: `scene_body[i]` per scene, `transition[i,i+1]` per pair, `audio_master` and `subtitles` depend on the manifest's voice and align takes, `compose` depends on all of them, `preview` on `compose`. Every node's `input_hash` derives from manifest entries only; an unchanged hash with a verified checksum is a cache hit with no job.
- Render steps run on the `render` queue (concurrency 2) and are admitted only when `GpuProbe` shows the render VRAM reserve free (phase 3). `EncoderInfo` feeds the `encoder` field of `/gpu` (schema owned by phase 3).

## Related files
- Create:
  - `db/migrations/*_renders.sql`, `db/queries/{renders,manifests,library}.sql`
  - `api/internal/render/{manifest,supersede,plan,motion,segments,audio,subtitles,compose,qc,encoder}.go`
  - `api/internal/diskguard/`, `api/internal/library/`
  - `openapi/paths/{renders,library}.yaml`, `openapi/schemas/render.yaml`
  - `web/src/features/{render,library}/`, `web/src/routes/_app/projects/$seriesId/render/**`, `web/src/routes/_app/library.tsx`
- Modify: `openapi/root.yaml`, `deploy/docker/worker.Dockerfile` (FFmpeg with nvenc, libass, libwebp, libaom/svt-av1 at a pinned version). `compose.gpu.yml` is owned by phases 9a–9c during the parallel window; `NVIDIA_DRIVER_CAPABILITIES=compute,video,utility` is already set in phase 1.

## Implementation steps
1. Write the encoder probe (nvenc vs x264), cached per worker boot, exposed through `EncoderInfo`.
2. Write the motion filtergraph builders (static, kenburns, parallax behind a capability flag) as pure functions with golden argv tests. **Gate: zoompan micro-benchmark** <!-- RT#15 --> — encode a 60s 1080p30 kenburns segment with the chosen filtergraph on the 12-vCPU VM (render concurrency 2) and record fps. Pass if a 60-min episode's bodies fit the ≤15 min budget below at that fps; otherwise switch kenburns to the cheaper `crop`+`scale` per-frame expression path (or a 1.5× pre-scale), re-measure, and record the chosen path and numbers in the phase report before step 3.
3. Write the manifest freeze, the segment planner (integer frame counts at 30fps), hashes and DAG creation through `pipeline.Enqueue`.
4. Write the body and transition encoders with closed-GOP params and checksum-verified cache hits.
5. Write the audio master (two-pass loudnorm) and the subtitles builder (SRT/ASS, EN word grouping, VI segments).
6. Write compose, QC and preview, plus the render report with the final sha256.
7. Write the supersede handler on `scenes.Changed` (debounced) and the disk watermark admission check.
8. Build the render page UI and the scene preview in the storyboard inspector.
9. Build the Library table, usage, manual cleanup with dry-run, and the TTL cleanup periodic step.

## Todo checklist
- [ ] Encoder probe
- [ ] Motion builders + zoompan benchmark gate (+ parallax gated)
- [ ] Manifest freeze + segment planner + hashing + DAG
- [ ] Body/transition encoding + checksum-verified cache
- [ ] Audio master + subtitles
- [ ] Compose + QC + previews
- [ ] Supersede on edit + disk watermark
- [ ] Render UI + inspector preview
- [ ] Library + cleanup + TTL

## Performance budget checks
- Full 60-min episode with NVENC: all segments ≤15 min wall time from a cold cache (gate numbers from step 2). Concat plus mux of cached segments ≤60s. Measured in phases 9c/12 on the real GPU; CI runs libx264 on 60s fixtures only.
- A single-scene edit re-encodes ≤3 segments plus the audio master plus the concat, ≤90s for a 40-min episode; a supersede reuses every unchanged segment.
- Worker memory ≤1.5GB during compose (within the 3g `mem_limit`). Temp disk ≤2× the largest segment set.
- The render page SSE shows per-stage counters without refetch. The Library table is virtualized with cursor paging.

## Security checklist
- [ ] FFmpeg only through the phase 7 runner (single whitelist constant, forced `-f`, `-safe 1`, host/path validation, `-loglevel error`, scrubbed stderr)
- [ ] Subtitle text escaped for ASS (braces, backslashes) to prevent override-tag injection
- [ ] Temp dirs per step (`0700`), cleaned on success/failure; disk watermark before enqueue
- [ ] Render keys workers-only; cache hits checksum-verified; final render sha256 recorded for phase 10
- [ ] Cleanup is owner/editor only, dry-run first, audited; never deletes anything referenced by a manifest, render or publication
- [ ] Browser preview URLs ≤10 min; internal URLs never leave the worker

## Reuse points
- Reuse `media/ffmpeg` (phase 7), `pipeline.*` (`SupersedeRun`, `AdmissionCheck`), `scenes.Changed`, `storage.Internal`, `JobProgress`, `PipelinePips`, `VirtualTable`, `InlineError`, `duration.Estimate`.
- **Pact with phase 9c:** QC reads per-scene image scores from `scene_takes.params.score` when present, and parallax is enabled when `/gpu` `capabilities` contains `depth`. Phase 8 never edits phase 9a–9c files.
- Create `render.QCReport` and `render.Timeline` (consumed by phase 10) and `diskguard.Watermark`.

## Tests
- `scripts/tb.ps1 test`: golden filtergraphs, frame-exact timeline math, ASS escaping, hash stability, manifest hash determinism.
- `scripts/tb.ps1 test-integration` (libx264, lavfi fixtures):
  - a 12-scene render; ffprobe asserts duration ±1 frame, 1 video + 1 audio + 1 subtitle stream, a keyframe at each segment start, LUFS within ±1 LU
  - editing scene 5 re-encodes exactly body 5 and transitions 4→5 and 5→6
  - **edit during render**: a take change mid-render supersedes the run; the new run reuses unchanged segments and the output never mixes old and new takes (A/V offsets checked)
  - a tampered cached segment (checksum mismatch) is re-encoded, not reused
  - killing the worker during compose; the resume completes (AC4)
  - disk watermark: enqueue refused with 507 when the threshold is faked
- GPU check (manual, compose.gpu): `ENCODER=nvenc` renders the fixture, the report records `encoder: h264_nvenc`, and a concurrently resident model is not evicted.

## Success criteria
- A fixture episode renders to a playable 1080p MP4 with narration, soft or burned subtitles and a QC report. Rerendering one scene touches only that scene's segments (AC4).
- An edit during a render produces a superseded run and a consistent new render.
- The zoompan gate result is recorded and the 60-min budget holds with the chosen path.
- The render page and Library match the wireframes' functional zones; "No background music" is stated.

## Risks + rollback
- Boundary glitches in concatenated segments (Medium×High): identical encoder params, closed GOP, fixed timebase, a boundary decode test, and a fallback full re-encode compose flag.
- NVENC missing in the WSL2 container (Low×Medium): the probe falls back to x264; the UI shows the encoder.
- Kenburns zoompan too slow on CPU (Medium×Medium): the step 2 gate decides the path before the planner is built; CUDA filters are not an option while models share the GPU.
- Supersede thrash from rapid edits (Medium×Low): 5s debounce; segments already encoded are reused.
- Disk exhaustion (Medium×High): watermark admission, TTL cleanup, Library cleanup, nightly backup off C:.
- Rollback: new tables, routes and steps only. Revert the PR (unregister the hook and admission check).

## Next steps
Phase 10 consumes renders, manifests and QC. Phase 9c enables parallax and the image scores.
