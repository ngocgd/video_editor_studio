# The llm-cli sidecar: a tiny Go HTTP shim (api/cmd/llmcli) that spawns a
# pinned Node/claude-code CLI. This container holds only the Anthropic
# OAuth token secret; it never mounts the DB, MinIO, KEK or YouTube
# secrets (see compose.yml's llm-cli service).
FROM golang@sha256:bdca99a00bc16590cb1a0bb4e698f5fc5d6a64e4d5eef13d9f18a0ee08e5fa65 AS build
WORKDIR /src
COPY api/go.mod api/go.sum* ./
RUN go mod download
COPY api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/llmcli ./cmd/llmcli

FROM node@sha256:43ac6c60b8f89723f746e8a92ce91abd5017e627ce1ddfe4238355d3a30b772c
ARG CLAUDE_CODE_VERSION=2.1.282
ENV NPM_CONFIG_UPDATE_NOTIFIER=false \
    NPM_CONFIG_FUND=false \
    NODE_ENV=production
# Pinned version, matched against LLMCLI_PINNED_VERSION at startup by the
# shim's self-check (see api/cmd/llmcli/selfcheck.go).
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
    && npm cache clean --force
COPY --from=build /out/llmcli /usr/local/bin/llmcli

# Non-root, no shell needed at runtime beyond what the claude CLI itself
# execs; HOME/tmp are tmpfs-mounted by compose (read_only rootfs).
RUN groupadd -g 10002 llmcli && useradd -u 10002 -g llmcli -m -s /usr/sbin/nologin llmcli
USER llmcli:llmcli
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/llmcli"]
