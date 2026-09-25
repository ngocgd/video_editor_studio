# Nightly backup sidecar: pg_dump + age (client-side encryption) + mc
# (S3-compatible upload to the cloud backup bucket), driven by busybox
# crond so the container just stays up and cron fires the schedule.
FROM bitnamilegacy/minio-client@sha256:00dcc4e58ada0df45bb7d9ee435af98295f96c27c3c68292ce78ec700a87b511 AS mc-src

FROM alpine@sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507

# postgresql17-client to match deploy/compose.yml's postgres:17 server:
# pg_dump refuses to run against a server newer than itself.
RUN apk add --no-cache postgresql17-client age bash coreutils tzdata dcron \
    && mkdir -p /var/spool/cron/crontabs

# MinIO client, from the same pinned image deploy/minio-init.sh already
# uses, for S3-compatible upload to the backup bucket (Cloudflare R2 /
# Backblaze B2) and for pulling the object bucket to stage for encryption.
COPY --from=mc-src /opt/bitnami/minio-client/bin/mc /usr/bin/mc

COPY deploy/backup/backup.sh /backup.sh
COPY deploy/backup/entrypoint.sh /entrypoint.sh
RUN chmod +x /backup.sh /entrypoint.sh

# Runs as root: dcron and reading the mounted secrets both need it, and
# this image only ever runs as a local sidecar, never receiving traffic.
ENTRYPOINT ["/entrypoint.sh"]
