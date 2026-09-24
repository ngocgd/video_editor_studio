# Single container for Go, Node, uv, buf and make so the host needs only
# Docker, Node (for fast web HMR) and git. Used for `make gen`, `make lint`,
# `make test`, CI parity, and nothing else — it never runs in production.
FROM golang@sha256:2c4c60ef415fbfa5e90300722293bef36c5e63fae17570ce18f580af933dbd73 AS golang-src
FROM node@sha256:b26b04c123d9ff8ab646ceb18b9d75a1173acf64b9a401094b906d27b29338d4 AS node-src

FROM debian@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git make unzip gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

# Go toolchain, copied from the pinned golang image.
COPY --from=golang-src /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:/root/go/bin:${PATH}
ENV GOTOOLCHAIN=auto

# Node 22, copied from the pinned node image.
COPY --from=node-src /usr/local/bin/node /usr/local/bin/node
COPY --from=node-src /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx

# uv (Python package/project manager), pinned by digest.
COPY --from=ghcr.io/astral-sh/uv@sha256:3adc3706091ce7c2fe595e669628caedd6d951551b92b258b7e7dbe06d9440bc /uv /uvx /usr/local/bin/

# buf CLI, pinned version.
RUN curl -sSL "https://github.com/bufbuild/buf/releases/download/v1.47.2/buf-Linux-x86_64" \
    -o /usr/local/bin/buf && chmod +x /usr/local/bin/buf

# golangci-lint, installed as a standalone binary (not a go.mod `tool`
# dependency: its transitive closure pulls in unrelated tooling and would
# bloat go.sum by orders of magnitude).
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
    | sh -s -- -b /usr/local/bin v2.14.0

# Redocly CLI for OpenAPI bundling, pinned version.
RUN npm install -g @redocly/cli@1.25.11 && npm cache clean --force

WORKDIR /src
VOLUME ["/root/go/pkg/mod", "/root/.cache/go-build", "/root/.npm"]

# Runs as root: this image only ever runs locally or in CI as `docker compose
# run --rm toolbox`, is never published, and must write into bind-mounted
# named volumes across Windows/WSL2/Linux CI without UID mapping friction.
ENTRYPOINT ["make"]
