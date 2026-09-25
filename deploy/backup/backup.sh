#!/bin/bash
# One backup run: pg_dump -Fc, an mc mirror of the object bucket, both
# encrypted client-side with age before upload to BACKUP_TARGET (an
# encrypted cloud bucket: Cloudflare R2 or Backblaze B2), then a
# retention prune (7 daily, 4 weekly) and a row in backup_runs recording
# the outcome. Exits non-zero (and logs loudly) on any failure so a
# failed cron run is never silent.
set -uo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${MINIO_ENDPOINT:?MINIO_ENDPOINT is required}"
: "${MINIO_BUCKET:?MINIO_BUCKET is required}"
: "${BACKUP_TARGET:?BACKUP_TARGET is required}"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

now_ts="$(date -u +%Y%m%dT%H%M%SZ)"
weekday="$(date -u +%u)" # 1=Monday .. 7=Sunday
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

# --- age identity (generated once, stored in the backup_encryption_key secret) ---
IDENTITY_FILE="/run/secrets/backup_encryption_key"
[ -s "$IDENTITY_FILE" ] || fail "backup_encryption_key secret is missing or empty"
RECIPIENT="$(age-keygen -y "$IDENTITY_FILE" 2>&1)" || fail "could not derive age recipient from identity"

# --- backup target credentials (ENDPOINT/ACCESS_KEY/SECRET_KEY/BUCKET) ---
CRED_FILE="/run/secrets/backup_target_credentials"
[ -s "$CRED_FILE" ] || fail "backup_target_credentials secret is missing or empty"
# shellcheck disable=SC1090
. "$CRED_FILE"
: "${BACKUP_TARGET_ACCESS_KEY_ID:?backup_target_credentials must set BACKUP_TARGET_ACCESS_KEY_ID}"
: "${BACKUP_TARGET_SECRET_ACCESS_KEY:?backup_target_credentials must set BACKUP_TARGET_SECRET_ACCESS_KEY}"

mc alias set local "http://$MINIO_ENDPOINT" "$(cat /run/secrets/minio_root_user)" "$(cat /run/secrets/minio_root_password)" \
    || fail "mc alias set local failed"
mc alias set target "$BACKUP_TARGET" "$BACKUP_TARGET_ACCESS_KEY_ID" "$BACKUP_TARGET_SECRET_ACCESS_KEY" \
    || fail "mc alias set target failed"

# --- Postgres dump ---
DUMP_FILE="$WORKDIR/loomtale-$now_ts.dump"
pg_dump -Fc "$DATABASE_URL" -f "$DUMP_FILE" || fail "pg_dump failed"
age -r "$RECIPIENT" -o "$DUMP_FILE.age" "$DUMP_FILE" || fail "encrypting the dump failed"
mc cp "$DUMP_FILE.age" "target/postgres/loomtale-$now_ts.dump.age" || fail "uploading the encrypted dump failed"

# --- Object bucket mirror, encrypted per-object before upload ---
STAGE_DIR="$WORKDIR/objects"
ENC_DIR="$WORKDIR/objects-enc"
mkdir -p "$STAGE_DIR" "$ENC_DIR"
mc mirror --quiet "local/$MINIO_BUCKET" "$STAGE_DIR" || fail "mirroring the object bucket locally failed"
find "$STAGE_DIR" -type f | while IFS= read -r f; do
    rel="${f#"$STAGE_DIR"/}"
    mkdir -p "$ENC_DIR/$(dirname "$rel")"
    age -r "$RECIPIENT" -o "$ENC_DIR/$rel.age" "$f" || exit 1
done || fail "encrypting the object bucket mirror failed"
mc mirror --quiet "$ENC_DIR" "target/objects/$now_ts" || fail "uploading the encrypted object mirror failed"

# --- Retention: keep 7 daily + 4 weekly dumps under postgres/ ---
# The newest 7 dumps are always kept ("daily" retention). Among anything
# older, a Sunday (weekday 7) snapshot is kept as a "weekly" copy, capped
# at the 4 most recent such Sundays; everything else is pruned. Runs in
# the current shell (not a piped subshell) so weekly_kept actually
# persists across iterations.
prune_old_dumps() {
    mapfile -t all_dumps < <(mc ls target/postgres/ 2>/dev/null | awk '{print $NF}' | sort)
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
        mc rm "target/postgres/$name" >/dev/null 2>&1 || true
    done
}
prune_old_dumps || log "warning: retention prune step had an issue (non-fatal)"

record_result success "dump=$DUMP_FILE.age objects_prefix=objects/$now_ts weekday=$weekday" \
    || log "warning: could not record success in backup_runs"
log "OK"
