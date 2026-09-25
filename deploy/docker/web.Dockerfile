# Multi-stage build: compile the SPA with Node, serve the static bundle with
# Caddy running as non-root.
FROM node@sha256:b26b04c123d9ff8ab646ceb18b9d75a1173acf64b9a401094b906d27b29338d4 AS build
WORKDIR /src
COPY web/package.json web/package-lock.json* ./
RUN npm ci
COPY web/ ./
COPY openapi/ /openapi/
RUN npm run build

FROM caddy@sha256:6aeddd44c3078b0f9a35206472a11420648a79c184603ef95957d0a20044cb2b
COPY deploy/caddy/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /src/dist /srv
# The upstream image sets cap_net_bind_service on the caddy binary so it can
# bind privileged ports (80/443) as non-root. We only ever bind 8080, and
# compose runs this container with `no-new-privileges`, which refuses to
# exec a binary carrying a file capability the process doesn't already
# have. Stripping it here keeps the container startable under that flag.
RUN apk add --no-cache libcap \
    && setcap -r /usr/bin/caddy \
    && apk del libcap \
    && addgroup -g 1000 caddyapp && adduser -D -u 1000 -G caddyapp caddyapp \
    && chown -R caddyapp:caddyapp /srv /config /data
USER caddyapp
EXPOSE 8080
