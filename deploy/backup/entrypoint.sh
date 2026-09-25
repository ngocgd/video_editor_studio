#!/bin/bash
# Installs the nightly schedule into crond and keeps the container running.
# `docker compose run --rm backup /backup.sh` (used for the manual test run
# and by `make test-integration`-style verification) bypasses this
# entirely and runs one backup immediately.
set -euo pipefail

# The compose tmpfs mount at /var/spool/cron shadows whatever the image
# built there, so the crontabs subdirectory must be recreated here rather
# than only at image build time.
mkdir -p /var/spool/cron/crontabs
echo "${BACKUP_SCHEDULE:-0 3 * * *} /backup.sh >> /proc/1/fd/1 2>&1" > /var/spool/cron/crontabs/root

echo "backup sidecar started; schedule: ${BACKUP_SCHEDULE:-0 3 * * *} (TZ=${TZ:-UTC})"
# `-f` (foreground) makes dcron call setpgid on itself, which fails with
# EPERM as PID 1 under this container's capability/no-new-privileges
# restrictions. Daemonizing normally and keeping the container alive with
# a trivial foreground process avoids that path entirely.
crond -l 2
exec tail -f /dev/null
