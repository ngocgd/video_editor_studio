# Phase 10: Review queue and publish to YouTube

## Context links
- [plan.md](plan.md) · [contract §2 AC6, §2 constraints (official API only), §7 (private lock until audit), §1 decision 6 (review + beta auto-upload)](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- [market/policy research §5–6 (quota, publishAt, private-lock, disclosure)](../reports/researcher-260924-2128-market-channels-copyright-policy.md) · [TTS research §5 (disclosure, inauthentic content)](../reports/researcher-260924-2145-narration-voice-tts-channels.md)
- Wireframe: [review-publish](../../docs/wireframe/review-publish.html) · [design-guidelines §7 publish state honesty](../../docs/design-guidelines.md)
- Depends on phase 8 (renders, manifests, QC, timeline), phase 9a (Qwen-Image thumbnails) and phase 9c (image scores).

## Overview
- Priority: P1 · Status: pending · Effort: 24h <!-- RT#15 re-estimate -->
- This phase delivers per-channel Google OAuth with encrypted tokens and a human review gate with the wireframe's 8 pre-publish checks. It also delivers a crash-resumable, **idempotent** upload (never a duplicate public video) through the official Data API (private or scheduled) with thumbnail, metadata, chapters and the synthetic-content flag, plus quota tracking and honest audit-state UI. An optional beta auto-upload runs after approval.

## Requirements
- OAuth:
  - Authorization code flow with PKCE, `state` bound to the session (single use, 10 min), `access_type=offline`, `prompt=consent`.
  - Scopes: `youtube.upload`, `youtube.readonly`, `yt-analytics.readonly`, requested once (phase 11 needs the last one).
  - The refresh token is stored through `envelope` (kind `youtube_refresh`). Disconnect revokes the token at Google and deletes the secret. Connect and disconnect are audited.
- `youtube_channels`: channel id, title, thumbnail, secret ref, scopes, `api_project_audited` (manual toggle with an audit form date and note), `long_uploads_status`, `custom_thumbnails_ok`, status.
- **Channel eligibility precheck** <!-- RT#3 -->: on connect and before each publish, `channels.list part=status` reads `longUploadsStatus`. Episodes >15 min require `allowed`; otherwise publish is blocked with "Verify your channel to upload videos longer than 15 minutes" and a link. Custom thumbnails also need a verified channel: a `403` from `thumbnails.set` is **permanent** (`thumbnail_not_permitted`), sets `custom_thumbnails_ok=false`, and the UI says why; it is never retried.
- Publications:
  - `publications(render_id, channel_id, title ≤100, description ≤5000 bytes, tags ≤500 chars total, category, default language, thumbnail asset, visibility private|scheduled|public, publish_at, contains_synthetic_media default true, made_for_kids false, status draft|awaiting_review|approved|uploading|processing|uploaded|failed, youtube_video_id, upload_session secret ref, bytes_sent, upload_nonce, approved_render_sha256)` <!-- RT#3 RT#14 -->
  - `review_decisions`
  - `quota_ledger(project, pt_date, bucket, units)`
- Visibility honesty: while `api_project_audited=false`, Scheduled and Public are **disabled with the reason in text** and the result is labelled "Private (API not audited)" with a lock icon. Once audited, `scheduled` uploads as private with `status.publishAt`.
- Pre-publish checks (the wireframe's 8) are computed from `render.QCReport` and the data:
  1. subtitles aligned (max drift ≤0.3s)
  2. loudness within ±1 LU of −14
  3. all scenes rendered, no placeholders
  4. character consistency ≥0.8 (flags low-score scenes with jump links)
  5. synthetic-content flag set
  6. ≥3 random spot-check segments watched (tracked from player events)
  7. title and thumbnail differ from the last 5 publications (normalised title similarity <0.8 and perceptual hash distance above a threshold; supports the inauthentic-content policy)
  8. human approval recorded; approval stores the render's sha256 from the phase 8 report (`approved_render_sha256`) <!-- RT#14 -->
  - Publishing is blocked until all checks pass or the owner explicitly overrides with a reason (audited).
- Upload (idempotent) <!-- RT#3 RT#8 -->:
  - Start: a CAS `UPDATE publications SET status='uploading' WHERE id=$1 AND status='approved'` — zero rows means another run owns it, and the step exits. A random `upload_nonce` is generated once per publication and embedded in the metadata (a tag `lt-<nonce>` and a trailing description line).
  - The `publish.upload` step (queue `io`) opens a resumable session (`videos.insert`, `uploadType=resumable`) and **persists the session URI (encrypted, it is a capability URL) before sending the first byte**.
  - Bytes: 16MB chunks (multiples of 256KB), each read with a **per-chunk ranged `GetObject` through `storage.Internal`** (no long-lived presigned stream, no temp file). A running sha256 over the streamed bytes is compared with `approved_render_sha256` **before the final chunk is sent**; a mismatch aborts the session and fails the step (`render_changed_since_approval`).
  - Resume: query the session with `Content-Range: bytes */N`. `308` → resume from the reported offset. `200/201` → the upload completed; take the video id from the body. `404/410` (session gone) → before opening a new session, **dedupe**: list the channel's uploads playlist (`playlistItems.list`, most recent 50) and `videos.list` their tags for `lt-<nonce>`; if found, adopt that video id; otherwise open a new session.
  - Then `thumbnails.set` (JPEG ≤2MB, 1280×720, converted through the phase 7 FFmpeg runner) unless `custom_thumbnails_ok=false`, then poll `processingDetails`.
- Quota <!-- RT#3 -->: pre-check before each call against a **conservative ledger until live-verified**: `videos.insert` 1,600 units, `thumbnails.set` and `videos.update` 50, `playlistItems.list`/`videos.list`/`channels.list` 1, all from the 10,000/day pool (≈5 uploads/day). The research's lower insert cost is not trusted until the live check in step 2 confirms it against the Cloud console quota page; the ledger values are config. Buckets reset at Pacific-time midnight. When over quota, the step snoozes until reset and the UI shows it.
- Metadata helpers: auto chapters in the description from `render.Timeline` (`00:00 …` with ≥3 chapters and ≥10s each), LLM-suggested title and description variants as a `DiffProposal`-style choice (never auto-applied), and a thumbnail generated from Qwen-Image (phase 9a workflow) or uploaded.
- **Public-metadata guard** <!-- RT#12 -->: suggestions are generated from tainted story content, so any URL, @handle, email or phone number in a suggested title, description or tag is highlighted and must be confirmed individually before the metadata can be saved; unconfirmed items are stripped. The final metadata is validated again at upload time.
- The Review Queue page lists renders awaiting review and gives a review player (the timeline component from phase 7 plus spot-check markers). "Send back to storyboard" appears per the wireframe.
- Beta auto-upload: a tenant setting that enqueues `publish.upload` automatically **after approval** (it never skips review).

## Architecture
A render moves from `awaiting_review` through the checks engine and human approve to `approved`. The upload step then runs the insert, the thumbnail and processing polling, reaching `uploaded`. Every Google call goes through `api/internal/youtube` (an `oauth2.TokenSource` with persisted refresh, a netguard client and typed errors for quota, auth and transient failures), so there is one client used by the phase 10 and phase 11 steps. The official Go client `google.golang.org/api/youtube/v3` is used for metadata, and a manual resumable upload loop is used for control over chunks, resume and dedupe.

## Related files
- Create:
  - `db/migrations/*_publish.sql`, `db/queries/{channels,publications,quota}.sql`
  - `api/internal/{youtube,publish,review,oauthgoogle}/`
  - `openapi/paths/{channels,publications,review}.yaml`, `openapi/schemas/publish.yaml`
  - `web/src/features/{review,publish,settings-youtube}/`
  - `web/src/routes/_app/review/**`, `web/src/routes/_app/projects/$seriesId/publish/**`, `web/src/routes/_app/settings/youtube.tsx`
- Modify: `openapi/root.yaml`, `.env.example` (`GOOGLE_CLIENT_ID`, redirect URL; the client secret goes in a secrets file).

## Implementation steps
1. Write the Google OAuth connect and callback (PKCE and state), token storage, revoke, and the channel fetch (`channels.list mine=true`).
2. Write the youtube client wrapper, error taxonomy (incl. permanent `thumbnail_not_permitted`, `long_uploads_not_allowed`), the conservative quota ledger and PT reset logic, and the channel eligibility precheck. Live-verify the `videos.insert` quota cost once and record it (ledger stays conservative until then).
3. Write the checks engine (pure functions over QCReport, scores and history) and the review decisions.
4. Write the idempotent upload step (CAS, nonce, session URI persisted first, per-chunk range GET, running sha256, 200/201/308/404/410 handling, uploads-playlist dedupe), then the thumbnail set and processing poll, including the synthetic flag and visibility logic.
5. Write the metadata helpers (chapters, LLM title and description variants with the URL/handle confirmation guard, thumbnail conversion).
6. Build the Review Queue and review player UI, and the Publish page per the wireframe (audit banner, disabled options with reason, checks list).
7. Build the Settings > YouTube channel page (connect, audit toggle and note, scopes shown) and the auto-upload beta toggle.

## Todo checklist
- [ ] OAuth PKCE + encrypted tokens + revoke
- [ ] YouTube client + quota ledger
- [ ] Pre-publish checks engine + review gate
- [ ] Idempotent resumable upload (CAS, nonce, dedupe, hash check) + thumbnail + processing poll
- [ ] Channel eligibility precheck
- [ ] Metadata helpers
- [ ] Review Queue + Publish UI
- [ ] Channel settings + beta auto-upload

## Performance budget checks
- Upload memory is ≤64MB (one chunk buffer), with no temp files. Throughput is limited only by the uplink. Each chunk read is a fresh ranged GET, so a multi-hour upload never depends on an expiring URL.
- The checks are computed in ≤200ms from stored reports, with no re-probing of media.
- Review player seeks within the presigned MP4 in ≤300ms (range requests, `faststart`).

## Security checklist
- [ ] PKCE + single-use state bound to session; redirect URI exact-match; tokens never sent to browser
- [ ] Refresh token and upload session URI encrypted at rest; redacted in logs
- [ ] Publish/approve/override/connect/disconnect audited; owner-only override with reason
- [ ] Only official Data API endpoints; no browser automation or captcha bypass (contract non-goal)
- [ ] Title/description/tags validated (length, no control characters); LLM suggestions require explicit choice; URLs/handles in suggestions confirmed individually
- [ ] Uploaded bytes hash-match the approved render; publication CAS prevents concurrent uploads

## Reuse points
- Reuse `envelope`, `audit`, `netguard`, `pipeline` steps (with snooze on quota), `media/ffmpeg` (thumbnail conversion), `render.QCReport` and `render.Timeline`, the ComfyUI thumbnail workflow, and `Timeline`, `DiffProposal`, `StatusChip` and `InlineError`.
- Create `youtube.Client` (reused by phase 11 analytics) and `review.Checks` (extensible list).

## Tests
- `scripts/tb.ps1 test` covers the checks engine table tests, quota PT-date math, chapter builder rules, and the metadata validation.
- `scripts/tb.ps1 test-integration` covers OAuth and the upload against an `httptest` Google double in `_test.go`: a chunked upload, a kill mid-upload followed by resume from the reported offset, a crash after the final chunk (`200` on query → no second video), a `410` session with the video already present (dedupe adopts it), two concurrent upload steps (CAS lets one run), a render swapped after approval (hash mismatch aborts before the final chunk), quota-exceeded snooze, a transient thumbnail failure retry and a `403` thumbnail marked permanent.
- Manual live test (a checklist in the phase report) against a real test channel: connect, eligibility shown, upload a 60s render as private with thumbnail, chapters and `containsSyntheticMedia=true`, verify in YouTube Studio, and record the observed quota usage.

## Success criteria
- A rendered episode passes review and uploads to YouTube as **private**, with thumbnail, title, description, tags, chapters and the synthetic-content flag, verified in Studio (AC6).
- When `api_project_audited=true`, a scheduled upload sets `publishAt`. Otherwise the option is disabled with the reason shown.
- A killed upload resumes without restarting from byte 0, and no crash point produces a duplicate video.

## Risks + rollback
- The quota or policy numbers from the research are unverified (contract §7) (Medium×Medium): the ledger starts at the conservative 1,600-unit insert cost and is lowered only after the live check.
- Duplicate uploads after a crash (Low×High): session URI persisted first, `200/201` handling, nonce dedupe, publication CAS.
- Channel not verified (Medium×Medium): eligibility precheck blocks >15-min uploads with a clear reason; thumbnails degrade to the auto-generated frame with the reason shown.
- The OAuth app is in Google "testing" mode, so refresh tokens expire after 7 days (High×Medium). Document publishing the OAuth consent screen, and have the UI show reconnect-needed state.
- Upload rejected for policy reasons: the status is surfaced verbatim from the API.
- Rollback: channels and publications tables and routes. Revert the PR, and revoke the test channel tokens.

## Next steps
Phase 11 reads analytics through the same channel tokens.
