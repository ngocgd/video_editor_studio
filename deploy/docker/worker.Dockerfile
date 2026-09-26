# Multi-stage build for the Go worker process (pipeline orchestration lands
# in phase 3; phase 1 ships a minimal binary that exits cleanly so the
# compose stack and CI stay green end to end).
FROM golang@sha256:bdca99a00bc16590cb1a0bb4e698f5fc5d6a64e4d5eef13d9f18a0ee08e5fa65 AS build
WORKDIR /src
COPY api/go.mod api/go.sum* ./
RUN go mod download
COPY api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# The subtitle font libass burns with (render.FontsDir), pinned to a
# google/fonts commit and verified by checksum. Literata covers Latin and
# Vietnamese; OFL.txt is its licence.
FROM golang@sha256:bdca99a00bc16590cb1a0bb4e698f5fc5d6a64e4d5eef13d9f18a0ee08e5fa65 AS fonts
ARG FONTS_COMMIT=4e5f06dbb274a27ebe71ed54ea706b3ee40eabd9
RUN mkdir -p /fonts /scratch \
 && curl -fsSL -o /fonts/Literata.ttf "https://raw.githubusercontent.com/google/fonts/${FONTS_COMMIT}/ofl/literata/Literata%5Bopsz,wght%5D.ttf" \
 && curl -fsSL -o /fonts/OFL.txt "https://raw.githubusercontent.com/google/fonts/${FONTS_COMMIT}/ofl/literata/OFL.txt" \
 && printf '%s  %s\n' \
      b41138c9373112f32abb589cc22e8674b06ed4048b0c513be922bdd26f274440 /fonts/Literata.ttf \
      8742963604cd89dc81437811a850018fc03b2bfad686d7422c8235967c87614e /fonts/OFL.txt \
    | sha256sum -c -

# A fully static ffmpeg 7.1.1 build (libx264, libass, libwebp, libaom AV1
# for AVIF) and its ffprobe, pinned by digest; being static, it runs on
# distroless. The media steps and the render segments, loudness and QC
# use it; when this build has no NVENC, the render encoder probe falls
# back to libx264.
FROM mwader/static-ffmpeg@sha256:11a44711684c0b9f754c047dcd64235b8b52deab251bd0e0a86f22faa160749c AS ffmpeg

FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/worker /usr/local/bin/worker
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg /ffprobe /usr/local/bin/ffprobe
COPY --from=fonts /fonts /usr/share/fonts/loomtale
# The render scratch volume's mount point, owned by nonroot so a fresh
# named volume inherits a writable owner.
COPY --from=fonts --chown=65532:65532 /scratch /scratch
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/worker"]
