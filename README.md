# Loomtale Studio

Internal story-to-YouTube video studio. See [`docs/tech-stack.md`](docs/tech-stack.md) and the
[implementation plan](plans/260924-2244-loomtale-studio-mvp/plan.md) for background.

## Dev quick start

Prerequisites on the host: Docker, Node 22 (`nvm install 22 && nvm use 22`, or just respect
[`.nvmrc`](.nvmrc)), and git. Go, buf, sqlc and the other Go tools are **not** installed on the
host — they run inside the `toolbox` container.

```bash
# 1. Copy the env template and fill in local values.
cp .env.example .env

# 2. Create the MinIO root credential files consumed by compose secrets (dev-only values).
mkdir -p secrets
echo "loomtale-root" > secrets/minio_root_user.txt
echo "$(openssl rand -hex 16)" > secrets/minio_root_password.txt

# 3. Generate code (OpenAPI -> Go + TS, SQL -> sqlc, proto -> Go + Python) inside the toolbox.
scripts/tb.sh gen        # or: scripts/tb.ps1 gen  on Windows

# 4. Run the full CI suite locally (lint, unit tests, gen-check, vuln, audit).
scripts/tb.sh ci

# 5. Bring up the core stack and check health.
docker compose -f deploy/compose.yml up -d --wait
curl -fsS http://127.0.0.1:8080/api/v1/healthz

# 6. Frontend dev server (fast HMR, runs on the host, not in the toolbox).
cd web && npm ci && npm run dev
```

After the first `up`, read the `minio-init` container logs once for the generated
`MINIO_APP_ACCESS_KEY` / `MINIO_APP_SECRET_KEY` pair and put them in `.env` (not committed).

### Integration tests without host Go

`api/internal/integration` (build tag `integration`) drives a real running stack over HTTP and
Postgres. CI runs it directly on the runner (`.github/workflows/ci.yml`'s `integration` job).
Locally, run it from inside a container on the stack's own network instead — no Go on the host
needed, and it uses your own compose project name so it never collides with another stack:

```bash
docker compose -p loomtale-dev -f deploy/compose.yml -f deploy/compose.integration.yml \
    --env-file .env up -d --wait --build
PROJECT=loomtale-dev scripts/test-integration-toolbox.sh
docker compose -p loomtale-dev -f deploy/compose.yml -f deploy/compose.integration.yml \
    --env-file .env down -v
```

`deploy/compose.integration.yml` is a local-only override (never used in production or CI) that
points the api service's presigned-URL MinIO endpoint at MinIO's container hostname, since the
test-runner container can't reach the host's published `127.0.0.1:9000`.

## Repository layout

```
api/            Go module (chi API, worker, loomtale CLI, generated sqlc/oapi-codegen/proto code)
db/             goose migrations and sqlc queries (source of truth; api/internal/db mirrors are generated)
openapi/        OpenAPI source split by domain, bundled to openapi.gen.yaml
proto/          gRPC contracts (buf generates both Go and Python stubs from one run)
workers-python/ uv-managed Python worker package
web/            Vite + React + TS SPA, generated API client in src/api/gen
deploy/         compose files, Dockerfiles, Caddy config
scripts/        tb.sh / tb.ps1 toolbox wrappers
```

## Toolchain

One `toolbox` image (Go, Node 22, uv, buf, sqlc, goose, oapi-codegen, golangci-lint,
govulncheck, air, make) runs every Go-side command: `scripts/tb.sh <make-target>`. Generated
code is committed; `make gen-check` (inside the toolbox) fails CI on drift.
