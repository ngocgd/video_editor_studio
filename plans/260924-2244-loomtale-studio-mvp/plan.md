---
title: "Loomtale Studio MVP"
description: "Phased build of the internal, SaaS-ready story-to-YouTube video studio: Go+River API, Python GPU workers, React SPA, YouTube publish and analytics."
status: pending
priority: P1
effort: 402h
branch: chore-aff-web-skill
tags: [go, river, postgres, minio, grpc, comfyui, ffmpeg, react, youtube, security, performance]
created: 2026-09-24
---

# Loomtale Studio MVP

Source of truth: [contract](../reports/brainstorm-260924-2128-story-video-studio-contract.md) (§2, §10, §11 NFRs as top priority), [tech stack](../../docs/tech-stack.md), [design guidelines](../../docs/design-guidelines.md), [wireframes](../../docs/wireframe/). Red-team adjudication: [reports/red-team-260924-2308-adjudication.md](reports/red-team-260924-2308-adjudication.md).

## Phases

| # | Phase | Effort | Depends on | Group | Status |
|---|---|---|---|---|---|
| 1 | [Repo scaffold, toolbox, CI, codegen, memory budget](phase-01-repo-scaffold-tooling-ci.md) | 14h | none | A | completed |
| 1b | [Blackwell/ComfyUI/Qwen-Edit smoke spike (gate)](phase-01b-blackwell-comfyui-smoke-spike.md) | 2h | 1 | B (with 2) | completed (conditional go) |
| 2 | [DB, auth, audit, storage, secrets, backups](phase-02-db-auth-security-foundation.md) | 24h | 1 | B | completed |
| 3 | [Job orchestration, GPU slot, SSE](phase-03-job-orchestration-sse.md) | 26h | 2 | C (with 4, 5) | completed |
| 4 | [Providers, gRPC, Python worker, llm-cli sidecar](phase-04-provider-layer-workers.md) | 24h | 2, 1b (VRAM baseline); pact with 3 | C | completed |
| 5 | [Frontend foundation and shared components](phase-05-frontend-foundation.md) | 18h | 2; step 7 needs 3 | C | completed |
| 6 | [Story writer, import, LLM settings UI](phase-06-story-writer-import.md) | 24h | 3, 4, 5 | D | completed (provider switch completing on a second provider pending: only claude-cli is available; Ollama variant deferred to 9c) |
| 7 | [Characters, storyboard, scene editor](phase-07-characters-storyboard.md) | 28h | 6 | E | completed (Ollama variant of the split pending by design: recorded with the LoRA/scoring phase and needs local model weights, and model downloads are paused by the user; render-piece staleness arrives with phase 8; open Medium: goose refuses lower-numbered migrations merged after a higher one on an existing database, awaiting a lead decision) |
| 8 | [Render pipeline and Library](phase-08-render-pipeline-library.md) | 28h | 3, 7 | F (with 9a→9c) | completed (pending: NVENC part of the manual GPU check, since the worker's static ffmpeg has no h264_nvenc and the libx264 fallback from the risk table is in place; resident-model part of that check pending because model downloads are paused by the user; open Mediums: concurrent StartRender can create two active runs, no sweep of orphaned scratch temp dirs, a restart supersedes the active run even when the fresh manifest hash is unchanged, and the migration-ordering question awaiting the lead) |
| 9a | [Manifest, ComfyUI, image engines](phase-09a-comfyui-image-engines.md) | 26h | 1b, 4; e2e steps need 7; UI needs 5 | F | completed (criteria needing model weights pending: model downloads paused by the user) |
| 9b | [TTS, align, Ollama LLM](phase-09b-tts-align-ollama.md) | 20h | 9a | F | completed (criteria needing model weights or engine runtime wheels pending: model downloads paused by the user) |
| 9c | [LoRA trainer, scoring, depth, sign-off](phase-09c-lora-scoring-benchmark.md) | 22h | 9b, 7 | F | completed (pyworker DINOv2 scoring, depth and ai-toolkit trainer engines, manifest entries, bench vision/train/train-smoke suites; character LoRA training on the pyworker trainer, image.score and image.depth scene steps, seeded LLM default migration. Pending: weight-dependent criteria and the model sign-off (model downloads paused by the user), trainer packaging awaits a user decision; open Mediums: train step dataset memory cap, seeded default needs a re-seed once an Ollama LLM is installed) |
| 10 | [Review and publish to YouTube](phase-10-review-publish-youtube.md) | 24h | 8, 9a, 9c | G | in progress (part 1 merged: Google OAuth channel connection with encrypted tokens, channels API and Settings > YouTube page, YouTube Data API client with resumable upload streamed from storage, quota ledger; remaining: part 2 (review queue and page, publications, pre-publish checks, thumbnails, scheduling; needs phases 8 and 9c part 2 on main), live criteria with a real Google OAuth app and channel pending (external)) |
| 11 | [Analytics (Analytics + Reporting API)](phase-11-analytics.md) | 18h | 10 | H | completed (tracked videos, daily Analytics API and Reporting API sync with look-back and quota ledger, Analytics page with channel overview, video table and detail with retention, suggestions with evidence and LLM explain, Dashboard channel and YPP widgets; pending: live criteria with a real Google OAuth app and channel (reach report type and metric combinations, real data, endpoint and sync-time budgets; external, first reach report arrives about 48h after job creation), the Dashboard "videos awaiting review" widget and tracking of app-published videos (source publication, owned by the review and publish part 2 branch) until phase 10 part 2 is on main) |
| 12 | [Hardening, E2E, acceptance, docs](phase-12-hardening-e2e-docs.md) | 16h | all | I | pending |

**Effort:** bottom-up base 314h + **contingency 28% (range 25–30% = 79–94h) = 88h → 402h total**. The former 208h estimate is superseded. <!-- RT#15 -->

Ordering: 1 is serial; 1b and 2 run in parallel (disjoint files). 3, 4 and 5 own disjoint trees (`api/internal/pipeline`; `api/internal/providers` + `proto/` + `workers-python/` + both compose files; `web/`). Group F runs phase 8 beside the serial chain 9a → 9b → 9c (they share `compose.gpu.yml`, `models/manifest.yaml`, `pyworker.Dockerfile` and the single GPU). Before 9b every phase is testable without a large model: the claude CLI is real, the Ollama service runs without a model, and GPU steps return an honest `engine_not_installed`.

## Shared-file rules (parallel safety)

- `openapi/root.yaml` only lists `$ref`s; each phase owns `openapi/paths/<domain>.yaml` and `openapi/schemas/<domain>.yaml`. `openapi/schemas/gpu.yaml` (full `GpuStatus`) is owned by phase 3; phases 4, 8, 9a–9c only populate it through Go interfaces. <!-- RT#15 -->
- Migrations use goose timestamp names; each phase owns its files and `db/queries/<domain>.sql`. Generated code is never hand-edited; `make gen-check` fails CI on drift. All protos are defined in phase 4 and generated by one `buf generate` (Go + Python). <!-- RT#15 -->
- `api/cmd/*` wiring is owned by phase 3; other phases export constructors and the lead wires them at merge. In group C, phase 4 owns both compose files.
- Config is loaded per package (`caarlos0/env`), so there is no shared config struct.

## Acceptance criteria (contract §2) → phases

| AC | Criterion | Built in | Verified in |
|---|---|---|---|
| 1 | EN episode ≥30 min, 1080p MP4, voice, aligned subs, ≥2 consistent characters | 6, 7, 8, 9a–9c | 12 |
| 2 | Same in Vietnamese | 6, 7, 8, 9a–9c | 12 |
| 3 | Imported chapter → video | 6 (import) + 7–9c | 12 |
| 4 | Per-step rerun, per-scene rerender, resume after shutdown | 3, 7, 8 | 3, 8, 12 |
| 5 | Realtime per-job progress + GPU queue in UI | 3, 5 | 5, 12 |
| 6 | Private/scheduled upload + thumbnail + metadata + AI disclosure flag | 10 | 10, 12 |
| 7 | Analytics: views, watch time, CTR per video (Reporting API, ≈2-day lag), retention | 11 | 11, 12 |
| 8 | Switch LLM Ollama (seeded default) ↔ Claude ↔ Gemini by config, no code change | 4, 6, 9c | 4, 12 |

§11 budgets and the security list are enforced in every phase's "Performance budget checks" and "Security checklist".

## User decisions (2026-09-24)

- Apply all 15 red-team findings (below).
- `.wslconfig` already raised by the controller to memory=20GB, processors=12, swap=16GB (backup `C:/Users/ADMIN/.wslconfig.bak-260924`); phase 1 step 0 only verifies `docker info` after `wsl --shutdown`. Services are budgeted for a 20GB VM (phase 1 table).
- Edit during an active render is allowed: the running render run is superseded and a new run is created from a fresh manifest, reusing unchanged segments (phase 8).
- CTR per video via the YouTube Reporting API reach report added to phase 11 (+8h); AC7 keeps CTR per video with ≈2-day lag; evidence may use an existing public video.
- Ollama is the required local default LLM, seeded as default after phase 9c sign-off.
- Phase 9 split into 9a/9b/9c; a 2h Blackwell smoke spike (phase 1b) runs right after phase 1, with downloads user-approved at execution time.
- Bottom-up re-estimate with a 25–30% contingency line (above).

## Validation Log

### Session — 2026-09-24 (4 questions, all answered)
1. **Backup target:** encrypted cloud object storage (Cloudflare R2 or Backblaze B2). Backups are encrypted client-side before upload; credentials come only from secrets. Applied to phase 2.
2. **Claude CLI fallback:** if subscription-token auth fails inside the `llm-cli` sidecar, run the same sidecar as a small host-side Windows process using the host's existing Claude Code login, reached from containers over a localhost-only endpoint with a shared-secret header. The anthropic-api adapter stays available but is not the fallback. Applied to phase 4.
3. **CSP:** `style-src 'self' 'unsafe-inline'`, `script-src 'self'`, Trusted Types, `object-src 'none'` — confirmed.
4. **Execution:** sequential, one phase at a time (1 → 1b → 2 → …), pausing after each phase for user review and the context rule. No parallel multi-agent execution.

Verification: plan already carries red-team evidence; no unresolved `[UNVERIFIED]` tags remain beyond the ones owned by live-check steps (P10 step 2 quota, P11 step 1 report id).

### Session — 2026-09-26 (execution change, supersedes decision 4)
1. **Execution:** two parallel lanes run by agent teams, each phase implemented by one agent and then verified by an independent agent (review, fixes, `make ci`, integration, e2e, success criteria). Lane A: 6 → 7 → 8. Lane B: 9a → 9b. Then 9c (needs 7 and 9b), 10, 11 and 12 run in order.
2. **Merge gate:** a phase that passes verification is merged into `main` and pushed without pausing for review; the user reads the cook report afterwards.
3. **Model downloads:** pre-approved for every model pinned in `models/manifest.yaml` (phases 9a–9c).
4. **Shared resources:** code and unit tests run in parallel; anything that brings up a Docker stack (integration, e2e, GPU steps) and every merge into `main` runs under a shared lock (`.claude/locks/with-lock.sh heavy|merge`), so only one full stack runs at a time.

## Execution rules

- **Parallel lanes** (from 2026-09-26, see Validation Log): one worktree and branch per phase under `.claude/worktrees/`; the lead wires `api/cmd/*` conflicts at merge; generated code is regenerated after merging `main` into the phase branch, never hand-merged.
- **Context rule:** at every phase boundary, if context usage is at 40–50% or more, stop and ask the user to run `/compact` before the next phase.
- One phase = one branch/PR; `make ci` (inside the toolbox) green before a phase is done. No plan/phase/finding IDs in code, migrations, tests or commits (conventional commits, no AI references).
- A phase is complete only when its success criteria are observable. Model downloads happen only in 1b (user-approved) and 9a–9c.

## Red Team Review

### Session — 2026-09-24
**Findings:** 15 (15 accepted, 0 rejected; 1 raw scope finding rejected)
**Severity breakdown:** 5 Critical, 10 High

| # | Finding | Severity | Disposition | Applied To |
|---|---|---|---|---|
| 1 | River rescue (15m) < job timeouts; batch attempt cannot fence per step; two rescue paths race | Critical | Accept | P3 (CAS claim, 4h rescue, step-ID args, regroup), P12 |
| 2 | Fan-in lost wakeup; SSE LISTEN loss; quota hook missing; SSE cap vs multi-tab | Critical | Accept | P3 (remaining_deps, ready/resync/version, admission checks, per-user cap), P5 (leader tab), P2 |
| 3 | YouTube upload not idempotent; thumbnails need verified channel; quota unverified; CTR only in Reporting API | Critical | Accept | P10 (CAS, nonce dedupe, session-first, eligibility, 1,600-unit ledger), P11 (Reporting API), P12 |
| 4 | Docker VM had 8GB/4 CPU; no RAM budget | Critical | Accept | P1 (verify step, mem_limit table), P1b, P9a (GGUF Q4, go/no-go), P12 |
| 5 | VRAM already ≈5.4GB used; unloads async; OOM retried identically; trainer outside residency; nvidia-smi unreliable | Critical | Accept | P3 (render reserve, gpu_oom), P4 (measured budget, VRAM polling, app-side proof), P8 (CPU filters), P9a, P9c |
| 6 | Static queue vs runtime provider; interactive starved; advisory lock loss | High | Accept | P3 (Queue(ctx, StepRef), priorities, chunks, try-lock + watchdog), P4, P6, P7 |
| 7 | Ollama silently demoted to optional | High | Accept | P4, P6, P7, P9b, P9c, P12, plan.md |
| 8 | 10-min presigned URLs used by multi-hour streams; one signing client | High | Accept | P2 (two presigners), P4, P8, P10 (per-chunk range GET) |
| 9 | No render input snapshot → mixed takes, A/V drift | High | Accept | P7 (scenes.Changed hook), P8 (manifest, supersede), P12 |
| 10 | No backups; disk budget ignores Docker/VHD/renders | High | Accept | P2 (nightly backup, WAL), P5, P8 (watermark, TTL), P1b/P9a (one Qwen-Edit revision, pre-flight), P12 (restore drill) |
| 11 | claude CLI shares worker secrets; shell argv; credentials.json fallback | High | Accept | P4 (llm-cli sidecar, []string argv, init tools assert, fallback dropped), P12 |
| 12 | Derived text trusted; LLM character UUIDs trusted; public metadata exposure | High | Accept | P4 (DataBlock + taint), P6 (provenance), P7 (name mapping), P10 (URL confirm), P12 |
| 13 | Model supply chain, pickles, open egress, docker.sock; FFmpeg whitelist inconsistent | High | Accept | P1, P4 (gpu_net internal, offline), P7 (single whitelist, forced -f), P8, P9a–9c |
| 14 | URL leaks in logs; log authz; audit not append-only; tenant resolution; POST key; CSP; unauth ZAP | High | Accept | P2, P3, P5, P6, P7, P8, P10, P12 |
| 15 | 208h not credible; riskiest assumption tested late; zoompan unbenchmarked; shared /gpu schema; two proto toolchains | High | Accept | plan.md, P1 (buf), P1b, P3 (GpuStatus), P8 (zoompan gate), P9a–9c split, P12 (tech-stack names) |

Rejected raw finding: SC9 (ZAP/k6/nightly/SSE benchmark exceed scope) — security and scale are the user's top priority (contract §11); its SSE-cap part is merged into #2.

### Whole-Plan Consistency Sweep

Swept all 16 plan files on 2026-09-24 for superseded terms. Remaining hits are intentional.

| Superseded term | Result |
|---|---|
| `RescueStuckJobsAfter (15m)` | Removed; P3 uses 4h > longest timeout, with per-kind overrides asserted below it |
| `{StepIDs []uuid, Attempt int}` | Removed; args are `{StepIDs []uuid}`, attempt lives in the DB CAS |
| `Ollama, optional` / optional profile | Removed; Ollama is a required service (P4), model in P9b, default seeded in P9c |
| `credentials.json` | Removed from phases; fallback is the host-side llm-cli process (Validation Log 2) |
| `Queue() string` | Replaced by `Queue(ctx, StepRef)` (P3) and `registry.QueueFor` (P4) |
| `file,https,tls,tcp` / `file,http,https` | Replaced by one whitelist constant in P7 (`https,tls,tcp` remote, `file` temp-dir only) |
| `≤15.0GB per model` | Replaced by the measured `budget_mb` (≈10.9GB free minus render reserve) |
| `phase-09-local` | File deleted; links point to 09a/09b/09c; bare "phase 9" remains only in 9a–9c titles ("split of the former phase 9") |
| `208h` | Appears only in the effort line above and this table, as superseded |
| `style-src 'self';` | Replaced by the decided CSP in P2 (`style-src 'self' 'unsafe-inline'`, Trusted Types, `object-src 'none'`) |
| Also checked | `grpc_tools` (only as "not used", P1), `UntrustedDoc` (none), trainer container / `Train` in worker.proto (none), `≤4 streams per session` (none), Report-Only (only as forbidden), `nvidia-smi` (only as "not used", P4) |

Remaining contradictions (unresolved): none found in the plan files. `docs/tech-stack.md` still names `docker-compose.yml` / `docker-compose.gpu.yml`; it is outside this plan dir and is corrected by the P12 docs step.

## Progress notes

- 2026-09-25: phase 1 done ([report](reports/cook-260925-phase-01-scaffold.md)). Docker VM verified at 20GB / 12 CPU. Follow-ups: `/readyz` returns 503 on a fresh `compose up` until the MinIO app key is wired by hand, so phase 2's secrets work should automate that; host-measured `/healthz` p95 is 10.9ms because of the WSL2 NAT hop (the handler itself takes <1ms).
- Model weights are in Docker volume `loomtale_models` ([report](../reports/spike-260924-model-downloads.md)). Phase 1b and 9a must mount this volume, not `models`. Z-Image Turbo was downloaded as int8, which phase 1b has to confirm ComfyUI supports.
- 2026-09-25: phase 1b ended in a conditional go ([report](reports/cook-260925-phase-01b-smoke-spike.md)). sm_120 works with PyTorch 2.9.1+cu128. Z-Image Turbo int8 is a clean go at 25s per 1024px image. Qwen-Image-Edit-2509 Q4 takes 184–244s per edit with a VRAM peak of ≈14.1GiB, so phase 9a limits it to character sheets, runs it with no other GPU user, and re-measures it under normal desktop load. A research update ([report](../reports/researcher-260925-0947-image-models-adoption-update.md)) recommends Qwen-Image-Edit-2511 over 2509 and Chroma1-HD as a trial; both are pending the user's download approval.
- 2026-09-25: phase 2 done ([report](reports/cook-260925-phase-02-db-auth-security.md)). The phase 1 `/readyz` 503 follow-up is resolved: the MinIO app key now comes from operator-provisioned `.env` values (never generated/printed at runtime), so a fresh `docker compose up --wait` reaches a healthy `/readyz` with zero manual steps, verified end to end. Follow-ups: the nightly backup's cloud upload (`mc` to a real Cloudflare R2/Backblaze B2 bucket) is implemented but unverified without live credentials — `pg_dump`, the `age` encrypt/decrypt round-trip, and MinIO's S3 API were each verified independently instead; a real-bucket check is worth doing before or during phase 12's restore drill. `github.com/getkin/kin-openapi` bumped 0.142.0→0.144.0 for a `govulncheck`-flagged nil-pointer panic reachable from the new request validator.
- 2026-09-25: phase 2 merged after a security review ([review](../reports/code-reviewer-260925-1203-phase-02-security-review.md)). All High and Medium findings were fixed; details are in the phase 2 cook report. Still open: a real R2/B2 backup upload (moved to the phase 12 restore drill) and a test for secrets leaking into captured logs. Model changes: Qwen-Image-Edit-2511 replaced 2509 (deleted), and Illustrious-XL v1.1 plus a Xianxia LoRA were added ([style comparison](../reports/style-compare-260925-xianxia-models.md)). Proposed default: Illustrious + LoRA for style, with pose control or a Z-Image layout pass in phase 9a; this awaits the user's style choice.
- 2026-09-25: phase 3 merged after a correctness and security review ([review](../reports/code-reviewer-260925-1508-phase-03-orchestration-review.md)). The Critical, all High and all Medium findings were fixed. The integration suite passed twice with `-race` while the live worker was running: 52/52 tests, 0 flakes. `GET /jobs` p95 is 0.2ms at 120k rows. Clients cannot set priority; the server derives it from the step kind. A worker without a handler for a step snoozes it instead of failing it. `.gitattributes` now forces LF, because CRLF checkouts broke the container scripts on Windows. Still open: a dedicated test for concurrent quota enqueue, and a SIGKILL two-process crash test.
- 2026-09-25: phase 4 merged after review ([review](../reports/code-reviewer-260925-1830-phase-04-providers-review.md)). All High and Medium findings were fixed, and the llm-cli sidecar and egress-proxy isolation were verified live. A `worker_status` heartbeat table now feeds `/gpu` and provider availability. Anthropic and Gemini use direct REST, which was accepted. Process-wide BYOK is refused in SaaS mode, and per-tenant keys come in phase 6. MinIO has an internal alias on the GPU network. The live Claude path is still gated on the user running `claude setup-token`, and the host-side fallback is built but has not been tested live. The WSL VM was lowered to 12GB with autoMemoryReclaim so the user could game; raise it back to 20GB before phase 9a (ComfyUI).
- WSL VM back at 20GB (autoMemoryReclaim=gradual and sparseVhd kept), so phase 9a's ComfyUI budget holds again.
- 2026-09-25: phase 5 merged after review ([review](../reports/code-reviewer-260925-2141-phase-05-frontend-review.md)). All findings were fixed. First paint is 124–139KB gzip, and the SSE bridge now handles leader-tab handoff, reconnects after a clean close, caps topics at 50, and gates on version. Known gaps: dashboard stat cards and the GPU bar lag live SSE by up to 15s (they are polled), and the GPU panel is polled rather than pushed, which needs a tenant GPU topic in the API later.
- 2026-09-26: Follow-up fixes from the merged phases' reviews landed on main ([report](reports/cook-260926-review-follow-ups.md), [review](../reports/code-reviewer-260926-2217-review-follow-ups-review.md)): a model pull that hits its deadline now ends in a resumable failed install and staging downloads are serialised per file; worker output transfers treat forbidden or expired URLs as retryable; integration logins share one per-IP budget helper (`isolateLoginIPBudget`); draft writes run in one transaction with step-apply idempotency and size bounds; the tenant-query lint covers the story tables; the LLM settings test returns `latencyMs`; importer, bible editor, glossary seed and translate token-cap fixes. Pending: live model pull and GPU criteria (model downloads paused by the user); one non-blocking Medium from the review (20000-character paragraph bound versus the blank-line-only paragraph split).

## Resume checkpoint: phase 6 (2026-09-26 ~01:00)

- Phase 6 code is on local branch `feat/story-writer-import` (not pushed). The branch lives in worktree `C:/Users/ADMIN/orca/projects/aff-ytb-ntNocj/.claude/worktrees/agent-aabe929bc40d9c7df`. Commits: `c520c07` (feature bulk), `dd7287d` and `835a776` (WIP: GetAiActionResult endpoint). Everything is committed.
- Remaining steps:
  1. Run `cd web; npm run gen` to pick up GetAiActionResult.
  2. Change `web/src/features/writer/use-ai-action.ts` to poll GetAiActionResult (~400ms while pending/queued/running) instead of the dead `llm.delta` SSE path.
  3. Squash the WIP commits into conventional commits.
  4. Run the full verification: tb ci; the integration suite on `-p loomtale-p6` (note the known login-rate-limit bucket artifact when running the whole suite); web typecheck, lint, test, build and budget; and the Playwright spec `web/e2e/writer-import-settings.spec.ts`.
  5. Save screenshots into `reports/phase-06-screens/`.
  6. Write the cook report `reports/cook-260925-phase-06-story-writer.md`.
  7. Review, merge and push, the same way as phases 2â€“5.
- Why this stopped: the subagent first hit its weekly limit (since reset). After that, the Bash tool in this session failed on every command with `line 166: expor: command not found`, because the harness had cached a truncated session-env script. Restarting Claude Code fixes it. The session-env hook files were deduplicated, and the backups are `*.bak` next to them.
