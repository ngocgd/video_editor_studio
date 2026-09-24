#!/bin/bash
# Runs once against a healthy MinIO using the root credentials (compose
# secrets), then creates the app bucket and a scoped access key so the API
# and workers never see the root credentials.
set -euo pipefail

ROOT_USER="$(cat /run/secrets/minio_root_user)"
ROOT_PASSWORD="$(cat /run/secrets/minio_root_password)"
BUCKET="${MINIO_BUCKET:-loomtale}"

mc alias set local http://minio:9000 "$ROOT_USER" "$ROOT_PASSWORD"

mc mb --ignore-existing "local/$BUCKET"
mc anonymous set none "local/$BUCKET"

mc admin policy create local loomtale-app-policy - <<POLICY
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

if ! mc admin user info local loomtale-app >/dev/null 2>&1; then
  APP_SECRET="$(head -c 32 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 32)"
  mc admin user add local loomtale-app "$APP_SECRET"
  mc admin policy attach local loomtale-app-policy --user loomtale-app
  echo "created scoped app key loomtale-app (secret only printed once, operator must store it in .env):"
  echo "MINIO_APP_ACCESS_KEY=loomtale-app"
  echo "MINIO_APP_SECRET_KEY=$APP_SECRET"
else
  echo "loomtale-app key already exists, skipping creation"
fi
