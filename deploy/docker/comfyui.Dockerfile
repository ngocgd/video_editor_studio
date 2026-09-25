# Throwaway spike image (phase 1b go/no-go). Phase 9a re-derives its own
# hardened Dockerfile from these pins; this file is not built by CI.
#
# Base: nvidia/cuda cudnn runtime (Blackwell needs CUDA 12.8+ userspace
# libs; GPU passthrough to this base tag was verified on this host before
# the spike started). PyTorch is installed from the official cu128 wheel
# index, which has shipped sm_120 (Blackwell) kernels since the 2.7 series.
FROM nvidia/cuda@sha256:17e2934e1fa96152b14f78078bfbafd0f00f391df995dc6c641a720fce1202bb

ARG COMFYUI_COMMIT=78368eafee727c52efd121b775e0783c195e5c94
ARG GGUF_NODE_COMMIT=6ea2651e7df66d7585f6ffee804b20e92fb38b8a
ARG TORCH_VERSION=2.9.1
ARG TORCHVISION_VERSION=0.24.1

ENV DEBIAN_FRONTEND=noninteractive \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1

RUN apt-get update && apt-get install -y --no-install-recommends \
        python3.10 python3.10-venv python3-pip git ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && ln -sf /usr/bin/python3.10 /usr/bin/python3 \
    && ln -sf /usr/bin/python3.10 /usr/bin/python

# PyTorch cu128 wheels (bundle their own CUDA runtime shared libs; the
# base image's driver-facing libs are still needed for libcuda.so/nvidia-smi
# passthrough from the host).
RUN pip install --no-cache-dir \
        torch==${TORCH_VERSION}+cu128 torchvision==${TORCHVISION_VERSION}+cu128 \
        --index-url https://download.pytorch.org/whl/cu128

# ComfyUI pinned by commit, plus its own requirements.txt.
RUN git clone --no-checkout https://github.com/comfyanonymous/ComfyUI.git /app/ComfyUI \
    && cd /app/ComfyUI && git checkout ${COMFYUI_COMMIT} \
    && pip install --no-cache-dir -r requirements.txt

# ComfyUI-GGUF custom node, pinned by commit (reviewed: pure-python, no
# build step, single runtime dependency `gguf`; see plans/reports for the
# review notes recorded by this spike).
RUN git clone --no-checkout https://github.com/city96/ComfyUI-GGUF.git \
        /app/ComfyUI/custom_nodes/ComfyUI-GGUF \
    && cd /app/ComfyUI/custom_nodes/ComfyUI-GGUF && git checkout ${GGUF_NODE_COMMIT} \
    && pip install --no-cache-dir -r requirements.txt

# Non-root: ComfyUI only ever writes to /app/ComfyUI/{input,output,temp,user},
# the model tree is bind-mounted read-only.
RUN useradd -u 10001 -m -s /usr/sbin/nologin comfy \
    && mkdir -p /app/ComfyUI/input /app/ComfyUI/output /app/ComfyUI/temp /app/ComfyUI/user \
    && chown -R comfy:comfy /app/ComfyUI

WORKDIR /app/ComfyUI
USER comfy:comfy
EXPOSE 8188

ENTRYPOINT ["python3", "main.py"]
CMD ["--listen", "0.0.0.0", "--port", "8188"]
