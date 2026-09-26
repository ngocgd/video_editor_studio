# Independent review: YouTube channel connection, quota ledger and upload client (round 1)

Branch `feat/youtube-channels-upload` at `33bd21f`, reviewed against `main` at `4962ecd`. The reviewer is lane c's verifier and did not write this code. The scope is the first part of phase 10: Google OAuth with sealed channel tokens, the channel connect/list/audit/disconnect API and Settings page, the YouTube Data API client with the resumable upload, the quota ledger, the migration and the tests. Publications, the review page, pre-publish checks, thumbnails and scheduling are deferred to the second part.

## Verdict

**Not ready to merge.** One High finding (a regression in LLM provider resolution) must be fixed. Docker is still down (the engine answers HTTP 500), so the toolbox run, the integration suite and the Playwright spec have not run on this branch.

## Findings

### High

**H1. Tenants without their own API key can no longer use the operator's Anthropic or Gemini key.** `secrets.Store.Get` now delegates to the new `Open`, which returns `fmt.Errorf("secrets: get %s: %w", kind, ErrNotFound)` when no row exists (`api/internal/secrets/store.go`). The previous `Get` wrapped `pgx.ErrNoRows`. `registry.Registry.ResolveName` (`api/internal/providers/registry/registry.go:188-197`) treats only `errors.Is(err, pgx.ErrNoRows)` as "tenant has no BYOK key, fall through to the process-wide adapter". `bootstrap.Build` always wires `reg.BYOK = secretsStore` and the `anthropic-api`/`gemini-api` factories, because `cmd/api` and `cmd/worker` always pass a non-nil store. So a tenant that selects `anthropic-api` or `gemini-api` and relies on the operator's key now gets `registry: byok lookup for "anthropic-api": secrets: get llm_api_key: secrets: not found`, and every LLM step for that tenant fails. The registry unit tests do not catch this because their `fakeBYOK` still returns `pgx.ErrNoRows`. Fix: make `ErrNotFound` keep the old contract by also wrapping the pgx error (for example `fmt.Errorf("secrets: get %s: %w (%w)", kind, ErrNotFound, err)`), or make the registry accept `secrets.ErrNotFound`. Add a test through the real `secrets.Store` (or change the fake to return what the store returns).

### Medium

**M1. The upload client inherits a 30-second whole-request timeout.** `oauthgoogle.Client` copies `base.Timeout` from the netguard client, which is 30s (`api/internal/netguard/netguard.go`). `http.Client.Timeout` covers sending the request body, so a 16 MB chunk needs an uplink of about 4.5 Mbit/s or faster, and a 64 MB chunk about 18 Mbit/s. A slower uplink fails every chunk as a transient error and the upload never finishes. The connect flow only calls `channels.list` today, so nothing breaks yet, but the second part will wire `Upload` through this client. Use a client without a whole-request timeout for session PUTs (keep dial, TLS and response-header timeouts, and rely on the context), or size chunks from the timeout.

**M2. The upload session URI can leak into error text.** `transportError` formats the `*url.Error` from `http.Client.Do` with `%v`. For a chunk PUT that error includes the full session URI (`...&upload_id=...`), which is a capability URL. The phase security checklist requires the session URI to be encrypted at rest and redacted in logs. The second part will likely store and log step errors, so redact the URL (for example unwrap `*url.Error` and keep only `Op` and `Err`) before the upload is wired.

### Low

**L1. A crafted `reason` crashes the Settings page.** `validateYouTubeSettingsSearch` accepts any `[a-z_]{1,40}` reason, and `REASON_TEXT[search.reason]` is a plain object lookup. `?connect=error&reason=__proto__` yields `Object.prototype` (and `constructor` yields a function) as the message text, and React throws when it renders an object. Use `Object.hasOwn(REASON_TEXT, reason)` or a `Map`.

**L2. Channel thumbnails are blocked by the CSP.** The card renders `channel.thumbnailUrl` (a `yt3.ggpht.com` or `googleusercontent.com` URL), but `img-src` in `deploy/caddy/Caddyfile` and `api/internal/secheaders/headers.go` allows only `'self' blob: data:` and the media origin. In the deployed app every channel avatar is a broken image. Either allow the Google image host in `img-src` or drop the image.

**L3. The OAuth callback answers JSON when the session is missing.** The callback has `x-min-role: owner`, so a browser that returns from Google without a valid session (expired or a different host name) gets a 401 problem document instead of a redirect to Settings. This is not a security problem, but a redirect with `reason=invalid_state` would be friendlier.

**L4. Concurrent reconnects of the same channel can leave a live grant.** Two callbacks for the same channel both upsert and `Put`, and the last write wins. The earlier refresh token stays valid at Google and is never revoked. This is unlikely and harmless for the studio, but it is worth a note for the second part.

**L5. The cook report overstates the upload's dedupe.** The report says the upload "dedupes by the `lt-<nonce>` tag before opening a new session" on a 404/410. `Client.Upload` returns `upload_session_gone`, and the dedupe (`FindUploadByTag`) is left to the caller. The unit test composes both. The behaviour is fine for a client library, but the second part's pipeline step must do the dedupe.

### Checked and found sound

- Tenant isolation: every `youtube_channels` query filters by `tenant_id`, `ConsumeYouTubeOAuthState` checks tenant and session, `lint-tenant-queries.sh` covers both new tenant tables, and the integration test checks that another tenant can neither list nor disconnect the channel. `quota_ledger` is per Google project by design.
- OAuth: PKCE S256, a 32-byte state stored as its sha256, bound to the session and tenant, 10-minute expiry, consumed with `DELETE ... RETURNING` before anything else. Login-CSRF (an attacker's code in a victim's session) is refused because the state must come from the same session. A grant without the upload scope, without a channel, or failing to store is revoked. The callback redirect uses fixed reason codes only, so nothing is reflected.
- Secrets: the refresh token is sealed with AAD binding tenant, kind and owner; it is never returned by the API. Disconnect revokes, deletes the secret and marks the row, and a second disconnect is a 204.
- SSRF: all Google calls go through a netguard client limited to `oauth2.googleapis.com` and `www.googleapis.com`, with no redirects followed, so a forged `Location` session URI cannot reach another host.
- Authorization: list is `viewer`, connect, callback, audit toggle and disconnect are `owner`, enforced by the generated RBAC middleware.
- Quota: `ReserveQuotaUnits` is a single `INSERT ... ON CONFLICT DO UPDATE ... WHERE` that cannot overspend under concurrency; PT dates and the DST-aware reset are correct and tested.
- Upload: one chunk buffer (default 16 MB, capped at 64 MB, 256 KiB multiples), one ranged GET per chunk, no temp files, the session URI is persisted before the first byte, resume queries `bytes */N`, partial acceptance rehashes and resends, and a hash mismatch cancels before the final chunk.
- Board: no CHANGE line from another lane touches these files. `scripts/lint-tenant-queries.sh` `TENANT_TABLES` is also edited by lanes a and d; the merge must take the union.

## Verification run by the reviewer

Docker is down (`docker info` fails), so the host toolchain was used, mirroring the Makefile.

| Check | Result |
|---|---|
| gen (migrations and model assets copy, redocly bundle, oapi-codegen, sqlc, web gen) then `git status` | no drift |
| `go vet ./...` (also `-tags=integration`) | pass |
| `golangci-lint run ./...` (also `--build-tags=integration`) | 0 issues |
| tools vet, tenantctx, `lint-tenant-queries.sh`, models lint, ruff | pass |
| `go test ./... -count=1` in api and tools (no `-race`: no C compiler on the host) | pass |
| `uv run pytest -q` | 66 passed, 2 skipped |
| web typecheck, lint, test (17 files, 89 tests), vite bundle, budget-check | pass |
| `scripts/tb.sh gen lint test` (with `-race`) | not run: Docker down |
| integration suite incl. `youtube_channels_test.go` | not run: Docker down |
| Playwright `web/e2e/youtube-settings.spec.ts` | not run: Docker down |

## Success criteria

- Private upload of a reviewed episode, verified in Studio: pending, deferred to the second part of this phase (needs phase 8 renders and phase 9c scores); the live half also needs a real Google OAuth app and channel.
- Scheduled upload sets `publishAt` when audited, disabled otherwise: pending, deferred to the second part of this phase (needs phase 8 renders). The audit toggle is built.
- A killed upload resumes without restarting and no crash point duplicates: met at the client level by the fake-server unit tests, which pass on the host. The publication CAS is deferred to the second part (needs phase 8 renders).
- Upload memory ≤64 MB, no temp files: met by construction (reviewed).
- Manual live test: pending, external (needs a real Google OAuth app and channel).

## Unresolved questions

- When will Docker be back? The toolbox, integration and e2e runs, and therefore the merge, wait on it.
- For M1, should the second part keep 16 MB chunks with a context-only deadline, or pick the chunk size from a measured uplink?
