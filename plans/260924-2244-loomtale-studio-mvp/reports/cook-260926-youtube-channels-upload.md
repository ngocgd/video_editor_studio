# Phase 10 part 1 — YouTube channel connection, quota ledger and upload client

Branch `feat/youtube-channels-upload` (lane c). This is the first part of phase 10: everything that does not need phase 8 renders or phase 9c scores. The review page, publications, pre-publish checks, thumbnails and scheduling are deferred to the second part.

## What shipped

- **Schema.** Migration `20260927300000_youtube_channels.sql` adds `youtube_channels`, `youtube_oauth_states` and `quota_ledger`. Queries live in `db/queries/channels.sql` and `db/queries/quota.sql`; `db/queries/secrets.sql` gained `DeleteSecret`. `scripts/lint-tenant-queries.sh` now covers the two tenant tables, and the OAuth state consumption is tenant-filtered.
- **Sealed secrets.** `api/internal/secrets` has kind-generic `Put`, `Open` and `Delete`, and the kind `youtube_refresh` (owner ref is the channel row id). The LLM wrappers are unchanged.
- **Google OAuth** (`api/internal/oauthgoogle`). The authorization code flow uses PKCE (S256), `access_type=offline` and `prompt=consent`, and requests `youtube.upload`, `youtube.readonly` and `yt-analytics.readonly` once. It also covers code exchange, refresh with rotation, dead-grant detection, revoke, a `TokenSource` and an authorising `Transport`. The state is stored as its sha256, is bound to the session and tenant, expires after 10 minutes and is consumed by `DELETE ... RETURNING`, so it is single use even when the wrong session presents it.
- **Channels API** (`api/internal/channelsapi`, routes `/api/v1/channels`, `/channels/connect`, `/channels/oauth/callback`, `/channels/{id}`). The routes cover connect, list, the manual API-audit toggle (date and note) and disconnect. The callback always redirects to a relative `/settings/youtube?connect=ok|error&reason=<fixed code>`, so nothing from Google or the query string is reflected. A grant that lacks the upload scope or has no channel is revoked. Disconnect revokes at Google (best effort, recorded in the audit metadata), deletes the secret and marks the row disconnected; a second disconnect returns 204. Connect, audit changes and disconnect are audited. Tokens never reach the browser.
- **YouTube client** (`api/internal/youtube`). It covers `channels.list` (mine, with `status.longUploadsStatus` read at connect for eligibility), `playlistItems.list` and `videos.list` for nonce dedupe, and error classification (quota, auth, permanent, transient). The resumable upload persists the session URI through a callback before the first byte. It streams 16MB chunks by default (256KB multiples, capped at 64MB, one buffer) through per-chunk ranged reads (`storage.Internal.ReadRange`), with no temp file. It resumes with `Content-Range: bytes */N`: a 308 resumes from the reported offset, a 200/201 adopts the finished video, and a 404/410 dedupes by the `lt-<nonce>` tag before opening a new session. A running sha256 is checked against the approved hash before the final chunk is sent, so a changed render aborts.
- **Quota ledger.** The ledger is kept per Google Cloud project with Pacific-time days and resets at PT midnight. Costs are env config with conservative defaults: insert 1600, write 50, read 1, daily limit 10000. Reservations never overspend under concurrency, and a `quotaExceeded` answer marks the rest of the day exhausted.
- **Wiring.** `api/cmd/api` reads `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET_PATH`, `GOOGLE_OAUTH_REDIRECT_URL` and `YOUTUBE_QUOTA_*`. `deploy/compose.yml` passes them to the api and `.env.example` documents them. Without a client id and secret the feature reports "not configured" and the rest of the app is unaffected.
- **Web.** Settings > YouTube (`/settings/youtube`) lists the connected channels with their eligibility, audit state and status. It offers connect (disabled with the reason when OAuth is not configured), an audit form and disconnect with confirmation, and it shows the callback outcome. The page is linked from the account settings, the command palette and the top bar titles.

## Verification

Docker was down for this whole run: the engine answers HTTP 500 and `docker_data.vhdx` is still attached to Windows as a read-only disk (Get-Disk disk 1). The host toolchain was used instead, mirroring the Makefile targets.

- gen: sqlc, oapi-codegen, the redocly bundle and the web client were regenerated on the host, and `git diff --exit-code` shows no drift. The generated files are LF.
- lint: `go vet ./...` (also with `-tags=integration`) passes and `golangci-lint run ./...` reports 0 issues (also with the integration build tag). tenantctx, `scripts/lint-tenant-queries.sh` and the models lint pass.
- test: `go test ./... -count=1` passes in api and tools. It ran without `-race` because the host has no C compiler. The fakes guard their state with a mutex for the toolbox's `-race` run.
- Unit tests against a fake YouTube server cover a chunked upload that persists the session first, a resume from the reported offset, a crash after the final chunk (no second video), a gone session deduped to the existing video, a hash mismatch that aborts before the final chunk, partial chunk acceptance, quota refusal, request validation, PT-date and reset math, and ledger exhaustion. The OAuth tests cover the PKCE URL, verifier matching, refresh rotation and dead-grant detection.
- web: `npm run typecheck`, `npm run lint`, `npm test` (17 files, 89 tests), `npx vite` bundle and `npm run budget-check` pass.
- **Pending (Docker down):** `scripts/tb.sh gen lint test` (with `-race`), the integration suite (`api/internal/integration/youtube_channels_test.go`: single-use, session-bound state; the connect, list, audit and disconnect lifecycle against a Google double; refusal of unusable grants; the unconfigured state; a ledger that never overspends under concurrency) and the Playwright spec `web/e2e/youtube-settings.spec.ts`. None of these has run yet. They must run under the heavy lock with compose project `loomtale-c` before merge.

## Success criteria

- A rendered episode passes review and uploads as private with thumbnail, title, description, tags, chapters and the synthetic-content flag, verified in Studio: **pending**, deferred to the second part of this phase (needs phase 8 renders and phase 9c scores). The live half also needs a real Google OAuth app and a real test channel, which are external.
- A scheduled upload sets `publishAt` when audited, and the option is disabled with the reason otherwise: **pending**, deferred to the second part of this phase (needs phase 8 renders for publications). The audit toggle it depends on is built.
- A killed upload resumes without restarting from byte 0, and no crash point produces a duplicate video: **met at the client level** by the fake-server unit tests (resume from the reported offset, 200 on query after the final chunk, gone session deduped by nonce). The publication CAS and the pipeline step that drive it are deferred to the second part (needs phase 8 renders).
- Upload memory is at most 64MB with no temp files: **met by construction.** There is one chunk buffer, capped at `MaxChunkSize` = 64MB, and each chunk is a fresh ranged read.
- Manual live test (connect, eligibility, observed quota): **pending, external.** It needs a real Google OAuth app and a real channel.

## Deviations

- No `golang.org/x/oauth2` or `google.golang.org/api` dependency. OAuth and the few Data API calls are hand-written with `net/http`, which keeps the dependency surface small and makes the Google double straightforward. The phase file's wording assumed the libraries.
- The client secret is read only from the file at `GOOGLE_CLIENT_SECRET_PATH`, which an operator compose override mounts. No compose `secrets:` entry was added, because a missing secrets file would break `compose up` for everyone who does not use YouTube.
- The PKCE code verifier is stored in plaintext in the state row for at most 10 minutes. It is useless without the authorization code and the client secret.
- Channel eligibility is read at connect only. The per-publish precheck belongs with publications in the second part.

## Independent review

The independent review (`plans/reports/code-reviewer-260926-1828-youtube-channels-upload-review.md`, round 1 on `112803d`) found one High, two Medium and five Low findings. This run fixed the High and both Medium findings, each with a regression test, and left the Low findings for later.

- **High, the operator's LLM key fallback: fixed** (`32ee61d`). `secrets.Store.Open` now wraps both `secrets.ErrNotFound` and `pgx.ErrNoRows` for a missing secret, so `registry.ResolveName` again falls back to the operator's `anthropic-api` or `gemini-api` adapter for a tenant without its own key. New tests go through the real `secrets.Store` over a database double that has no rows: `TestOpenMissingSecretWrapsNotFoundAndNoRows` and `TestGetMissingLLMKeyKeepsNoRowsContract` in `api/internal/secrets/store_test.go`, and `TestResolveNameFallsBackThroughTheRealSecretsStore` in the registry tests. All three fail without the fix and pass with it.
- **Medium, the 30-second timeout on upload chunks: fixed** (`9a37ce1`). A data chunk PUT now uses a copy of the base client without its whole-request timeout. The copy keeps the transport, so dialing, TLS and the host allowlist are unchanged. Each chunk is bounded by its own deadline: 2 minutes plus one second per 128 KiB, which assumes an uplink of about 1 Mbit/s (about 4 minutes for a 16 MB chunk). A missed chunk deadline is a transient `upload_chunk_timeout`, and a retry resumes the session. Opening a session, the status query and the cancel keep the base client's timeout. Tests: `TestUploadChunkOutlivesBaseClientTimeout` and `TestUploadChunkDeadlineIsTransient`.
- **Medium, the session URI in transport errors: fixed** (`9a37ce1`). `transportError` and `sendRange` drop the URL from a `*url.Error` and keep only its operation and cause, including when the caller's context is cancelled. Tests: `TestUploadTransportErrorHidesSessionURI` (the server drops the connection mid-chunk) and `TestCancelledUploadErrorHidesSessionURI`.
- **Low findings: not addressed in this run**, which was scoped to the Critical, High and Medium findings. These are the `__proto__` reason on the Settings page, channel thumbnails blocked by the CSP, the JSON 401 from the callback without a session, and the unrevoked grant after concurrent reconnects. The fifth Low finding says that the "What shipped" section overstates the upload's dedupe. It is correct: `Client.Upload` returns `upload_session_gone`, and the caller must dedupe by the nonce tag with `FindUploadByTag`. The second part's pipeline step has to do that.

After the fixes, the host run passes again: `go vet ./...` (also with `-tags=integration`), `golangci-lint run ./...` with 0 issues (also with the integration build tag), `go test ./... -count=1` and `scripts/lint-tenant-queries.sh`. No generated code or web file changed. The toolbox run with `-race`, the integration suite and the Playwright spec are still pending, because Docker is still down.

## Merge notes

- `scripts/lint-tenant-queries.sh` `TENANT_TABLES` is edited by lanes a, c and d. The merge takes the union.
- `openapi/root.yaml`, `api/cmd/api/*`, `deploy/compose.yml`, `.env.example` and the web route tree were changed as announced on the board. Regenerate the generated code after merging; never hand-merge it.

- Merged into main after main (with the review follow-up fixes and the scoring engines) was merged into the branch. Two conflicts: the generated `server.gen.go` was regenerated with the toolbox, and `TENANT_TABLES` took the union. The toolbox lint and test, the web checks and the integration suite were re-run green on the merged tree before the merge into main.

## Unresolved questions

- The migration version 20260927300000 sorts before 20260927400000, which main already had. Fresh databases apply both in order, but a development database that already applied the later version needs goose's allow-missing mode (or a reset) to pick this one up. Is that acceptable for the MVP?
- Which Google Cloud project and OAuth consent screen (testing or published) will the live check use? In testing mode refresh tokens expire after 7 days, and the UI then shows the reconnect state.
