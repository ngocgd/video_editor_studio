#!/usr/bin/env bash
# Asserts the ComfyUI container's isolation against a running GPU stack:
# it cannot resolve or reach huggingface.co (or anything else outside
# loomtale_gpu), publishes no host port, runs as non-root with a
# read-only root filesystem and the models volume read-only, and has no
# docker.sock.
#
# Usage: PROJECT=loomtale-dev scripts/test-comfyui-isolation.sh
set -euo pipefail
export MSYS_NO_PATHCONV=1

cd "$(dirname "${BASH_SOURCE[0]}")/.."
project="${PROJECT:?set PROJECT to the compose project name}"
compose=(docker compose -p "$project" -f deploy/compose.yml -f deploy/compose.gpu.yml --env-file .env)

fail=0
check() {
  local name="$1"; shift
  if "$@"; then
    echo "ok   - $name"
  else
    echo "FAIL - $name"
    fail=1
  fi
}

in_comfyui() { "${compose[@]}" exec -T comfyui python -c "$1"; }

no_dns() {
  in_comfyui "
import socket, sys
try:
    socket.getaddrinfo('huggingface.co', 443)
except OSError:
    sys.exit(0)
sys.exit(1)"
}

no_connect() {
  # A literal public IP skips DNS: the internal network must still refuse.
  in_comfyui "
import socket, sys
try:
    socket.create_connection(('1.1.1.1', 443), timeout=5).close()
except OSError:
    sys.exit(0)
sys.exit(1)"
}

no_published_ports() {
  [ -z "$(docker port "$("${compose[@]}" ps -q comfyui)")" ]
}

non_root() {
  [ "$(in_comfyui 'import os; print(os.getuid())' | tr -d '\r')" != "0" ]
}

read_only_rootfs() {
  in_comfyui "
import sys
try:
    open('/app/ComfyUI/probe', 'w')
except OSError:
    sys.exit(0)
sys.exit(1)"
}

read_only_models() {
  in_comfyui "
import sys
try:
    open('/app/ComfyUI/models/probe', 'w')
except OSError:
    sys.exit(0)
sys.exit(1)"
}

no_docker_sock() {
  in_comfyui "import os, sys; sys.exit(1 if os.path.exists('/var/run/docker.sock') else 0)"
}

offline_env() {
  in_comfyui "import os, sys; sys.exit(0 if os.environ.get('HF_HUB_OFFLINE') == '1' and os.environ.get('TRANSFORMERS_OFFLINE') == '1' else 1)"
}

check "comfyui cannot resolve huggingface.co" no_dns
check "comfyui cannot open a TCP connection to a public IP" no_connect
check "comfyui publishes no host port" no_published_ports
check "comfyui runs as a non-root user" non_root
check "comfyui root filesystem is read-only" read_only_rootfs
check "comfyui models mount is read-only" read_only_models
check "comfyui has no docker.sock" no_docker_sock
check "comfyui runs with HF_HUB_OFFLINE=1 and TRANSFORMERS_OFFLINE=1" offline_env

exit "$fail"
