# Phase 02: DB schema, auth, RBAC, audit, storage, secrets, backups

## Context links
- [plan.md](plan.md) · [contract §2 constraints, §11 security](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- [backend research §6 SaaS-readiness](../reports/researcher-260924-2128-backend-architecture-goclaw.md)
- Depends on phase 1 (toolbox, codegen, compose). Runs in parallel with phase 1b.

## Overview
- Priority: P1 · Status: pending · Effort: 24h <!-- RT#15 re-estimate -->
- This phase builds the SaaS-ready foundation that every later phase uses: tenant-scoped schema conventions, session auth, CSRF, RBAC, rate limiting, an append-only audit log, envelope-encrypted secrets, two presigners (browser and internal), security headers with a final CSP, spec-driven request validation, log scrubbing, and nightly backups.

## Requirements
- Every business table has `tenant_id uuid not null`, `id uuid` (UUIDv7, generated in Go), `created_at` and `updated_at`. Every sqlc query on a tenant table includes `tenant_id = @tenant_id`, enforced by `scripts/lint-tenant-queries` in CI.
- **Tenant resolution:** the tenant comes only from the session (`sessions.active_tenant_id`), never from a path, query, header or body. `POST /auth/switch-tenant {tenant_id}` checks membership, rotates the session and is audited. A custom `go/analysis` analyzer (`tools/tenantctx`) fails CI if a handler reads a `tenant_id` from request params instead of `tenant.FromCtx(ctx)`. Postgres RLS is planned for the SaaS phase (roadmap item), not built now. <!-- RT#14 -->
- Auth uses a server-side session. The cookie is `__Host-lt_sess` with HttpOnly, Secure (localhost counts as a secure context), SameSite=Lax, Path=/, and a 12h idle / 7d absolute lifetime. Only the SHA-256 of the token is stored.
- Passwords use argon2id (m=64MiB, t=3, p=2, 16B salt, PHC string). Hashes are rehashed on login if the params change.
- CSRF uses a synchronizer token (hashed in the session row) sent as `X-CSRF-Token` on unsafe methods, plus an Origin/Referer allowlist check.
- RBAC roles are owner, editor and viewer. Each route declares its minimum role through the OpenAPI extension `x-min-role`, which the middleware reads from the generated spec (a single source).
- Rate limiting uses a Postgres token bucket for login and password endpoints (to stay stateless across instances), plus a per-instance in-memory limiter for general API flood defence.
- **DB roles and audit immutability:** migrations run as `loomtale_owner`; the app connects as `loomtale_app` (no DDL, no ownership). `audit_log` has a trigger that raises on UPDATE, DELETE and TRUNCATE, and `loomtale_app` holds only INSERT and SELECT on it. The audit migration's goose down **refuses** (raises an exception) instead of dropping the table. <!-- RT#14 -->
- Secrets use envelope encryption: AES-256-GCM with a per-record DEK wrapped by a KEK from `/run/secrets/master_key`, a `key_id` for rotation, and AAD=`tenant_id|kind|record_id`. Plaintext is never logged.
- **Storage, two presigners** <!-- RT#8 -->:
  - `storage.Browser` signs against the public endpoint (`127.0.0.1:9000`) with ≤10 min expiry. Uploads use a presigned POST policy that pins `eq $key` (the exact key, not a prefix), content-length-range and content-type. <!-- RT#14 -->
  - `storage.Internal` uses minio-go with the app credentials against the in-network `minio:9000` endpoint. It serves workers directly (GetObject with Range, PutObject multipart) and issues internal presigned URLs whose TTL equals the calling step's timeout plus 10 min (for FFmpeg and the Python worker).
  - Keys are `t/{tenant}/...`. The `t/{tenant}/render/` prefix is **workers-only**: `storage.Browser` refuses to sign any POST under it. Every browser URL is issued only after an ownership check on the `assets` row. <!-- RT#14 -->
  - Finalize does a HEAD plus a magic-byte sniff (first 512B range GET) before marking an asset `ready`, and records `sha256` (computed by the uploader for worker writes via `x-amz-checksum-sha256`, or by a finalize hash step for browser uploads).
- **Log scrubbing:** an slog `ReplaceAttr` drops keys matching `(?i)token|secret|password|cookie|authorization|key` and any `secretstr.String`, and a **value-level scrubber** rewrites any URL value to strip query strings containing `X-Amz-Signature`, `X-Amz-Credential`, `upload_id` or `upload_protocol` (capability URLs). The same scrubber is exported for FFmpeg stderr and step logs. <!-- RT#14 -->
- **Backups:** a `backup` compose service runs nightly at 03:00 Asia/Bangkok: `pg_dump -Fc` and `mc mirror` of the bucket to `${BACKUP_TARGET}`, an **encrypted cloud bucket (Cloudflare R2 or Backblaze B2)** — dumps and objects are encrypted client-side (age or restic) before upload, credentials only from secrets, a bucket-scoped write-only key (validated 2026-09-24), keeping 7 daily and 4 weekly copies. Each run records its outcome in a `backup_runs` table, which `/readyz` reports as detail (the phase 5 status bar warns when the last success is older than 36h). Postgres gets `max_wal_size=2GB` and `wal_compression=on`. <!-- RT#10 -->
- Bootstrap: the `loomtale create-owner` CLI replaces open signup.

## Architecture
- Tables: `tenants`, `users`, `memberships(tenant_id,user_id,role)`, `sessions(…, active_tenant_id)`, `audit_log`, `secrets(kind, owner_ref, key_id, wrapped_dek, nonce, ciphertext)`, `assets(kind, storage_key, mime, bytes, sha256, width, height, duration_ms, variants jsonb, status)`, `rate_limit_buckets`, `tenant_quotas` (columns only; `quota.Check` is called from `pipeline.Enqueue` in phase 3, unlimited by default locally). River tables are migrated through `rivermigrate` inside `loomtale migrate`. <!-- RT#2 -->
- Middleware order: RequestID → Recover → RealIP (trusted proxy list) → SecurityHeaders → RateLimit → Session → CSRF → OapiRequestValidator (kin-openapi, from the bundle) → Tenant ctx (from session only) → RBAC → handler. Errors are returned as problem+json.
- **Security headers (CSP decided now, enforcing, never Report-Only)** <!-- RT#14 -->:
  `Content-Security-Policy: default-src 'self'; script-src 'self'; object-src 'none'; img-src 'self' blob: data: ${MEDIA_ORIGIN}; media-src 'self' blob: ${MEDIA_ORIGIN}; connect-src 'self' ${MEDIA_ORIGIN}; font-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; require-trusted-types-for 'script'; trusted-types default dompurify`
  - Rationale for `style-src 'unsafe-inline'`: Radix (floating-ui positioning), sonner (runtime style tag) and TipTap/ProseMirror write inline styles. Script execution stays locked to `'self'` with Trusted Types, so the residual risk is CSS injection only, which is accepted and documented. Revisit if the shell is ever server-rendered with nonces.
  - Plus `X-Content-Type-Options`, `Referrer-Policy: strict-origin-when-cross-origin` and `Permissions-Policy`. HSTS is sent only when `PUBLIC_URL` is https. Caddy and the API share the header values from one env file. A CI test asserts the enforcing header is present and that no `Content-Security-Policy-Report-Only` header is sent.
- Presign cache-friendliness (browser presigner only): the signing time is floored to a 5-minute window and URLs expire after 10 minutes, so the same asset yields a byte-identical URL within a window and browser caching works.

## Related files
- Create:
  - `db/migrations/*_db_roles.sql`, `*_core_tenancy.sql`, `*_assets.sql`, `*_audit.sql`, and `db/queries/{auth,tenancy,assets,audit}.sql`
  - `api/internal/{auth,rbac,csrf,ratelimit,audit,crypto/envelope,storage,assets,tenant,validation,secretstr,obs/scrub}/`
  - `tools/tenantctx/` (analyzer), `openapi/paths/{auth,assets}.yaml`, `openapi/schemas/{auth,assets,problem}.yaml`
  - `scripts/lint-tenant-queries.sh`, `deploy/backup/backup.sh`
- Modify: `api/cmd/api/main.go` (middleware chain), `api/cmd/loomtale` (migrate, create-owner), `deploy/compose.yml` (master_key secret, `backup` service, postgres WAL args, two DB role secrets), `deploy/caddy/Caddyfile` (headers), `openapi/root.yaml`.

## Implementation steps
1. Write the DB role migration (`loomtale_owner`, `loomtale_app`), the other migrations and sqlc queries, and the UUIDv7 helper (`github.com/google/uuid` NewV7).
2. Write the envelope crypto with a KEK loader (32B base64 file, fail-fast if absent), a rotation-ready `key_id`, and property tests.
3. Implement argon2id hashing, the session store, login/logout/me, `switch-tenant` and CSRF token issuance (`GET /auth/csrf`).
4. Implement the RBAC middleware that reads `x-min-role` from the loaded spec, plus a startup check that every operation declares a role (deny by default). Implement the `tenantctx` analyzer.
5. Implement the Postgres token bucket (login: 5/min per IP+username, 20/hour per IP) and the in-memory limiter.
6. Implement the audit writer (in the same tx as the action), the immutability trigger, the refusing down migration, and `GET /audit` (owner only, cursor paginated).
7. Implement the storage package: both presigners, POST policy with `eq $key`, the workers-only `render/` rule, finalize sniff (allowlist per asset kind: png/jpeg/webp/avif, wav/flac/mp3, mp4, txt/md), sha256 recording and size caps per kind. Configure bucket CORS for the web origin only.
8. Mount the request validator from the bundled spec and a problem+json error mapper.
9. Write the `create-owner` CLI (password read from stdin, never argv).
10. Add the security headers in the API and Caddy, the redacting logger and the value-level URL scrubber.
11. Write the backup service and script; run it once manually and list the produced files.

## Todo checklist
- [ ] DB roles + tenancy schema + tenant query lint + tenantctx analyzer
- [ ] Envelope encryption + tests
- [ ] Sessions, argon2id, CSRF, switch-tenant
- [ ] RBAC from `x-min-role`, deny by default
- [ ] Rate limiting (DB + memory)
- [ ] Audit log (trigger, role split, refusing down)
- [ ] Two presigners + finalize sniff + sha256
- [ ] Spec validator + problem+json
- [ ] create-owner CLI
- [ ] Headers (final CSP) + redacting logger + URL scrubber
- [ ] Nightly backup + WAL settings

## Performance budget checks
- Login p95 must stay ≤400ms (argon2 dominates). All other authenticated reads must add ≤3ms of middleware overhead (benchmark `BenchmarkMiddlewareChain`).
- Session lookup uses a single indexed query on `sessions(token_hash)`. `last_seen` is written at most once per 5 min per session.
- All list endpoints use cursor pagination on `(tenant_id, id)` with UUIDv7 ordering, and `EXPLAIN` in the tests must show an index scan.
- The API never proxies object bytes. A test asserts that no API handler reads from S3 streams except finalize's 512B sniff (workers read through `storage.Internal`).

## Security checklist
- [ ] Cross-tenant access returns 404 (not 403) in the integration test matrix; tenant never read from request params (analyzer)
- [ ] Session fixation: token rotated on login and tenant switch; logout deletes row
- [ ] CSRF enforced on every POST/PUT/PATCH/DELETE; Origin checked
- [ ] Secrets and capability URLs never in logs (test scans captured log output for planted values and a planted presigned URL)
- [ ] Browser presigned URLs ≤10 min, exact key pinned, `render/` refused; internal URLs step-length TTL and never sent to the browser
- [ ] Upload MIME sniffed server-side; declared type ignored for trust
- [ ] `audit_log`: UPDATE/DELETE/TRUNCATE raise even as table owner; app role INSERT/SELECT only; down migration refuses
- [ ] KEK absent → process refuses to start
- [ ] Enforcing CSP only (no Report-Only), `object-src 'none'`, Trusted Types

## Reuse points
- Create once, used by every later phase: `storage.Browser`/`storage.Internal`, `assets.Finalize`, `envelope.Seal/Open` (YouTube tokens in phase 10, API keys in phase 4), `audit.Record`, `rbac` via spec extension, `httpx.Problem`, `obs/scrub.URL`, the tenant-scoped sqlc conventions and the cursor pagination helper `httpx.Cursor`.

## Tests
- `scripts/tb.ps1 test` for unit tests (crypto, argon2, csrf, cursor, URL scrubber, analyzer testdata).
- `scripts/tb.ps1 test-integration`, which runs against real Postgres and MinIO from compose: the auth flow, tenant switch, the tenant isolation matrix, presign → upload with curl → finalize, rejection of a mismatched MIME type, rejection of a POST to a different key or to `render/`, and `UPDATE audit_log` failing as both roles.
- `bash scripts/lint-tenant-queries.sh`
- `docker compose run --rm backup /backup.sh` produces a dump and a mirror in `${BACKUP_TARGET}`.

## Success criteria
- The owner can log in, `/me` returns their role, and a viewer gets 403 on editor routes.
- A second tenant cannot read or presign the first tenant's asset, as proven by tests.
- Encrypted secrets round-trip, while a DB dump shows only ciphertext.
- The enforcing CSP and headers are present on both the API and the SPA responses (curl assertions), and no Report-Only header exists.
- A manual backup run produces a restorable `pg_dump` file and an encrypted bucket mirror in the cloud backup bucket (the restore drill is in phase 12).

## Risks + rollback
- The CSP breaks the SPA or media (Medium×Medium): the policy is decided above against the known inline-style users, and the phase 12 Playwright listener fails on any violation. No Report-Only escape hatch. <!-- RT#14 -->
- Losing the KEK means secrets become unrecoverable (Low×High). Document a backup of `secrets/master_key` in the deployment guide (kept separately from `${BACKUP_TARGET}`), and keep tokens re-obtainable by re-connecting OAuth.
- The cloud backup bucket is unreachable or its credentials expire (Medium×Medium): the backup service fails loudly (non-zero exit logged, Dashboard warning added in phase 5 through `/readyz` detail).
- Rollback: goose down migrations exist for each file except `audit_log`, whose down refuses by design; roll that one back only by restoring a backup. The phase ships as its own PR.

## Next steps
Phases 3, 4 and 5 start in parallel.
