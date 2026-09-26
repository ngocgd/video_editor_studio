#!/usr/bin/env bash
# Runs `go test ./internal/integration/...` against a running compose stack
# from inside a container attached to that stack's network, mirroring the
# CI `integration` job (.github/workflows/ci.yml) without needing Go on the
# host. Deliberately scoped the same as that job — not
# ./internal/workerpb/..., whose proto-roundtrip test spawns a local Python
# gRPC server process the toolbox container doesn't have set up and which
# CI's integration job doesn't run either; that suite has its own coverage
# under `make test`.
#
# Usage:
#   docker compose -p <project> -f deploy/compose.yml -f deploy/compose.integration.yml \
#       --env-file .env up -d --wait --build
#   PROJECT=<project> scripts/test-integration-toolbox.sh [-run Pattern]
#   (INTEGRATION_TAGS=integration,live adds the live-LLM checks, for a
#   stack started with a real provider such as COMPOSE_PROFILES=claude-cli)
#   docker compose -p <project> -f deploy/compose.yml -f deploy/compose.integration.yml \
#       --env-file .env down -v
#
# <project> must be your own `docker compose -p` project name (not shared
# with another concurrently running stack), and the stack must already be up
# with deploy/compose.integration.yml applied so the api service hands back
# presigned URLs this container can actually follow (see that file).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

PROJECT="${PROJECT:?set PROJECT to the docker compose -p project name the stack is running under}"
NETWORK="${PROJECT}_loomtale_core"

if ! docker network inspect "$NETWORK" >/dev/null 2>&1; then
    echo "network $NETWORK not found; bring the stack up first with:" >&2
    echo "  docker compose -p $PROJECT -f deploy/compose.yml -f deploy/compose.integration.yml --env-file .env up -d --wait --build" >&2
    exit 1
fi

[ -f .env ] || { echo ".env not found; run scripts/dev-secrets-init.sh first" >&2; exit 1; }
set -a
# shellcheck disable=SC1091
. ./.env
set +a

docker build -q -f deploy/docker/toolbox.Dockerfile -t loomtale/toolbox:local . >/dev/null

# Git Bash/MSYS on Windows rewrites leading-slash arguments (container
# paths like /src/api) into host paths before they ever reach docker;
# disabling that rewrite here is a no-op on Linux/macOS.
export MSYS_NO_PATHCONV=1

exec docker run --rm \
    --network "$NETWORK" \
    -v "$(pwd):/src" \
    -w /src/api \
    -e CGO_ENABLED=1 \
    -e CI="${CI:-}" \
    -e LLMCLI_EXPECTED="${LLMCLI_EXPECTED:-}" \
    -e API_BASE_URL="http://web:8080/api/v1" \
    -e OWNER_DATABASE_URL="postgres://loomtale_owner:${POSTGRES_OWNER_PASSWORD:?set in .env}@postgres:5432/loomtale?sslmode=disable" \
    -e DATABASE_URL="postgres://loomtale_app:${POSTGRES_APP_PASSWORD:?set in .env}@postgres:5432/loomtale?sslmode=disable" \
    -e BACKUP_DATABASE_URL="postgres://loomtale_backup:${POSTGRES_BACKUP_PASSWORD:?set in .env}@postgres:5432/loomtale?sslmode=disable" \
    -e MINIO_ENDPOINT="minio:9000" \
    -e MINIO_BUCKET="loomtale" \
    -e MINIO_APP_ACCESS_KEY="${MINIO_APP_ACCESS_KEY:?set in .env}" \
    -e MINIO_APP_SECRET_KEY="${MINIO_APP_SECRET_KEY:?set in .env}" \
    --entrypoint go \
    loomtale/toolbox:local \
    test -tags="${INTEGRATION_TAGS:-integration}" ./internal/integration/... -race -count=1 -v "$@"
