# Allowlist-only forward proxy the llm-cli sidecar uses for all egress
# (see deploy/egress-proxy/tinyproxy.conf for the allowlist and why).
FROM alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache tinyproxy \
    && addgroup -g 10003 tinyproxy 2>/dev/null || true \
    && adduser -D -u 10003 -G tinyproxy tinyproxy 2>/dev/null || true
COPY deploy/egress-proxy/tinyproxy.conf /etc/tinyproxy/tinyproxy.conf
COPY deploy/egress-proxy/allowed-hosts.conf /etc/tinyproxy/allowed-hosts.conf
EXPOSE 8888
USER tinyproxy:tinyproxy
ENTRYPOINT ["tinyproxy", "-d", "-c", "/etc/tinyproxy/tinyproxy.conf"]
