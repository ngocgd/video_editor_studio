#!/usr/bin/env bash
# Fails if any sqlc query against a tenant-scoped table does not filter on
# tenant_id = @tenant_id (as an AND/WHERE condition, not merely present
# somewhere in the text — an `OR tenant_id = @tenant_id` clause would match
# textually without actually restricting the result set). Run from
# anywhere: paths resolve relative to this script's own directory.
#
# A query is exempt if its body carries a
# "-- lint-tenant-queries:allow: <reason>" comment line, the same
# reviewable escape hatch used elsewhere in this codebase (analyzer
# allow-comments, nolint). INSERT queries are exempt automatically: they
# write a tenant_id column but have no WHERE clause to filter, since there
# is nothing to read back.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Tables that carry a tenant_id column (kept in sync with db/migrations by
# hand; this check is a safety net, not a schema introspector). `sessions`
# is deliberately excluded: it has no tenant_id column (only
# active_tenant_id, which is not a row-ownership/scoping column — sessions
# are scoped by session id, looked up by token hash).
TENANT_TABLES="assets secrets audit_log memberships tenant_quotas pipeline_runs pipeline_steps pipeline_step_deps series story_bibles episodes episode_drafts episode_draft_revisions imports llm_settings draft_step_applications youtube_channels youtube_oauth_states analytics_tracked_videos video_metrics_daily video_retention channel_metrics_daily analytics_sync_state analytics_reporting_jobs analytics_reporting_reports analytics_suggestions"

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

fail=0

for file in db/queries/*.sql; do
    # awk splits the file into one block per "-- name:" header, keeping any
    # comment lines immediately above a header attached to THAT block
    # (csplit's own split point instead attaches the line directly above
    # the pattern match to the PRECEDING block, which silently exempts the
    # wrong query when an allow-comment sits right above "-- name:").
    awk -v outdir="$work_dir" '
        BEGIN { n = 0; file = "" }
        /^-- name:/ {
            if (file != "") close(file)
            n++
            file = outdir "/block-" n
        }
        { if (file != "") print > file }
    ' "$file"

    for block in "$work_dir"/block-*; do
        [ -f "$block" ] || continue
        name=$(grep -m1 '^-- name:' "$block" | sed -E 's/^-- name: ([A-Za-z0-9_]+).*/\1/')
        [ -n "$name" ] || { rm -f "$block"; continue; }

        # Strip SQL line comments before matching table/filter patterns so
        # a mention inside prose (not the allow-comment marker) can never
        # satisfy the check.
        code_only="$work_dir/code-$name"
        grep -vE '^\s*--' "$block" > "$code_only" || true

        touches_tenant_table=0
        for table in $TENANT_TABLES; do
            if grep -qiE "(FROM|INTO|UPDATE|JOIN)[[:space:]]+$table\b" "$code_only"; then
                touches_tenant_table=1
                break
            fi
        done
        if [ "$touches_tenant_table" -eq 0 ]; then
            rm -f "$block" "$code_only"
            continue
        fi

        is_insert=0
        grep -qiE '^[[:space:]]*INSERT INTO' "$code_only" && is_insert=1

        has_filter=0
        grep -qiE '(WHERE|AND)[[:space:]]+[a-z_.]*tenant_id[[:space:]]*=[[:space:]]*@tenant_id' "$code_only" && has_filter=1

        allowed=0
        grep -qi 'lint-tenant-queries:allow' "$block" && allowed=1

        if [ "$has_filter" -eq 0 ] && [ "$is_insert" -eq 0 ] && [ "$allowed" -eq 0 ]; then
            echo "lint-tenant-queries: $file: query $name touches a tenant table but has no 'tenant_id = @tenant_id' AND/WHERE filter" >&2
            fail=1
        fi
        rm -f "$block" "$code_only"
    done
done

if [ "$fail" -ne 0 ]; then
    echo "lint-tenant-queries: FAILED" >&2
    exit 1
fi
echo "lint-tenant-queries: OK"
