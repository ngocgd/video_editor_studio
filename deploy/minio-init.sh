#!/bin/bash
# Runs once against a healthy MinIO using the root credentials (compose
# secrets), then creates the app bucket and a scoped access key from
# operator-provided .env values (never generated/printed at runtime): this
# is what lets `docker compose up --wait` reach a healthy /readyz with
# zero manual steps, instead of an operator having to copy a freshly
# generated key out of the container logs into .env by hand.
set -euo pipefail

ROOT_USER="$(cat /run/secrets/minio_root_user)"
ROOT_PASSWORD="$(cat /run/secrets/minio_root_password)"
: "${APP_ACCESS_KEY:?APP_ACCESS_KEY is required}"
: "${APP_SECRET_KEY:?APP_SECRET_KEY is required}"
: "${BACKUP_ACCESS_KEY:?BACKUP_ACCESS_KEY is required}"
: "${BACKUP_SECRET_KEY:?BACKUP_SECRET_KEY is required}"
BUCKET="${MINIO_BUCKET:-loomtale}"

mc alias set local http://minio:9000 "$ROOT_USER" "$ROOT_PASSWORD"

mc mb --ignore-existing "local/$BUCKET"
mc anonymous set none "local/$BUCKET"

# Versioning lets finalize pin the exact object version it sniffed and
# verified: a client that re-uploads to the same key after finalize (its
# presigned POST is valid up to 10 minutes after issue) creates a new
# version that no asset row ever references, instead of mutating the
# verified one out from under already-issued download URLs.
mc version enable "local/$BUCKET"

# Cross-origin browser uploads/downloads: presigned POST/GET only from the
# SPA's own origin (deploy/compose.yml sets WEB_ORIGIN).
mc admin config set local api cors_allow_origin="${WEB_ORIGIN:-http://127.0.0.1:8080}" || true

POLICY_FILE="$(mktemp)"
cat > "$POLICY_FILE" <<POLICY
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"],
      "Resource": ["arn:aws:s3:::${BUCKET}", "arn:aws:s3:::${BUCKET}/*"]
    }
  ]
}
POLICY
mc admin policy create local loomtale-app-policy "$POLICY_FILE"
rm -f "$POLICY_FILE"

# `mc admin user add` on an existing access key resets its secret key, so
# this stays idempotent and self-healing if the mounted secret ever
# changes (e.g. rotated) without needing a separate update path.
mc admin user add local "$APP_ACCESS_KEY" "$APP_SECRET_KEY"
mc admin policy attach local loomtale-app-policy --user "$APP_ACCESS_KEY" 2>/dev/null || true
echo "app key from MINIO_APP_ACCESS_KEY (.env) is provisioned"

# Read-only key for the backup sidecar: it only ever mirrors the source
# bucket out, never writes to it, so it gets GetObject/ListBucket and
# nothing else — not the app's read-write policy, and not root.
BACKUP_POLICY_FILE="$(mktemp)"
cat > "$BACKUP_POLICY_FILE" <<POLICY
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:ListBucket"],
      "Resource": ["arn:aws:s3:::${BUCKET}", "arn:aws:s3:::${BUCKET}/*"]
    }
  ]
}
POLICY
mc admin policy create local loomtale-backup-policy "$BACKUP_POLICY_FILE"
rm -f "$BACKUP_POLICY_FILE"
mc admin user add local "$BACKUP_ACCESS_KEY" "$BACKUP_SECRET_KEY"
mc admin policy attach local loomtale-backup-policy --user "$BACKUP_ACCESS_KEY" 2>/dev/null || true
echo "read-only backup key from MINIO_BACKUP_ACCESS_KEY (.env) is provisioned"
