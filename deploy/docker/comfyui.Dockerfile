# ComfyUI image engine (Blackwell sm_120, CUDA 12.8, PyTorch cu128).
#
# Derived from the phase 1b spike's verified pins and hardened:
# - CUDA base image pinned by digest;
# - ComfyUI and every custom node pinned by commit (only ComfyUI-GGUF,
#   reviewed: pure Python, no build step, one dependency `gguf`);
# - every Python package pinned through deploy/comfyui/constraints.txt;
# - two stages, so the runtime image has no git, pip cache or compiler;
# - runs as a non-root user with offline Hugging Face env, a read-only
#   root filesystem (see compose.gpu.yml) and all writes going to /scratch.
FROM nvidia/cuda@sha256:17e2934e1fa96152b14f78078bfbafd0f00f391df995dc6c641a720fce1202bb AS build

ARG COMFYUI_COMMIT=78368eafee727c52efd121b775e0783c195e5c94
ARG GGUF_NODE_COMMIT=6ea2651e7df66d7585f6ffee804b20e92fb38b8a

ENV DEBIAN_FRONTEND=noninteractive \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_DEFAULT_TIMEOUT=120 \
    PIP_RETRIES=10

RUN apt-get update && apt-get install -y --no-install-recommends \
        python3.10 python3.10-venv git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN python3.10 -m venv /opt/venv
ENV PATH=/opt/venv/bin:$PATH

COPY deploy/comfyui/constraints.txt /tmp/constraints.txt

# PyTorch cu128 wheels from the official index (they ship sm_120
# kernels); the constraints file pins the exact versions.
RUN pip install -c /tmp/constraints.txt torch torchvision \
        --index-url https://download.pytorch.org/whl/cu128

RUN git clone --no-checkout https://github.com/comfyanonymous/ComfyUI.git /app/ComfyUI \
    && cd /app/ComfyUI && git checkout ${COMFYUI_COMMIT} \
    && pip install -c /tmp/constraints.txt -r requirements.txt \
    && rm -rf .git

RUN git clone --no-checkout https://github.com/city96/ComfyUI-GGUF.git /app/ComfyUI/custom_nodes/ComfyUI-GGUF \
    && cd /app/ComfyUI/custom_nodes/ComfyUI-GGUF && git checkout ${GGUF_NODE_COMMIT} \
    && pip install -c /tmp/constraints.txt -r requirements.txt \
    && rm -rf .git

# Byte-compile once at build time: the runtime root filesystem is
# read-only, so Python could not write __pycache__ later.
RUN python -m compileall -q /app/ComfyUI /opt/venv > /dev/null || true

FROM nvidia/cuda@sha256:17e2934e1fa96152b14f78078bfbafd0f00f391df995dc6c641a720fce1202bb

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends python3.10 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /opt/venv /opt/venv
COPY --from=build /app/ComfyUI /app/ComfyUI

RUN useradd -u 10001 -M -d /scratch -s /usr/sbin/nologin comfy \
    && mkdir -p /scratch && chown comfy:comfy /scratch

ENV PATH=/opt/venv/bin:$PATH \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    HF_HOME=/scratch/hf \
    HOME=/scratch

WORKDIR /app/ComfyUI
USER comfy:comfy
EXPOSE 8188

# --reserve-vram keeps the render reserve (1 GB) free for NVENC work; a
# workflow that needs more VRAM offloads weights to CPU RAM instead.
# --disable-api-nodes removes the nodes that call paid external APIs.
# Models are read from the read-only /app/ComfyUI/models mount; inputs,
# outputs, temp files and ComfyUI's own user database go to /scratch.
# ComfyUI refuses to start unless --user-directory already exists, and
# the scratch volume may be fresh, so the entrypoint creates the four
# directories before handing over to main.py.
ENTRYPOINT ["sh", "-c", "mkdir -p /scratch/input /scratch/output /scratch/temp /scratch/user && exec python main.py \"$@\"", "comfyui"]
CMD ["--listen", "0.0.0.0", "--port", "8188", \
     "--reserve-vram", "1", \
     "--disable-api-nodes", "--disable-auto-launch", \
     "--input-directory", "/scratch/input", \
     "--output-directory", "/scratch/output", \
     "--temp-directory", "/scratch/temp", \
     "--user-directory", "/scratch/user"]
