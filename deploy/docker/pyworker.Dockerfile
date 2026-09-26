# The Python gRPC worker. Without build arguments the image is small and
# framework-free: every engine is listed and answers an honest
# engine_not_installed. PYWORKER_EXTRAS adds engine runtimes from the
# locked extras (space-separated: "tts-en tts-vi align vision"); tts-en, align
# and vision bring the CUDA 12.8 PyTorch wheels (several GB), so a GPU
# deployment adds all four and a CI or laptop image goes without.
FROM python@sha256:2f17fc044b579bab302c2e8054d3a686e2cb9a83de48e70534b94cd8ebbe06a9 AS build
ARG PYWORKER_EXTRAS=""
WORKDIR /src
ENV UV_LINK_MODE=copy \
    UV_PYTHON_DOWNLOADS=never \
    UV_COMPILE_BYTECODE=1
COPY --from=ghcr.io/astral-sh/uv@sha256:88d7b48fc9f17462c82b5482e497af250d337f3f14e1ac97c16e68eba49b651e /uv /usr/local/bin/uv
COPY workers-python/pyproject.toml workers-python/uv.lock workers-python/.python-version ./
RUN uv sync --frozen --no-install-project --no-dev $(for e in $PYWORKER_EXTRAS; do printf -- '--extra %s ' "$e"; done)
COPY workers-python/src ./src
# --no-editable: the default editable install writes a .pth file
# pointing back at this build stage's /src, which does not exist in the
# runtime stage below (only .venv and src are copied forward).
RUN uv sync --frozen --no-dev --no-editable $(for e in $PYWORKER_EXTRAS; do printf -- '--extra %s ' "$e"; done)

FROM python@sha256:2f17fc044b579bab302c2e8054d3a686e2cb9a83de48e70534b94cd8ebbe06a9
WORKDIR /app
# CTranslate2 (faster-whisper) finds cuBLAS and cuDNN in the NVIDIA wheels
# PyTorch installs; the paths do not exist in an image without extras.
# HF_HUB_CACHE is where engines link offline cache views of pinned files.
ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    HF_HUB_CACHE=/tmp/hf-cache \
    MODELS_DIR=/models \
    LD_LIBRARY_PATH=/app/.venv/lib/python3.12/site-packages/nvidia/cublas/lib:/app/.venv/lib/python3.12/site-packages/nvidia/cudnn/lib
COPY --from=build /src/.venv /app/.venv
COPY --from=build /src/src /app/src
RUN useradd -u 10001 -m -s /usr/sbin/nologin pyworker
USER pyworker:pyworker
EXPOSE 9090
ENTRYPOINT ["python", "-m", "loomtale_worker.server"]
