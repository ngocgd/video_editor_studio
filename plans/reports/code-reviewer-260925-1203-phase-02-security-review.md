# Code review: feat/db-auth-security-foundation (phase 02 security foundation)

Scope: `git diff main...feat/db-auth-security-foundation` (3 commits, 130 files). Worktree `agent-a03e2529a410b8342`. Read-only.
Verified live: FK-cascade vs audit trigger, and owner `DISABLE TRIGGER`, in a throwaway postgres:17 container. Scrubber behaviour checked with a golang:1.26 probe that ran a copy of `scrub.go` in the scratchpad.
Paths are repo-relative. `api/` = `api/internal/` unless noted.

**Overall:** The core design holds up. Token and CSRF values are stored only as SHA-256, compares are constant-time, cookies use `__Host-` with HttpOnly/Secure/Lax, tenant scope comes from the session, every current query is tenant-scoped, the POST policy pins `eq $key`, CSP is byte-identical in API and Caddy, and the new two-statement rate limiter has no over-admission race: `UPDATE ... WHERE tokens >= 1` re-checks the row after taking its lock. The gaps are resource-exhaustion DoS, one schema bug (verified), a superuser credential that bypasses the role split, and tests that mostly cover happy paths.

## High

**H1. Pre-auth OOM DoS through argon2id memory. merge-blocking: YES**
- Where: `auth/password.go:18-24` (m=64MiB per hash), `authapi/login.go:44-66`, `cmd/api/main.go:153-154` (per-IP bucket capacity 20 = burst of 20), `deploy/compose.yml:139` (`mem_limit: 512m`).
- Failure: one unauthenticated IP sends 20 concurrent `/auth/login` requests with random emails. Each one runs a full argon2 (the dummy-hash decoy runs too), so 20 × 64MiB ≈ 1.3GB against a 512MB limit. The API is OOM-killed and everyone's sessions stop working. Repeat from new IPs.
- Fix: cap concurrent argon2 work with a semaphore (for example `GOMAXPROCS` or 4 slots) and return 429 or 503 when it is full. Set `GOMEMLIMIT`. Consider a lower per-IP burst.

**H2. No request body size limit anywhere. merge-blocking: YES**
- Where: `validation/middleware.go:41-46` (`openapi3filter` reads the whole body into memory); no `http.MaxBytesReader` exists in `api/`; `deploy/caddy/Caddyfile:12-14` has no `request_body { max_size }`.
- Failure: an anonymous `POST /api/v1/auth/login` with a 1GB body is buffered by the validator and then decoded again by the strict handler, which OOMs the API. `password` also has no `maxLength` (`openapi/schemas/auth.yaml:8-10`), so a multi-MB password goes straight into argon2.
- Fix: a global `http.MaxBytesReader` middleware (for example 64KiB for JSON) ahead of the validator, Caddy `request_body max_size`, and `maxLength: 1024` on password and `maxLength: 320` on email.

**H3. Hardcoded Postgres superuser `loomtale`/`loomtale` defeats the role split and audit immutability. merge-blocking: YES (cheap fix)**
- Where: `deploy/compose.yml:43-45`. This existed on main, but this phase's guarantees depend on it being fixed.
- Failure: any container on `loomtale_core` (worker, backup, minio-init, a future ComfyUI node) connects as superuser with a password that is committed in the repo. It can then `ALTER TABLE audit_log DISABLE TRIGGER`, read `sessions`, and so on. The least-privilege work in `db_roles.sql` does nothing against this.
- Fix: `POSTGRES_PASSWORD_FILE=/run/secrets/postgres_superuser_password` using a generated secret (add it to `dev-secrets-init.sh`). Drop the unused `POSTGRES_PASSWORD=change-me` from `.env.example`.

**H4. The audit trigger blocks every DELETE on `tenants` and `users` (verified live). merge-blocking: YES (fix now, before the immutable migration ships)**
- Where: `db/migrations/20260925010500_audit.sql:8-9` (FKs `ON DELETE SET NULL`) combined with `:30-32` (statement-level `BEFORE UPDATE` trigger).
- Failure: the FK's SET NULL action runs `UPDATE ONLY audit_log ...`, which fires the trigger. The trigger is statement-level, so it fires even when no audit rows match. In the probe, `DELETE FROM tenants WHERE id=2` failed with "append-only: UPDATE" even though tenant 2 had no audit rows. Result: tenants and users can never be deleted (offboarding, GDPR erasure, test cleanup). Because the down migration refuses, fixing this later needs a forward migration on an immutable table.
- Fix: drop the FKs on `audit_log.tenant_id` and `actor_user_id` (keep the uuid columns and an index), or use `ON DELETE NO ACTION` together with soft-delete. Add an integration test that deletes a user.

**H5. Backups break at media scale, and object copies are never pruned. merge-blocking: no (fix before real media is stored)**
- Where: `deploy/backup/backup.sh:69-78` stages a plaintext mirror plus an encrypted copy in `/tmp`, which is tmpfs (`compose.yml:245-246`, `mem_limit: 512m` at `:227`). tmpfs pages count against the cgroup.
- Failure: once the bucket passes a few hundred MB (a single 2GiB video, per `storage/sniff.go:17`), the nightly run hits ENOSPC or an OOM kill. Separately, `target/objects/$now_ts` is a full copy every night and the prune (`:86-102`) only covers `postgres/`, so cloud storage grows by the full bucket size every day. This violates the 7-daily/4-weekly retention in the spec.
- Fix: stream per object (`mc cat | age | mc pipe`) or use restic/rclone-crypt for incremental encrypted sync, and apply retention to objects as well.

**H6. Client IP collapses to the proxy's IP, so the 20/h per-IP login limit becomes global. merge-blocking: no (must fix before any non-localhost exposure)**
- Where: `httpx/real_ip.go:12-18` (trusts all of RFC1918 by default) and `:66-73`; Caddy (`Caddyfile:12-14`) has no `trusted_proxies`. Caddy listens on plain `:8080`, so https is terminated upstream.
- Failure: behind Docker Desktop's port forwarding or an upstream TLS terminator, Caddy sees a single peer IP and writes that into XFF. Every user then shares one `login:ip:` bucket and one in-memory 100/min bucket, and 20 bad logins an hour lock everyone out. Any container on the compose network can also spoof XFF to the API.
- Fix: set Caddy `trusted_proxies` to the real upstream CIDR. Narrow the API's default trusted list to Caddy's address or subnet (configured explicitly). Document this in the deployment guide.

## Medium

**M1. `rbac/rbac.go:97-104`: routes marked `viewer` skip the tenant check when no tenant is in context. merge-blocking: no**
- `getAsset` and `listAssets` are `viewer` (`openapi/paths/assets.yaml:63,89`). A session with a revoked membership or no membership reaches the handler, and `tenant.MustFromCtx` panics, returning a 500 with a stack trace in the logs.
- This breaks deny-by-default. A future viewer handler that uses `FromCtx` leniently would run without tenant scope.
- Fix: add a separate `x-min-role: authenticated` for the account routes (me, csrf, logout, switch-tenant), and make `viewer` require a tenant.

**M2. `authapi/switch_tenant.go:50,78`: tenant switch throws away the new CSRF token. merge-blocking: no**
- `Rotate` replaces `csrf_token_hash`, but the 200 response is `Me` only (`openapi/paths/auth.yaml` switchTenant), so `created.CSRFToken` is lost.
- Every unsafe request after a switch returns 403 until the client calls `/auth/csrf`.
- Fix: return `{me, csrfToken}`, like login does.

**M3. `authapi/csrf.go:15-21` + `auth/store.go:119-128`: `GET /auth/csrf` rotates the token (a GET with side effects). merge-blocking: no**
- A second tab, or a cross-site top-level navigation (Lax sends the cookie), invalidates the SPA's token, causing CSRF 403 lockouts or a nuisance DoS.
- Fix: keep one stable token per session and return it (store it encrypted, or derive it as HMAC(session secret)), or make the endpoint a POST.

**M4. `db/queries/auth.sql:27-36` + `auth/store.go:77`: `RotateSession` resets `expires_at` to now+7d. merge-blocking: no**
- Switching tenants at least once a week keeps a session alive indefinitely, defeating the 7-day absolute lifetime.
- Fix: keep the original `expires_at` on rotate.

**M5. `obs/scrub/scrub.go:36,46-52`: capability URLs leak (verified with the probe). merge-blocking: no (fix before the worker/FFmpeg phase)**
- Leaks:
  - URLs embedded in text (FFmpeg `Opening 'http://…?X-Amz-Signature=…'`, which is the stated use of the exported `URL()`) are returned unchanged.
  - Lowercase `x-amz-signature` is not matched.
  - `error`-typed attrs (KindAny) are never scrubbed. `problemErrorHandler` logs `"error", err` (`cmd/api/main.go:235`), and a `*url.Error` from minio-go carries the full signed URL.
- The spec checklist item "planted presigned URL scan" is not met.
- Fix: regex-scrub `(?i)[?&](x-amz-signature|x-amz-credential|upload_id|upload_protocol)=[^&\s'"]*` in any string, and scrub `err.Error()` for KindAny values that implement `error`.

**M6. `assetsapi/presign.go:42` + `storage/browser.go:57`: re-uploading after finalize bypasses the sniff. merge-blocking: no**
- The POST policy stays valid for 10 minutes after issue. A client uploads a valid PNG, finalizes, then POSTs arbitrary bytes to the same key.
- The asset stays `ready` with a stale `sha256` and the sniff verdict no longer applies. Later worker parsing (FFmpeg) will consume unverified input; FFmpeg input disguised as media is a known SSRF and local-file-read vector.
- Fix: on finalize, server-side copy to an immutable final key (or enable object versioning and pin the VersionId), and point workers only at the final key.

**M7. Audit immutability covers the app role only. merge-blocking: no**
- `audit.sql:28-29` claims the owner is blocked, but the owner can `DISABLE TRIGGER` (verified).
- Owner credentials sit in `.env` and in the long-lived backup sidecar (`compose.yml:232`), which also holds the MinIO root credentials (`:229`, `backup.sh:57`).
- Fix: a dedicated read-only `loomtale_backup` role (SELECT only) and a read-only MinIO user for backup. Correct the migration comment.

**M8. Backup key handling. merge-blocking: no**
- `backup.sh:45-47` mounts the age *identity* (private key) on the host and only needs the recipient. Host compromise plus the target credentials exposes every offsite backup.
- The prune (`:100`, `mc rm`) needs delete rights, which contradicts the spec's "write-only key". With a truly write-only key, the prune fails silently (`|| true`).
- Fix: mount only the public recipient and keep the identity offline. Use lifecycle rules or object-lock on the bucket for retention instead of `mc rm`.

**M9. Tenant guards have escapes. merge-blocking: no (no current query escapes; checked every query in `db/queries/*.sql`)**
- `lint-tenant-queries.sh`:
  - `:6-8` vs `:26`: csplit puts the line *above* `-- name:` into the previous block, so the documented allow-comment placement exempts the wrong query.
  - `:35` does not detect `JOIN assets`.
  - `:18` omits `tenant_quotas` (which has `tenant_id`) and `sessions`.
  - `:49` is satisfied by comment text or by `OR tenant_id = @tenant_id`.
- `tools/tenantctx/analyzer.go:145-159` only checks the value expression of a composite-literal field. `switch_tenant.go:32,35` passes `req.Body.TenantId` through a local variable with no allow-comment, which proves the escape. Path params (`req.Id`) and `RequestFromCtx(ctx).Header` are also never flagged.
- Fix: derive the table list from migrations (columns named `tenant_id`), match `JOIN`, strip comments before matching, and have the analyzer track local variables within the function (SSA or simple def-use).

**M10. `validation/middleware.go:30-38`: the validator fails open on a route-lookup miss, and nothing tests that it is active. merge-blocking: no**
- Content-type rejection (the main defence against login-CSRF via `text/plain` form posts) and schema constraints depend on it.
- Fix: return 404 or 500 on a miss for `/api/v1/*`, and add an integration test that a `text/plain` login gets 400.

**M11. `db/queries/secrets.sql:1-9` vs `crypto/envelope/envelope.go:30`: AAD `record_id` breaks on upsert. merge-blocking: no (latent: no caller yet)**
- `ON CONFLICT` keeps the old `id`. A caller that seals with a freshly generated id gets a row whose ciphertext is bound to an id that no longer exists, and `Open(AAD(...row.ID))` then fails permanently.
- Fix: bind the AAD to the natural key `tenant|kind|owner_ref`, or return the id and seal after the upsert.

**M12. Integration tests mostly cover happy paths and do not run in CI. merge-blocking: no (strongly recommended before the next phase)**
- `integration/setup_test.go:318,332` and `session_helper_test.go:270-280` skip silently, and the suite is not in CI (per the cook report).
- `auth_flow_test.go:93,128`: both assert 403, but RBAC and CSRF both return 403, so a broken CSRF check that always rejects still passes.
- Missing tests:
  - old cookie returns 401 after logout and after a switch (rotation)
  - wrong or foreign-session CSRF token; bad or missing Origin
  - TRUNCATE; DELETE as the app role
  - list and audit isolation (`listAssets`, `listAudit` cross-tenant)
  - rate limit 429
  - presign → upload → finalize, a mismatched-MIME upload, a POST to a different key or under `render/` (all required by the spec's Tests section)
  - a planted-secret log scan

## Low (merge-blocking: no)
- L1 `auth/store.go:95-97`: DB errors are swallowed as "no session", so an outage looks like mass logout (401) instead of a 5xx. The `err` branch in `middleware.go:27` can never fire.
- L2 `authapi/login.go:51`: the per-user bucket key uses the email as typed (not lowercased), so case variants get fresh buckets. There is no per-account limit across IPs. `rate_limit_buckets` and expired `sessions` are never pruned (unbounded growth).
- L3 `deploy/postgres/init-roles.sh:15-23`: passwords are interpolated into SQL run as superuser, so a `'` in an operator-chosen secret breaks or injects. Use `psql -v` with `:'var'` quoting.
- L4 `scripts/dev-secrets-init.sh:32`: secrets are written with mode 0644 (use `umask 077`). Line 73 writes a literal `\n`, so sourcing it in `backup.sh:53` yields broken variables.
- L5 `validation/middleware.go:47`: returns kin-openapi `err.Error()`, which includes schema internals and possibly the submitted value (for example a too-short password). Return a generic detail.
- L6 `Caddyfile:21-26`: no HSTS on SPA responses when `PUBLIC_URL` is https (the API sends it), so the header parity the spec requires is broken.
- L7 `envelope.go:118`: `gcm.Open` panics on a wrong nonce length (corrupt row), so check `len(Nonce)`. The wrapped DEK is not AAD-bound to `key_id`. There is a single KEK and no keyring, so no rotation path yet.
- L8 `login.go:96-117`: re-login does not revoke the previous session row. `login.go:122` stores the raw typed email in the audit log, which can capture a password typed into the email field.
- L9 `storage/finalize.go:61-67`: browser uploads usually have no `sha256` (the spec asks finalize to compute one). `document` content is never inspected. The `ftyp` sniff accepts HEIC/MOV as `video/mp4`.
- L10 `cmd/api/main.go:182`: chi `middleware.Logger` writes plain text outside slog, bypassing the scrubber. `X-Request-Id` (`httpx/request_id.go:18`) is taken from the client unvalidated and echoed into logs.

## Checked and found no defect
- argon2id params match the spec, rehash-on-login works, the decoy hash gives timing parity for unknown emails.
- Token: 32B from crypto/rand, only SHA-256 stored, unique index.
- Idle and absolute expiry are enforced; logout deletes the row; login always creates a new session.
- CSRF compare is constant-time; Origin allowlist.
- RBAC fails at startup on a missing or unknown `x-min-role`.
- The trigger raises on UPDATE/DELETE/TRUNCATE; the app role has no TRUNCATE or DDL; no sequences exist (UUIDs); the down migration refuses.
- Per-record DEK with a random 96-bit nonce and a fresh DEK each time, so no nonce reuse; the KEK fails fast.
- POST policy pins `eq key`, content-type and length range, TTL ≤10m; keys are generated server-side, so `render/` is unreachable from the browser.
- `secrets/*` is gitignored and only `.gitkeep` is tracked.
- Cursor queries are backed by `(tenant_id, id)` indexes.

## Recommended order
H1, H2, H3, H4 before merge (each is small). Then M1–M4 and M12 before phase 5 SPA work. Fix H5/H6/M8 before any real deployment, and M5/M6 before the worker phase.

## Unresolved questions
1. How is TLS terminated in the deployment target (Cloudflare Tunnel, host Caddy, other)? H6's fix depends on the trusted CIDR.
2. Must tenants and users ever be hard-deleted (SaaS offboarding, GDPR)? This decides H4's fix (drop FKs vs soft-delete).
3. Is `127.0.0.1` (not `localhost`) accepted as a secure context for `__Host-`/Secure cookies in every browser the team uses? The default `ALLOWED_ORIGINS` and the compose ports use `127.0.0.1`.
4. Does MinIO reject unlisted POST form fields (for example `Content-Disposition`, `x-amz-meta-*`) the way S3 does? Not verified. If it doesn't, uploads can set extra headers on stored objects.
