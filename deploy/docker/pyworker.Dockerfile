# Phase 4 ships this image with no real engines: the point is a small,
# reproducible runtime that idles cleanly and answers every media RPC
# with an honest engine_not_installed. Phases 9a-9c add heavier ML deps
# to a follow-up image; this Dockerfile deliberately stays framework-free
# (no torch/transformers) since none of that is used until then.
FROM python@sha256:2f17fc044b579bab302c2e8054d3a686e2cb9a83de48e70534b94cd8ebbe06a9 AS build
WORKDIR /src
ENV UV_LINK_MODE=copy \
    UV_PYTHON_DOWNLOADS=never \
    UV_COMPILE_BYTECODE=1
COPY --from=ghcr.io/astral-sh/uv@sha256:88d7b48fc9f17462c82b5482e497af250d337f3f14e1ac97c16e68eba49b651e /uv /usr/local/bin/uv
COPY workers-python/pyproject.toml workers-python/uv.lock workers-python/.python-version ./
RUN uv sync --frozen --no-install-project --no-dev
COPY workers-python/src ./src
# --no-editable: the default editable install writes a .pth file
# pointing back at this build stage's /src, which does not exist in the
# runtime stage below (only .venv and src are copied forward).
RUN uv sync --frozen --no-dev --no-editable

FROM python@sha256:2f17fc044b579bab302c2e8054d3a686e2cb9a83de48e70534b94cd8ebbe06a9
WORKDIR /app
ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1
COPY --from=build /src/.venv /app/.venv
COPY --from=build /src/src /app/src
RUN useradd -u 10001 -m -s /usr/sbin/nologin pyworker
USER pyworker:pyworker
EXPOSE 9090
ENTRYPOINT ["python", "-m", "loomtale_worker.server"]
