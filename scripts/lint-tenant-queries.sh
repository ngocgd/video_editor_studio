#!/usr/bin/env bash
# Fails if any sqlc query against a tenant-scoped table does not filter on
# tenant_id = @tenant_id. Run from anywhere: paths resolve relative to
# this script's own directory.
#
# A query is exempt if the line immediately above its "-- name:" header (or
# anywhere in its own body) carries "-- lint-tenant-queries:allow: <reason>",
# the same reviewable escape hatch used elsewhere in this codebase
# (analyzer allow-comments, nolint). INSERT queries are exempt
# automatically: they write a tenant_id column but have no WHERE clause to
# filter, since there is nothing to read back.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Tables that carry a tenant_id column (kept in sync with db/migrations by
# hand; this check is a safety net, not a schema introspector).
TENANT_TABLES="assets secrets audit_log memberships"

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

fail=0

for file in db/queries/*.sql; do
    csplit -s -z -f "$work_dir/block-" "$file" '/^-- name:/' '{*}'

    for block in "$work_dir"/block-*; do
        [ -f "$block" ] || continue
        name=$(grep -m1 '^-- name:' "$block" | sed -E 's/^-- name: ([A-Za-z0-9_]+).*/\1/')
        [ -n "$name" ] || { rm -f "$block"; continue; }

        touches_tenant_table=0
        for table in $TENANT_TABLES; do
            if grep -qiE "(FROM|INTO|UPDATE)[[:space:]]+$table\b" "$block"; then
                touches_tenant_table=1
                break
            fi
        done
        if [ "$touches_tenant_table" -eq 0 ]; then
            rm -f "$block"
            continue
        fi

        is_insert=0
        grep -qiE '^[[:space:]]*INSERT INTO' "$block" && is_insert=1

        has_filter=0
        grep -qiE 'tenant_id[[:space:]]*=[[:space:]]*@tenant_id' "$block" && has_filter=1

        allowed=0
        grep -qi 'lint-tenant-queries:allow' "$block" && allowed=1

        if [ "$has_filter" -eq 0 ] && [ "$is_insert" -eq 0 ] && [ "$allowed" -eq 0 ]; then
            echo "lint-tenant-queries: $file: query $name touches a tenant table but has no 'tenant_id = @tenant_id' filter" >&2
            fail=1
        fi
        rm -f "$block"
    done
done

if [ "$fail" -ne 0 ]; then
    echo "lint-tenant-queries: FAILED" >&2
    exit 1
fi
echo "lint-tenant-queries: OK"
