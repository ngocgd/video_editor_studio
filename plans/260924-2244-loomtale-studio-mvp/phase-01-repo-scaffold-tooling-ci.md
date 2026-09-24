# Phase 01: Repo scaffold, toolbox, CI, codegen pipeline

## Context links
- [plan.md](plan.md) · [contract §5, §11](../reports/brainstorm-260924-2128-story-video-studio-contract.md) · [tech-stack](../../docs/tech-stack.md)
- [backend research: folder structure, compose layout](../reports/researcher-260924-2128-backend-architecture-goclaw.md)
- Host facts, verified 2026-09-24: Docker 29.2.1, Node v20.18.1, Python 3.12.10, git and gh are present. **Go, make, task and buf are missing.**
- Host WSL limits, verified 2026-09-24: `C:/Users/ADMIN/.wslconfig` already raised by the controller to `memory=20GB`, `processors=12`, `swap=16GB` (backup `C:/Users/ADMIN/.wslconfig.bak-260924`). It takes effect only after `wsl --shutdown` / Docker Desktop restart. <!-- RT#4 -->

## Overview
- Priority: P1 (blocks everything) · Status: pending · Effort: 14h <!-- RT#15 re-estimate -->
- This phase delivers a single-repo monorepo with one containerised toolchain, so the host needs only Docker, Node and git. It also delivers a single-source codegen pipeline (SQL → sqlc, OpenAPI → Go server plus a TS client with Zod, proto → Go and Python via one `buf generate`) and CI with the §11 security scanners. A thin vertical slice (`/healthz` → generated TS client → web page) proves the whole pipeline. It fixes the per-service memory budget that every later phase applies.

## Requirements
- The toolchain must not need a host Go install. The `toolbox` image (Go, Node 22, uv, buf, sqlc, goose, oapi-codegen, golangci-lint, govulncheck, air, make) is the only place the Go tools run. The host wrapper `scripts/tb.ps1` / `scripts/tb.sh` runs `docker compose -f deploy/compose.tools.yml run --rm toolbox make <target>`.
- Host Node must be upgraded to 22 LTS (`nvm install 22 && nvm use 22`), because Vite 7 needs Node ≥20.19 or ≥22.12 and the host has 20.18.1. Web dev runs on the host for fast HMR.
- Go tool versions are pinned with `tool` directives in `api/go.mod` (Go ≥1.24 feature). CI and the toolbox use the same pins (DRY).
- Generated code is committed. `make gen-check` regenerates and fails on `git diff`.
- **One proto toolchain:** `buf generate` produces both the Go stubs and the Python stubs (remote or local `protocolbuffers/python` + `grpc/python` plugins, pinned in `buf.gen.yaml`). `grpc_tools.protoc` is not used. <!-- RT#15 -->
- The compose core runs postgres, minio, minio-init (bucket, CORS, lifecycle), api, worker and web (Caddy serving the built SPA). `deploy/compose.gpu.yml` exists with the worker's NVENC device reservation and `NVIDIA_DRIVER_CAPABILITIES=compute,video,utility`; later phases add services to it.
- All images run as non-root, have pinned base digests, and have a healthcheck. Every service declares `mem_limit` (and `cpus` where noted) from the budget table below. <!-- RT#4 -->

### Memory budget (Docker/WSL VM = 20GB RAM, 12 vCPU) <!-- RT#4 -->
`mem_limit` is a ceiling, not a reservation. Only one GPU-backed service is active at a time (single GPU slot), so the worst concurrent case is the always-on core plus render plus one GPU service.

| Service | Phase that adds it | mem_limit | Notes |
|---|---|---|---|
| postgres | 1 | 1.5g | `shared_buffers=512MB`, `max_wal_size=2GB` (phase 2) |
| minio (+ minio-init one-shot) | 1 | 1g | |
| api | 1 | 512m | |
| worker (Go + ≤2 FFmpeg) | 1 | 3g | `cpus: 8`, render queue concurrency 2 |
| web (Caddy) | 1 | 128m | |
| backup (nightly, idle otherwise) | 2 | 512m | |
| llm-cli sidecar + egress-proxy | 4 | 512m + 64m | |
| pyworker (TTS, align, vision, trainer) | 4 / 9b / 9c | 8g | trainer peak; TTS/align ≈4g |
| comfyui | 9a | 10g | GGUF Q4 staging with CPU offload |
| ollama | 4 (model 9b) | 3g | weights live in VRAM; mmap |
| toolbox (dev only) | 1 | 2g | not running during renders |

Worst concurrent: core ≈6.8GB + comfyui 10GB = 16.8GB, leaving ≈3GB for the WSL kernel and page cache. The go/no-go gate for this budget is phase 1b (smoke) and phase 9a (full image stack).

## Architecture
```
/                         Makefile (all targets) · .github/workflows/ci.yml · .gitleaks.toml · .editorconfig
api/                      go.mod (module loomtale/api) · cmd/api · cmd/worker · cmd/loomtale (CLI: migrate, create-owner, bench)
  internal/<domain>/      handlers, services, per-package env config
  internal/db/gen/        sqlc output (generated)
  internal/httpapi/gen/   oapi-codegen strict chi server + types (generated)
db/migrations/            goose SQL (timestamp names) · db/queries/<domain>.sql · sqlc.yaml
openapi/                  root.yaml + paths/*.yaml + schemas/*.yaml → bundled openapi.gen.yaml (redocly)
proto/loomtale/worker/v1/ *.proto · buf.yaml · buf.gen.yaml (Go + Python)
workers-python/           uv project, src/loomtale_worker, tests/
web/                      Vite + React + TS · src/api/gen (hey-api: client-fetch + zod + tanstack-query plugins)
comfyui/workflows/        API-format workflow JSON (phase 9a)
deploy/                   compose.yml · compose.gpu.yml · compose.tools.yml · docker/*.Dockerfile · caddy/Caddyfile
scripts/                  tb.ps1 · tb.sh
```
Codegen data flow: `openapi/*.yaml` → `redocly bundle` → `openapi.gen.yaml` → (a) `oapi-codegen` produces `api/internal/httpapi/gen`, and (b) `@hey-api/openapi-ts` produces `web/src/api/gen`. The same bundle feeds the Go request-validator middleware in phase 2. `db/*.sql` → `sqlc` produces `api/internal/db/gen`. `proto/` → `buf generate` produces Go stubs and Python stubs in one run. <!-- RT#15 -->

## Related files
Create: every path in the tree above (skeletons), plus `deploy/docker/{toolbox,api,worker,web}.Dockerfile`, `.github/workflows/ci.yml`, `.gitignore` (updated to cover `.env*`, `secrets/`, `node_modules`, `.venv`), `.env.example`, `secrets/.gitkeep`, and `README.md` (dev quick start).
Modify: none (the repo has only `docs/` and `plans/`). Host: none — `.wslconfig` is already set; this phase only verifies it. <!-- RT#4 -->

## Implementation steps
0. **Host verify (gate, 10 min):** run `wsl --shutdown` (user confirms first, it stops Docker), restart Docker Desktop, then `docker info --format '{{.MemTotal}} {{.NCPU}}'` must show ≈20GB and 12 CPUs. Do not edit `.wslconfig` again. If the values differ, stop and report to the user. <!-- RT#4 -->
1. Upgrade host Node to 22 LTS and record it in `.nvmrc`.
2. Write `deploy/docker/toolbox.Dockerfile`: golang (latest stable, pinned digest) + Node 22 + uv + make + buf. `go tool` resolves the pinned tools. Mount the repo at `/src`, and cache GOMODCACHE and npm in named volumes.
3. Scaffold `api/` with chi, slog JSON logger, graceful shutdown (SIGTERM, 25s drain), and the `/healthz` (liveness) and `/readyz` (db + s3 ping) endpoints.
4. Write `openapi/root.yaml` with the `health` path and a problem+json (RFC 9457) error schema, then wire the redocly bundle, the oapi-codegen strict chi server and hey-api in `make gen`.
5. Add a sqlc config with pgx/v5, one placeholder query against `goose_db_version`, and goose embedded in the `loomtale migrate` CLI.
6. Add a proto skeleton (`worker/v1/health.proto`) and `buf.gen.yaml` generating Go and Python. Add a **cross-language round-trip test**: a Python test server built from the generated stubs answers a Go client test (and the reverse), run in `make test-integration`. <!-- RT#15 -->
7. Scaffold `workers-python/`: uv, Python 3.12, ruff, pytest, grpcio, and a health server using the generated stub.
8. Scaffold `web/`: Vite 7, React 19, TS strict, ESLint (with `react/no-danger`), Vitest, size-limit, and a page calling generated `getHealth`.
9. Write `deploy/compose.yml` (core) with healthchecks, a private network, only web:8080 and minio:9000 published on 127.0.0.1, `restart: unless-stopped`, and the `mem_limit` values from the budget table. `compose.gpu.yml` adds `deploy.resources.reservations.devices` (nvidia) to the worker. <!-- RT#4 -->
10. Write the Makefile targets `gen gen-check lint test test-integration vuln audit build up down logs migrate ci`.
11. Write `.github/workflows/ci.yml` with the jobs `go` (golangci-lint, `go test -race` with postgres+minio services, govulncheck), `web` (tsc, eslint, vitest, build, size-limit, `npm audit --audit-level=high`), `python` (ruff, pytest, `pip-audit`), `gen-check`, `gitleaks`, and `docker` (build all images, assert `USER` is not root, and assert every compose service has `mem_limit`). Use concurrency cancel-in-progress. <!-- RT#4 -->

## Todo checklist
- [ ] `docker info` shows ≈20GB / 12 CPU (no `.wslconfig` edit)
- [ ] Node 22 on host, `.nvmrc`
- [ ] toolbox image + `scripts/tb.*`
- [ ] api skeleton + health endpoints
- [ ] OpenAPI bundle → Go + TS codegen
- [ ] sqlc + goose wiring
- [ ] proto → Go + Python via buf + round-trip test
- [ ] Python worker skeleton
- [ ] web skeleton calling generated client
- [ ] compose core + gpu override + mem_limit table
- [ ] Makefile + CI green

## Performance budget checks
- size-limit is wired from day one: initial JS ≤200KB gzip (the CI job fails over budget), and the empty shell is expected to stay under 90KB.
- `/healthz` p95 must stay <5ms locally, and `/readyz` must stay <50ms.
- The toolbox caches modules. A second `make gen` should take <30s, and CI with caches should take <10 min.
- `docker stats` during `compose up` idle: core services total ≤3GB RSS.

## Security checklist
- gitleaks runs in CI, and a pre-commit sample hook is documented. `.env*` and `secrets/` are gitignored; `.env.example` holds placeholders only.
- The govulncheck, npm audit and pip-audit gates fail the build at high severity or above.
- Containers run non-root with `read_only: true` where feasible, `cap_drop: [ALL]`, `no-new-privileges`. Postgres and API ports are not published; only `127.0.0.1:8080` (web) and `127.0.0.1:9000` (MinIO, presigned media) are.
- No service mounts `/var/run/docker.sock` (CI asserts it on the rendered compose config). <!-- RT#13 -->
- MinIO root credentials come from compose `secrets:` files. The app uses a scoped access key created by minio-init that is limited to the app bucket.
- Pin base-image digests and GitHub Actions to commit SHAs.

## Reuse points
- Create: `api/internal/httpx` (JSON and problem+json helpers, request ID), `api/internal/obs` (slog setup with a redaction hook added in phase 2), and the per-package env loading convention.
- One toolbox image serves local dev, CI parity and codegen. One OpenAPI bundle serves the Go server, the Go validator and the TS client with Zod. One `buf generate` serves Go and Python.

## Tests
- `scripts/tb.ps1 ci`, which runs lint, unit tests, gen-check, vuln and audit.
- `docker compose -f deploy/compose.yml up -d --wait && curl -fsS http://127.0.0.1:8080/api/v1/healthz`
- `cd web && npm run test && npm run build && npx size-limit`
- `cd workers-python && uv run pytest -q`
- `scripts/tb.ps1 test-integration -run TestProtoRoundTrip`

## Success criteria
- `docker info` reports ≈20GB memory and 12 CPUs.
- On a clean clone with only Docker, Node 22 and git: `scripts/tb.ps1 ci` passes and `compose up --wait` makes all services healthy.
- The web page shows the API version through the generated client, with no hand-written types.
- The Go ↔ Python proto round-trip test passes.
- CI on the PR is green, and a deliberately planted fake secret is caught by gitleaks (verified once, then removed).

## Risks + rollback
- `.wslconfig` not applied because Docker was not restarted (Medium×High): step 0 is a gate. Host rollback is restoring `C:/Users/ADMIN/.wslconfig.bak-260924` and `wsl --shutdown` (user action).
- Codegen tool version drift (Medium×Medium) is mitigated by `go.mod` tool pins, `buf.lock`, a `package-lock.json`, `uv.lock` and gen-check.
- Windows bind-mount performance for the toolbox (Medium×Low) is mitigated by named volumes for caches and by recommending a WSL2 checkout if builds are slow.
- Rollback: this phase is purely additive, so revert the PR.

## Next steps
Phase 1b (Blackwell smoke spike) and phase 2 (schema, auth, security foundation) run in parallel.
