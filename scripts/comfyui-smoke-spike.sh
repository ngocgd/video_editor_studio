#!/usr/bin/env bash
# Phase 1b go/no-go spike: builds the throwaway ComfyUI image, verifies
# sm_120 (Blackwell) support, submits one Qwen-Image-Edit workflow (and,
# best-effort, one Z-Image Turbo int8 workflow), and records timing/VRAM/RAM.
#
# ComfyUI has no published host port (the compose network is internal-only,
# per the phase's security checklist), so every HTTP call goes through
# `docker exec <container> curl ...` from inside the container's own
# network namespace instead of a host-side curl.
#
# Reads deploy/compose.yml + deploy/compose.gpu.yml; writes results under
# ./spike-out (gitignored scratch dir).
set -euo pipefail

# Git Bash (MSYS) rewrites leading-/ arguments like "/out" into Windows
# paths before they reach `docker run`, which breaks container-side mount
# targets. This is a no-op on native Linux/macOS shells.
export MSYS_NO_PATHCONV=1

cd "$(dirname "${BASH_SOURCE[0]}")/.."

COMPOSE="docker compose -f deploy/compose.yml -f deploy/compose.gpu.yml"
OUT_DIR="./spike-out"
COMFY_URL="http://127.0.0.1:8188"
mkdir -p "$OUT_DIR"

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$1"; }
cexec() { docker exec loomtale-comfyui-1 "$@"; }

cleanup() {
  log "stopping comfyui container"
  $COMPOSE stop comfyui >/dev/null 2>&1 || true
  $COMPOSE rm -f comfyui >/dev/null 2>&1 || true
}
trap cleanup EXIT

log "step 0: host GPU facts"
nvidia-smi --query-gpu=name,memory.used,memory.total,driver_version --format=csv | tee "$OUT_DIR/00-host-nvidia-smi.txt"

log "step 1: build comfyui spike image"
$COMPOSE build comfyui 2>&1 | tee "$OUT_DIR/01-build.log"

log "step 1b: sm_120 / CUDA capability smoke (throwaway container, no compose network)"
docker run --rm --gpus all --entrypoint python3 loomtale/comfyui-spike:local -c "
import torch, time
print('torch', torch.__version__, 'cuda', torch.version.cuda)
assert torch.cuda.is_available(), 'CUDA not available'
cap = torch.cuda.get_device_capability(0)
print('device_capability', cap)
assert cap == (12, 0), f'expected sm_120, got {cap}'
a = torch.randn(4096, 4096, device='cuda')
b = torch.randn(4096, 4096, device='cuda')
torch.cuda.synchronize()
t0 = time.time()
c = a @ b
torch.cuda.synchronize()
print('matmul_ok', c.shape, 'seconds', round(time.time() - t0, 4))
" | tee "$OUT_DIR/02-sm120-smoke.log"

log "step 2: start comfyui service"
$COMPOSE up -d comfyui
log "waiting for /system_stats to answer (via docker exec, no host port is published)"
for i in $(seq 1 60); do
  if cexec curl -fsS "$COMFY_URL/system_stats" >/dev/null 2>&1; then break; fi
  sleep 2
done
cexec curl -fsS "$COMFY_URL/system_stats" | tee "$OUT_DIR/03-system-stats-baseline.json"
echo

log "step 3: generate a synthetic 1024x1024 reference image and stage it in the container's input dir"
docker run --rm -v "$(pwd)/$OUT_DIR:/out" python:3.12-slim bash -c "
pip install --quiet pillow && python3 -c \"
from PIL import Image
img = Image.new('RGB', (1024, 1024))
px = img.load()
for y in range(1024):
    for x in range(1024):
        px[x, y] = (x % 256, y % 256, (x + y) % 256)
img.save('/out/smoke-ref-1024.png')
\"
"
docker cp "$OUT_DIR/smoke-ref-1024.png" loomtale-comfyui-1:/app/ComfyUI/input/smoke-ref-1024.png

log "step 4: submit Qwen-Image-Edit-2509 workflow, poll VRAM every 1s, wait for completion"
printf '{"prompt": %s}' "$(cat comfyui/workflows/qwen-image-edit-2509-smoke.json)" > "$OUT_DIR/qwen-payload.json"
docker cp "$OUT_DIR/qwen-payload.json" loomtale-comfyui-1:/tmp/qwen-payload.json
QWEN_SUBMIT=$(cexec curl -fsS -X POST "$COMFY_URL/prompt" -H 'Content-Type: application/json' --data-binary @/tmp/qwen-payload.json)
echo "$QWEN_SUBMIT" | tee "$OUT_DIR/04-qwen-submit.json"
QWEN_PROMPT_ID=$(echo "$QWEN_SUBMIT" | grep -o '"prompt_id": *"[^"]*"' | head -1 | cut -d'"' -f4)
log "qwen prompt_id=${QWEN_PROMPT_ID:-<none, submit failed, see 04-qwen-submit.json>}"

: > "$OUT_DIR/05-qwen-vram-poll.csv"
echo "epoch_s,vram_free_bytes" >> "$OUT_DIR/05-qwen-vram-poll.csv"
T0=$(date +%s)
(
  while true; do
    STATS=$(cexec curl -fsS "$COMFY_URL/system_stats" 2>/dev/null || echo '{}')
    FREE=$(echo "$STATS" | grep -o '"vram_free": *[0-9]*' | head -1 | grep -o '[0-9]*$')
    echo "$(( $(date +%s) - T0 )),${FREE:-NA}" >> "$OUT_DIR/05-qwen-vram-poll.csv"
    sleep 1
  done
) &
STATS_PID=$!

docker stats --no-stream --format '{{.Name}},{{.MemUsage}}' loomtale-comfyui-1 > "$OUT_DIR/06-qwen-docker-stats-start.txt" || true

if [ -n "${QWEN_PROMPT_ID:-}" ]; then
  DEADLINE=$(( $(date +%s) + 300 ))
  while true; do
    HIST=$(cexec curl -fsS "$COMFY_URL/history/$QWEN_PROMPT_ID" 2>/dev/null || echo '{}')
    if echo "$HIST" | grep -q "\"$QWEN_PROMPT_ID\""; then
      echo "$HIST" > "$OUT_DIR/07-qwen-history.json"
      break
    fi
    if [ "$(date +%s)" -ge "$DEADLINE" ]; then
      log "TIMEOUT waiting for qwen workflow after 300s"
      break
    fi
    sleep 2
  done
fi
T1=$(date +%s)
kill "$STATS_PID" 2>/dev/null || true
wait "$STATS_PID" 2>/dev/null || true

docker stats --no-stream --format '{{.Name}},{{.MemUsage}}' loomtale-comfyui-1 > "$OUT_DIR/08-qwen-docker-stats-end.txt" || true
echo "qwen_seconds,$(( T1 - T0 ))" | tee "$OUT_DIR/09-qwen-timing.txt"

log "copying qwen output image out of the container"
docker cp loomtale-comfyui-1:/app/ComfyUI/output/. "$OUT_DIR/qwen-output/" 2>/dev/null || log "no qwen output dir yet (check 07-qwen-history.json for errors)"

log "step 5 (best-effort): submit Z-Image Turbo int8 workflow"
printf '{"prompt": %s}' "$(cat comfyui/workflows/z-image-turbo-int8-smoke.json)" > "$OUT_DIR/zimage-payload.json"
docker cp "$OUT_DIR/zimage-payload.json" loomtale-comfyui-1:/tmp/zimage-payload.json
ZIMAGE_RESP=$(cexec curl -fsS -X POST "$COMFY_URL/prompt" -H 'Content-Type: application/json' --data-binary @/tmp/zimage-payload.json 2>&1) || true
echo "$ZIMAGE_RESP" | tee "$OUT_DIR/10-zimage-submit.json"
ZIMAGE_PROMPT_ID=$(echo "$ZIMAGE_RESP" | grep -o '"prompt_id": *"[^"]*"' | head -1 | cut -d'"' -f4)
if [ -n "${ZIMAGE_PROMPT_ID:-}" ]; then
  log "z-image prompt_id=$ZIMAGE_PROMPT_ID"
  ZT0=$(date +%s)
  DEADLINE=$(( $(date +%s) + 180 ))
  while true; do
    HIST=$(cexec curl -fsS "$COMFY_URL/history/$ZIMAGE_PROMPT_ID" 2>/dev/null || echo '{}')
    if echo "$HIST" | grep -q "\"$ZIMAGE_PROMPT_ID\""; then
      echo "$HIST" > "$OUT_DIR/11-zimage-history.json"
      break
    fi
    if [ "$(date +%s)" -ge "$DEADLINE" ]; then
      log "TIMEOUT waiting for z-image workflow after 180s"
      break
    fi
    sleep 2
  done
  echo "zimage_seconds,$(( $(date +%s) - ZT0 ))" | tee "$OUT_DIR/12-zimage-timing.txt"
  docker cp loomtale-comfyui-1:/app/ComfyUI/output/. "$OUT_DIR/zimage-output/" 2>/dev/null || true
else
  log "z-image workflow was rejected at submit time (see 10-zimage-submit.json); not supported by this pinned commit or model layout"
fi

log "done. results are in $OUT_DIR"
