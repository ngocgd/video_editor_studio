#!/usr/bin/env bash
# Runs a voice, LLM, vision or LoRA training benchmark suite (`loomtale bench`) against a running
# GPU stack and reports the Python worker's peak RSS next to it: no
# container can call `docker stats` (none mounts docker.sock), so this
# script samples it on the host while the suite runs.
#
# Usage: PROJECT=loomtale-dev scripts/bench-voice.sh <voice-smoke|tts|align|llm|vision|train|train-smoke> [out-dir] [ollama-model]
# The stack must be up with deploy/compose.gpu.yml. Audio, cue files, LLM
# outputs with their ratings sheet, depth maps, LoRA weights, the RSS
# samples and the CLI log land in out-dir (default ./bench-out/<suite>).
# ollama-model defaults to OLLAMA_MODEL from .env. The vision, train and
# train-smoke suites read reference images (PNG/JPEG/WebP; train-smoke
# needs 4, or 5 to score a held-out one) from REFS, default <out-dir>/refs,
# mounted read-only.
set -euo pipefail
export MSYS_NO_PATHCONV=1

cd "$(dirname "${BASH_SOURCE[0]}")/.."

suite="${1:?usage: PROJECT=<compose project> scripts/bench-voice.sh <suite> [out-dir] [ollama-model]}"
project="${PROJECT:?set PROJECT to the compose project name}"
out="${2:-./bench-out/$suite}"
compose=(docker compose -p "$project" -f deploy/compose.yml -f deploy/compose.gpu.yml --env-file .env)
model_args=()
if [ -n "${3:-}" ]; then
  model_args=(--ollama-model "$3")
fi
ref_args=()
case "$suite" in
  vision|train|train-smoke)
    refs="${REFS:-$out/refs}"
    if [ ! -d "$refs" ]; then
      echo "reference image directory $refs does not exist (set REFS)" >&2
      exit 1
    fi
    refs_abs="$(cd "$refs" && pwd -W 2>/dev/null || pwd)"
    ref_args=(-v "$refs_abs:/refs:ro")
    model_args+=(--refs /refs)
    ;;
esac

mkdir -p "$out"
out_abs="$(cd "$out" && pwd -W 2>/dev/null || pwd)"
: > "$out/pyworker-rss.txt"

container="$("${compose[@]}" ps -q pyworker)"
if [ -z "$container" ]; then
  echo "pyworker is not running in project $project" >&2
  exit 1
fi

sample() {
  while true; do
    docker stats --no-stream --format '{{.MemUsage}}' "$container" 2>/dev/null | awk '{print $1}' >> "$out/pyworker-rss.txt"
    sleep 1
  done
}
sample &
sampler=$!
trap 'kill "$sampler" 2>/dev/null || true' EXIT

status=0
"${compose[@]}" --profile cli run --rm --no-deps -T \
  -v "$out_abs:/bench" "${ref_args[@]}" cli bench --suite "$suite" --out /bench "${model_args[@]}" \
  2>&1 | tee "$out/bench.log" || status=$?

kill "$sampler" 2>/dev/null || true
peak="$(sort -h "$out/pyworker-rss.txt" | tail -1)"
echo "pyworker RSS peak: ${peak:-not sampled} (mem_limit 8g)" | tee -a "$out/bench.log"
exit "$status"
