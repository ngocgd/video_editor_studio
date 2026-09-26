#!/usr/bin/env bash
# Runs an image benchmark suite (`loomtale bench`) against a running GPU
# stack, with the host-side ComfyUI RSS sampler the harness needs: no
# container can call `docker stats` (none mounts docker.sock), so this
# script polls it and hands the samples to the CLI through a bind mount.
#
# Usage: PROJECT=loomtale-dev scripts/bench-image.sh <image|image-smoke|image-gate> [out-dir]
# The stack must be up with deploy/compose.gpu.yml. Output images, the RSS
# samples and the CLI log land in out-dir (default ./bench-out/<suite>).
set -euo pipefail
export MSYS_NO_PATHCONV=1

cd "$(dirname "${BASH_SOURCE[0]}")/.."

suite="${1:?usage: PROJECT=<compose project> scripts/bench-image.sh <suite> [out-dir]}"
project="${PROJECT:?set PROJECT to the compose project name}"
out="${2:-./bench-out/$suite}"
compose=(docker compose -p "$project" -f deploy/compose.yml -f deploy/compose.gpu.yml --env-file .env)

mkdir -p "$out/images"
out_abs="$(cd "$out" && pwd -W 2>/dev/null || pwd)"
: > "$out/rss.tsv"

container="$("${compose[@]}" ps -q comfyui)"
if [ -z "$container" ]; then
  echo "comfyui is not running in project $project" >&2
  exit 1
fi

# docker stats prints e.g. "5.12GiB / 10GiB"; convert the usage to bytes.
to_bytes() {
  awk '{
    v = $1; u = v; sub(/[0-9.]+/, "", u); sub(/[A-Za-z]+/, "", v);
    m = 1;
    if (u == "KiB") m = 1024; else if (u == "MiB") m = 1048576; else if (u == "GiB") m = 1073741824;
    else if (u == "kB") m = 1000; else if (u == "MB") m = 1000000; else if (u == "GB") m = 1000000000;
    printf "%.0f\n", v * m
  }'
}

sample() {
  while true; do
    usage="$(docker stats --no-stream --format '{{.MemUsage}}' "$container" 2>/dev/null | awk '{print $1}')"
    if [ -n "$usage" ]; then
      printf '%s %s\n' "$(date +%s%3N)" "$(printf '%s\n' "$usage" | to_bytes)" >> "$out/rss.tsv"
    fi
    sleep 1
  done
}
sample &
sampler=$!
trap 'kill "$sampler" 2>/dev/null || true' EXIT

"${compose[@]}" --profile cli run --rm --no-deps -T \
  -v "$out_abs:/bench" cli bench --suite "$suite" --out /bench/images --rss-samples /bench/rss.tsv \
  2>&1 | tee "$out/bench.log"
