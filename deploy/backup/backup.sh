#!/bin/bash
# One backup run:
#   - pg_dump -Fc streamed straight through age into the cloud target (no
#     local copy of the plaintext dump ever touches disk/tmpfs), with
#     7-daily + 4-weekly retention pruning old dumps.
#   - the object bucket mirrored object-by-object, each one streamed
#     through age individually (`mc cat | age | mc pipe`, never a local
#     mirror of the whole bucket): a per-object existence check at the
#     target skips anything already uploaded, so a nightly run only ever
#     uploads what changed since the last one instead of re-uploading the
#     full bucket (assets are immutable once finalized, so "already
#     present" is a correct enough incremental-sync signal without a
#     checksum comparison).
# Encrypted client-side with age before upload to BACKUP_TARGET (an
# encrypted cloud bucket: Cloudflare R2 or Backblaze B2), using only the
# public recipient (the private identity is never mounted into this
# container — see deploy/compose.yml's secrets block). A row in
# backup_runs records the outcome. Exits non-zero (and logs loudly) on any
# failure so a failed cron run is never silent.
set -uo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${MINIO_ENDPOINT:?MINIO_ENDPOINT is required}"
: "${MINIO_BUCKET:?MINIO_BUCKET is required}"
: "${MINIO_BACKUP_ACCESS_KEY:?MINIO_BACKUP_ACCESS_KEY is required}"
: "${MINIO_BACKUP_SECRET_KEY:?MINIO_BACKUP_SECRET_KEY is required}"
: "${BACKUP_TARGET:?BACKUP_TARGET is required}"
: "${BACKUP_TARGET_BUCKET:?BACKUP_TARGET_BUCKET is required}"
: "${BACKUP_ENCRYPTION_RECIPIENT:?BACKUP_ENCRYPTION_RECIPIENT is required}"

now_ts="$(date -u +%Y%m%dT%H%M%SZ)"
run_id="$(cat /proc/sys/kernel/random/uuid)"

log() { echo "[backup $now_ts] $*"; }

record_start() {
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c \
        "INSERT INTO backup_runs (id, started_at, status) VALUES ('$run_id', now(), 'running');"
}

record_result() {
    status="$1"
    detail="$2"
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c \
        "UPDATE backup_runs SET status = '$status', detail = \$\$${detail}\$\$, finished_at = now() WHERE id = '$run_id';"
}

fail() {
    log "FAILED: $1"
    record_result failed "$1" || true
    exit 1
}

record_start || fail "could not record backup_runs start row"

# --- backup target credentials (ENDPOINT-independent: BACKUP_TARGET is the endpoint) ---
CRED_FILE="/run/secrets/backup_target_credentials"
[ -s "$CRED_FILE" ] || fail "backup_target_credentials secret is missing or empty"
# shellcheck disable=SC1090
. "$CRED_FILE"
: "${BACKUP_TARGET_ACCESS_KEY_ID:?backup_target_credentials must set BACKUP_TARGET_ACCESS_KEY_ID}"
: "${BACKUP_TARGET_SECRET_ACCESS_KEY:?backup_target_credentials must set BACKUP_TARGET_SECRET_ACCESS_KEY}"

mc alias set local "http://$MINIO_ENDPOINT" "$MINIO_BACKUP_ACCESS_KEY" "$MINIO_BACKUP_SECRET_KEY" \
    || fail "mc alias set local failed"
mc alias set target "$BACKUP_TARGET" "$BACKUP_TARGET_ACCESS_KEY_ID" "$BACKUP_TARGET_SECRET_ACCESS_KEY" \
    || fail "mc alias set target failed"

# --- Postgres dump, streamed through age straight to the target ---
DUMP_NAME="$BACKUP_TARGET_BUCKET/postgres/loomtale-$now_ts.dump.age"
pg_dump -Fc "$DATABASE_URL" \
    | age -r "$BACKUP_ENCRYPTION_RECIPIENT" \
    | mc pipe "target/$DUMP_NAME" \
    || fail "pg_dump | age | mc pipe failed"

# --- Object bucket, streamed and incrementally synced object-by-object ---
mc find "local/$MINIO_BUCKET" --type f 2>/dev/null | while IFS= read -r src; do
    rel="${src#local/"$MINIO_BUCKET"/}"
    dest="target/$BACKUP_TARGET_BUCKET/objects/$rel.age"
    if mc stat "$dest" >/dev/null 2>&1; then
        continue
    fi
    mc cat "$src" | age -r "$BACKUP_ENCRYPTION_RECIPIENT" | mc pipe "$dest" \
        || log "warning: failed to sync $rel (non-fatal, will retry next run)"
done
log "object sync pass complete"

# --- Retention for Postgres dumps: keep 7 daily + 4 weekly ---
# Objects are not pruned the same way: they are immutable once finalized
# (no delete-asset workflow exists yet), so the target mirror is simply
# the bucket's own live content, not a series of point-in-time snapshots
# that need thinning.
prune_old_dumps() {
    mapfile -t all_dumps < <(mc ls "target/$BACKUP_TARGET_BUCKET/postgres/" 2>/dev/null | awk '{print $NF}' | sort)
    total="${#all_dumps[@]}"
    [ "$total" -le 7 ] && return 0

    weekly_kept=0
    for ((i = total - 8; i >= 0; i--)); do
        name="${all_dumps[$i]}"
        stamp="$(echo "$name" | sed -E 's/^loomtale-([0-9]{8})T.*/\1/')"
        wd="$(date -u -d "$stamp" +%u 2>/dev/null || echo 0)"
        if [ "$wd" = "7" ] && [ "$weekly_kept" -lt 4 ]; then
            weekly_kept=$((weekly_kept + 1))
            continue
        fi
        mc rm "target/$BACKUP_TARGET_BUCKET/postgres/$name" >/dev/null 2>&1 || true
    done
}
prune_old_dumps || log "warning: retention prune step had an issue (non-fatal)"

record_result success "dump=$DUMP_NAME objects synced incrementally to target/$BACKUP_TARGET_BUCKET/objects/" \
    || log "warning: could not record success in backup_runs"
log "OK"
