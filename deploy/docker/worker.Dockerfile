# Multi-stage build for the Go worker process (pipeline orchestration lands
# in phase 3; phase 1 ships a minimal binary that exits cleanly so the
# compose stack and CI stay green end to end).
FROM golang@sha256:bdca99a00bc16590cb1a0bb4e698f5fc5d6a64e4d5eef13d9f18a0ee08e5fa65 AS build
WORKDIR /src
COPY api/go.mod api/go.sum* ./
RUN go mod download
COPY api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# A fully static ffmpeg 7.1.1 build (libwebp, libaom AV1 for AVIF) for the
# media steps, pinned by digest; being static, it runs on distroless.
FROM mwader/static-ffmpeg@sha256:11a44711684c0b9f754c047dcd64235b8b52deab251bd0e0a86f22faa160749c AS ffmpeg

FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/worker /usr/local/bin/worker
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/worker"]
