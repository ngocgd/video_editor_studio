# Independent review: YouTube channel connection, quota ledger and upload client (verify after Docker returned)

Branch `feat/youtube-channels-upload` at `d94b2e0`, reviewed against `main` at `4962ecd` (the branch already contains it). Main has since moved to `a20fd7c` (the scoring, depth and trainer engines merge: pyworker, model manifest and bench files only). `git merge-tree` shows a clean merge with no overlapping files, so the results below carry over; the merge step must still regenerate the generated code.

The reviewer is lane c's verifier and did not write this code. The scope is the first part of phase 10: Google OAuth with sealed channel tokens, the channel connect/list/audit/disconnect API and the Settings page, the YouTube Data API client with the resumable upload, the quota ledger, the migration and the tests. Publications, the review page, pre-publish checks, thumbnails and scheduling are deferred to the second part.

## Verdict

**Ready to merge.** There are no Critical or High findings. The High and both Medium findings from the earlier review are fixed and covered by regression tests, which pass under `-race`. The toolbox gen/lint/test run, the integration suite (with the migration applied to a real Postgres) and the new Playwright spec are all green. Four Low findings remain open and do not block the merge.

## Earlier findings, re-checked

- **High, operator LLM key fallback: fixed** (`32ee61d`). `secrets.Store.Open` wraps both `secrets.ErrNotFound` and `pgx.ErrNoRows` (`fmt.Errorf("secrets: get %s: %w (%w)", ...)`), so `registry.ResolveName` falls back to the operator's adapter again. `TestResolveNameFallsBackThroughTheRealSecretsStore` goes through the real store. `channelsapi.revokeStored` still matches on `secrets.ErrNotFound`, so both callers get what they expect.
- **Medium, 30-second timeout on upload chunks: fixed** (`9a37ce1`). A data chunk PUT uses a copy of the base client with `Timeout = 0`. The copy shares the transport, so the netguard host allowlist and the dial and TLS timeouts still apply. Each chunk has its own deadline: 2 minutes plus 1 second per 128 KiB, which is about 4 minutes for 16 MB and about 10.5 minutes for 64 MB. A missed chunk deadline with a live parent context becomes a transient `upload_chunk_timeout`. Status queries, session opening and cancel keep the 30-second client timeout.
- **Medium, session URI in error text: fixed** (`9a37ce1`). `redactURL` unwraps `*url.Error` to `Op` plus the cause, and `transportError` and the request-construction path in `sendRange` both use it. Nothing else formats the session URI: the 200/201 decode error and `failure` do not include the request URL.

## Findings

### Critical / High / Medium

None.

### Low (open, carried from the earlier review)

**L1. A crafted `reason` crashes the Settings page.** `validateYouTubeSettingsSearch` accepts `[a-z_]{1,40}`, and `REASON_TEXT[search.reason]` is a plain object lookup. `?connect=error&reason=__proto__` makes the message `Object.prototype`, and React throws when it renders an object. A link can therefore break the page for whoever opens it. Nothing is injected. Fix: `Object.hasOwn(REASON_TEXT, reason)` or a `Map` (`web/src/features/settings-youtube/connect-result.ts`).

**L2. Channel avatars are blocked by the CSP.** `img-src` allows only `'self' blob: data:` and the media origin, so the `yt3.ggpht.com` or `googleusercontent.com` thumbnail is a broken image in the deployed app.

**L3. The OAuth callback answers JSON 401 without a session.** A browser that comes back from Google without a valid session gets a problem document instead of a redirect with `reason=invalid_state`. This is not a security issue. The session cookie is `SameSite=Lax`, so the normal top-level redirect from Google does carry it.

**L4. A reconnect of an already connected channel does not revoke the previous refresh token.** The upsert keeps the row id and `Secrets.Put` overwrites the sealed token, so the old grant stays live at Google until the user removes it.

### Low (new)

**L5. A code exchange without a refresh token leaves the grant in place.** `oauthgoogle.Config.Exchange` returns `no_refresh_token` when Google omits it, and the callback answers `exchange_failed` without revoking anything. With `prompt=consent` Google always returns a refresh token, so this is unlikely. If it happens, the fix is to revoke the access token before failing.

**L6. The cook report's "What shipped" still overstates the upload dedupe.** It says a 404/410 "dedupes by the `lt-<nonce>` tag before opening a new session". `Client.Upload` returns `upload_session_gone`, and the caller has to dedupe with `FindUploadByTag`. The report's review section already says so, but the summary line was not corrected.

### Checked and found sound

- **Tenant isolation.** Every `youtube_channels` query filters by `tenant_id`. `ConsumeYouTubeOAuthState` deletes by state hash and returns the row only for the same tenant and session, before the expiry check. `lint-tenant-queries.sh` covers both new tenant tables, and the only unfiltered statement is the annotated cleanup of expired states. The integration test checks that another tenant can neither list nor disconnect a channel. `quota_ledger` is per Google project on purpose. There is no RLS in this schema, and the new tables receive the app role's grants through the default privileges from the roles migration.
- **Authorization.** In `openapi/paths/channels.yaml`, list is `x-min-role: viewer`. Connect, callback, audit toggle and disconnect are `owner`. The audit note is limited to 2000 characters in both the schema and the database CHECK.
- **OAuth.** The flow uses PKCE S256, a 32-byte random state stored as its sha256, a 10-minute TTL, single-use consumption, `access_type=offline` and `prompt=consent`. The three scopes are requested once. A grant without the upload scope, without a channel, over quota or failing to store is revoked. The redirect carries only fixed reason codes and a server-generated channel id.
- **Secrets.** The refresh token is sealed with AAD over tenant, kind and owner, and it is never in an API response. Disconnect revokes (best effort, recorded in the audit), deletes the secret and marks the row disconnected, and a second disconnect is a no-op 204. The token and revoke calls send the token in the form body, not the URL. The client secret is read from a file and a half-configured client is a startup error.
- **SSRF.** Every Google call goes through `netguard.Client`, limited to `oauth2.googleapis.com` and `www.googleapis.com` with redirects not followed. The chunk client copy keeps that transport, so a forged `Location` cannot reach another host.
- **Quota.** `ReserveQuotaUnits` is a single conditional upsert that cannot overspend, and the integration test proves this under concurrency against Postgres. Pacific-time dates and the DST-aware reset are unit-tested. A `quotaExceeded` answer marks the day exhausted.
- **Upload.** There is one chunk buffer (16 MB by default, at most 64 MB, in 256 KiB multiples) and one ranged GET per chunk that must fill the buffer exactly (`io.ReadFull`), with no temp files. The session URI is persisted before the first byte. A resume queries `bytes */N`, and a partial acceptance rehashes and resends. The sha256 is compared before the final chunk, and a mismatch cancels the session.
- **Board.** No CHANGE line from another lane touches this branch's files. Lane e (`feat/analytics`) was told to pick up `32ee61d` and `9a37ce1`. Lane g adds a `youtube_channels_tenant_id_id_key` UNIQUE constraint in its own migration, which does not conflict with this migration. `scripts/lint-tenant-queries.sh` `TENANT_TABLES` is also edited by other lanes, so the merge must take the union.

## Verification run by the reviewer

| Check | Result |
|---|---|
| `scripts/tb.sh gen lint test` (toolbox, `go test -race`) | pass: go vet, golangci-lint 0 issues, tenantctx, `lint-tenant-queries: OK`, models lint, api and tools tests with `-race`, pytest 66 passed, 2 skipped |
| Generated-code drift (host `git status` after toolbox gen) | none |
| web `npm run typecheck`, `npm run lint`, `npm test` | pass (17 files, 89 tests) |
| web `npx vite` bundle and `npm run budget-check` | pass |
| Integration: compose.yml plus the integration overlay, `loomtale-c`, `scripts/test-integration-toolbox.sh`, under the heavy lock | pass: 74 passed, 0 failed, 0 skipped. This includes all five YouTube and quota tests and the migration applied to a real Postgres. Torn down with `down -v`. |
| Playwright, compose.yml, seeded owner, `--workers=1`, under the heavy lock | pass on a fresh stack: 4 of 4 passed, including `youtube-settings.spec.ts`. The first run on a fresh stack passed 3 of 4. The smoke spec's Ctrl+K palette did not open within 5 seconds, although the palette's only change on this branch is one extra item. Rerun alone three times on the same stack, the smoke spec passed all three, so that failure was a flake. On that reused stack, a full run after those extra logins then timed out at the writer spec's login. That is the per-IP budget problem lane a found on main and fixed on its own branch, and it is not caused by this branch. A third run on a fresh stack passed 4 of 4. Every stack was torn down with `down -v`. |

## Success criteria

- A rendered episode passes review and uploads as private with thumbnail, title, description, tags, chapters and the synthetic-content flag, verified in Studio: **pending**, deferred to the second part of this phase (needs phase 8 renders and phase 9c scores). The live half also needs a real Google OAuth app and a real channel, which are external.
- A scheduled upload sets `publishAt` when audited, and is disabled with the reason otherwise: **pending**, deferred to the second part of this phase (needs phase 8 renders for publications). The audit toggle it depends on is built and covered by the integration test.
- A killed upload resumes without restarting from byte 0, and no crash point duplicates a video: **met at the client level.** The fake-server unit tests pass under `-race`: resume from the reported offset, a 200 on the status query after the final chunk, and `FindUploadByTag` adoption. The publication CAS and the pipeline step with the nonce dedupe are deferred to the second part of this phase (needs phase 8 renders).
- Upload memory is at most 64 MB, with no temp files: **met** by construction (reviewed).
- Manual live test (connect, eligibility shown, observed quota): **pending, external.** It needs a real Google OAuth app and a real channel.

## Unresolved questions

- The smoke spec's Ctrl+K step failed once in six runs, and the code involved is on main (the shortcut scope stack in `web/src/lib/shortcuts.ts`). Should a later hardening pass make that step wait for the shortcut listener?

- Should the second part of the phase keep 16 MB chunks with the context-only deadline, or size chunks from a measured uplink?
- Which Google Cloud project and consent-screen mode will the live check use? In testing mode refresh tokens expire after 7 days.
