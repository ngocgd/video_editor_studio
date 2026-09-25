# Multi-stage build for the Go worker process (pipeline orchestration lands
# in phase 3; phase 1 ships a minimal binary that exits cleanly so the
# compose stack and CI stay green end to end).
FROM golang@sha256:bdca99a00bc16590cb1a0bb4e698f5fc5d6a64e4d5eef13d9f18a0ee08e5fa65 AS build
WORKDIR /src
COPY api/go.mod api/go.sum* ./
RUN go mod download
COPY api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/worker /usr/local/bin/worker
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/worker"]
