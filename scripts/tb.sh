#!/usr/bin/env bash
# Runs a Makefile target inside the toolbox container so the host never
# needs Go, buf, sqlc, or any other Go tool installed directly.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

exec docker compose -f deploy/compose.tools.yml run --rm toolbox "$@"
