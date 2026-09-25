# Multi-stage build: compile the API in the pinned Go image, run it from
# distroless so the runtime image ships no shell and no package manager.
FROM golang@sha256:bdca99a00bc16590cb1a0bb4e698f5fc5d6a64e4d5eef13d9f18a0ee08e5fa65 AS build
WORKDIR /src
COPY api/go.mod api/go.sum* ./
RUN go mod download
COPY api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
# The loomtale operator CLI (migrate, create-owner) ships in the same
# image: the compose `migrate` service runs it, one-shot, before the api
# service starts, so `docker compose up --wait` needs no manual step.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/loomtale ./cmd/loomtale

FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/loomtale /usr/local/bin/loomtale
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
