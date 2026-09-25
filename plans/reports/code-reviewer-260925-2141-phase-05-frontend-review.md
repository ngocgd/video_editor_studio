# Phase 5 frontend foundation: pre-merge review

Branch `feat/frontend-foundation` @ 4576a67 vs main@39e84fb. 76 files, +6140/-359 (about 2.4k LOC of hand-written TS/TSX/CSS).
Checks I re-ran in the worktree: typecheck pass, lint pass, vitest 19/19 pass, `vite build` pass, `npm audit --audit-level=high` 0 vulns.
**The CI command `npx size-limit` fails (exit 1).** Entry chunk: index 143.6KB gz. First authenticated paint: index + _app 17.0 + gpu-status-bar 18.2 + cn 13.5 + route chunks, about 190KB gz.
Scouted: api/internal/sse/subscriber.go, api/internal/pipelineapi/events.go, api/internal/authapi (CSRF derivation), hey-api `core/serverSentEvents.gen.ts`, TanStack router-core `navigate`.

## Critical

**C1. The web CI job goes red on merge.** `.github/workflows/ci.yml:80` still runs `npx size-limit`, but the branch deleted the `size-limit` config from `web/package.json` and never wired up the replacement. I ran it: `ERROR Create Size Limit config in package.json`, exit 1. The replacement `npm run budget-check` is not in CI. That also breaks the spec's success criterion "size-limit CI gate is green".
Fix: in ci.yml, replace `npx size-limit` with `npm run budget-check`, or restore a config. Also drop the unused `size-limit` and `@size-limit/file` devDeps. **Blocks merge: yes.**

## High

**H1. Live updates stop for good after 1 hour.** `api/internal/sse/subscriber.go:34,152` closes every stream cleanly after `maxStreamLifetime` (1h). The hey-api fetch client `break`s on normal completion and never reconnects (`gen/core/serverSentEvents.gen.ts:223`). `sse-bridge.ts:458-461` exits its loop when the iterator is `done`. `currentTopicsKey` stays set (`:427-428`), so `rebuildStream` treats the stream as live and no-ops forever. The API comment assumes browser `EventSource` auto-reconnect, but this client is fetch-based. The same dead end happens on API graceful shutdown (ShutdownSignal).
Scenario: a user leaves the dashboard open for 61 minutes. The SSE stream is gone and only the 15s poll updates the UI.
Fix: after the drain loop (and in `catch` for non-abort errors), if the controller was not aborted, clear `currentTopicsKey`, broadcast `resync`, and reschedule `rebuildStream` with jittered backoff. **Blocks merge: yes.**

**H2. The topic union has no 50-run cap, so a busy tenant gets no SSE at all.** The server rejects more than 50 topics per stream (`api/internal/pipelineapi/events.go:22,101-103`, returns 403). The bridge sends the union of run ids from every consumer in every tab (`sse-bridge.ts:415-441`). Dashboard alone runs 3 lists × `limit:100` (`use-jobs.ts:17`); the render queue grows with each "Load more". A single run id that was deleted or became invisible also 403s the whole stream.
On 403 the client retries forever (`serverSentEvents.gen.ts:224-234`). Each attempt posts `resync` to every tab (`sse-bridge.ts:450-453`) and adds another one-shot `onReady` listener (`:490`).
Fix: cap and prioritise topics (running/queued first) at ≤50, or shard across ≤2 streams. Treat 4xx as non-retryable and fall back to polling. **Blocks merge: yes** (the feature silently fails at realistic scale).

**H3. Version gating ignores the cached snapshot, and stale transitions can roll back terminal state.** `sse-cache.ts:652-656` compares only against a private `lastAppliedVersion` map. It never looks at the cached item's `version`, and any `transition` bypasses the check. `reset()` (`:661`) clears the map on every resync. The API contract (`openapi/paths/events.yaml:18-21`) says to discard events with version ≤ the snapshot's.
Scenario: a refetch lands with v10 `done`, then a relayed or buffered v8 `running` transition arrives. The row goes back to running at a stale percentage. Out-of-order delivery is realistic during leader handoff or a stream rebuild, where two streams overlap. `sse-cache.test.ts:600-604` locks in this bug: it asserts that v3 `failed` overrides v5.
Fix: in `patchStepCaches`, apply only if `evt.version > item.version`; transitions are exempt only from coalescing, never from ordering. **Blocks merge: yes** (correctness of the core realtime path).

**H4. Background tabs buffer SSE events without limit.** `sse-cache.ts:652-668` queues every event and flushes on `requestAnimationFrame`, but rAF does not run in hidden tabs. Every follower and a hidden leader keeps appending (about 4 events/s per step, times the number of steps) until the tab becomes visible, then does one huge synchronous flush. Nothing is coalesced: 500 events means 500 × 3 `setQueryData` passes (`:672-701`). The test name "applying only the final state" (`sse-cache.test.ts:563`) is misleading; it passes only because the last write wins.
Fix: coalesce `pending` into a `Map<stepId, evt>` (latest by version). Wrap the flush in `notifyManager.batch`. Use `setTimeout` when `document.hidden`. **Blocks merge: no** (fix before phase 6 adds heavier topics).

**H5. The render queue stops updating live after "Load more".** `render-queue-view.tsx:31,48-58` copies query data into local `items` state. When `cursor` is set, only *unseen* ids are merged, so SSE patches to rows already shown never reach the UI. Page 1's query also has no observer any more, and the 15s refetch covers only the last page.
Changing the filter fires one request with the new filter plus the stale cursor (`:36-46`; the effect resets the cursor after render), which may 400.
Fix: use `useInfiniteQuery` (render `pages.flatMap`, patch pages in `sse-cache`) and put the cursor in the query key, reset on filter change. **Blocks merge: no.**

**H6. Every navigation waits on two serial network calls.** `routes/_app.tsx:8-16` `beforeLoad` runs on every navigation into any `_app` child and does `getMe()` then `getCsrf()` serially, outside the query cache. Every nav-rail click blocks on 2 round trips, and `TopBar`'s `useMe` fetches `/auth/me` a third time on cold load.
Fix: `context.queryClient.ensureQueryData(getMeOptions())`, and fetch the CSRF token only when `getCsrfToken()` is null, in parallel. **Blocks merge: no.**

## Medium

- **M1. Caddy caches the SPA fallback as immutable.** `deploy/caddy/Caddyfile:48-52`: the `header` directive runs before `try_files` and matches the *original* path. A missing `/assets/old-hash.js` (a stale tab after a deploy) gets `index.html` back with `max-age=31536000, immutable`, poisoning that URL for a year (it bites on rollback). The comment says index.html "keeps a non-cached response", which is false: with no Cache-Control, browsers apply heuristic caching to index.html, so users can hold a stale shell that points at deleted chunks.
  Fix: `handle /assets/* { header Cache-Control "...immutable"; file_server }` so misses return 404, plus `Cache-Control: no-cache` on the fallback. Add a `vite:preloadError` → `location.reload()` handler. Also stop shipping `dist/stats.html` (`vite.config.ts:12`); it is now served publicly next to the sourcemaps (sourcemaps pre-existing, `:26`).
- **M2. Stream rebuilds lose events, and backoff has no jitter.** Each topic change aborts and reopens the stream (`sse-bridge.ts:430-441`). Events published during the reconnect gap are lost, and a new `ready` does not trigger a snapshot refetch (only the error path does, `:450-453,490`). An abort during connect also throws into `onSseError`, which broadcasts a spurious resync to every tab. Generated backoff: no jitter, and `attempt` never resets after a successful connect (`serverSentEvents.gen.ts:101,107,233`), so a long-lived session reconnects at a 30s floor.
  Fix: treat every `ready` after a rebuild as a resync point. Ignore AbortError. Pass `sseSleepFn` with full jitter, or wrap the reconnect loop in the bridge.
- **M3. Leader protocol gaps.** (a) `unsubscribe` is never posted, so closed tabs' topics stay in the leader's union forever (`sse-bridge.ts:379-384`; nothing sends it). That feeds H2. Post it on `pagehide`. (b) A tab that joins when the union is unchanged never gets `ready` (`:427`), so the documented contract "gate queries on `ready`" (`use-sse-topics.ts:9-12`) would deadlock followers; today no caller gates on it. (c) The documented fallback "every tab runs its own stream" when Web Locks are missing is not implemented; `sse-leader.ts:742-744` just returns and no stream ever opens. (d) Release depends on `beforeunload` (`:749`): a cancelled unload drops leadership with no re-election, and the listener hurts bfcache. The lock auto-releases on unload anyway, so drop the handler.
- **M4. The subscribe-before-snapshot rule (RT#2) is not honoured.** Queries are not gated on `ready` (`use-jobs.ts:16-23`), and 15s polling runs in parallel with SSE (`:18`, `render-queue-view.tsx:40`), contradicting "no page refetch". The implementer disclosed this; it is acceptable only once H3 makes the snapshot version authoritative.
- **M5. No CSRF 403 recovery and no cross-tab auth sync.** `client.ts:239-254` never refreshes the CSRF token on a 403. `switchTenant` rotates the session (`api/internal/authapi/switch_tenant.go:89`), so other tabs keep a dead token and stale tenant data until a 401 forces a full reload. Their `redirect` is then lost, because `login.tsx:13-18` always redirects to `/`.
  Fix: on a 403 with a CSRF problem type, `GET /auth/csrf` and retry once. Broadcast `auth-changed` on the existing channel, and have tabs `queryClient.clear()` and refetch.
- **M6. Screens hide errors and loading.** Dashboard renders the "No jobs are running" empty state while loading *and* on error (`dashboard-view.tsx:30`). Retry and cancel mutation failures are silent (`use-jobs.ts:28-56`; no `onError`, no InlineError). Guidelines §7 require skeletons and inline errors. Cancel has no confirmation.
- **M7. Shortcuts: several advertised keys are dead.** `?` never fires: on a US layout the combo normalises to `shift+?` but the handler registers `"?"` (`shortcuts.ts:36,38-41`; `shortcut-sheet.tsx:20`). `G D/R/S` and `Ctrl .` are listed in the sheet (`shortcut-sheet.tsx:11-13,10`) but never registered anywhere. `pushShortcutScope` is never called, so global shortcuts fire behind open dialogs. Inline handlers re-register on every render (`:99-107`).
- **M8. Accessibility defects.** The collapsed nav rail renders links with no accessible name (`app-shell.tsx:59-60`: icon is `aria-hidden`, label hidden). Wrap the label in `sr-only` and add a tooltip. The status bar sets `role=status aria-live=polite` on content that changes every 4s (`status-bar.tsx:20-23`), so screen readers announce the GPU line continuously; move `aria-live` to a dedicated completion-message node. `VirtualTable` uses `role=table` rows with `aria-selected` and keeps focus on the scroller without `aria-activedescendant` (`virtual-table.tsx:61-82`), so arrow navigation is invisible to assistive tech; use `role=grid` plus activedescendant. Filter buttons use `role=tab` with no tabpanel and no arrow keys (`render-queue-view.tsx:100-114`); use `aria-pressed`. No focus or announce on route change.
- **M9. The budget script checks per-chunk size, not initial load.** `scripts/check-bundle-budget.mjs:139-147` gates only the largest chunk, so first paint could reach about 300KB while it still passes. Budget the sum of the entry plus its static imports, from the Vite manifest.

## Low

- L1. `login-form.tsx:34` passes an unvalidated `redirect` to `navigate({to})`. In this router version it is not an external open redirect (`to` is path-resolved; `//host` would throw in pushState), but validate `^/(?!/|\\)` anyway. The search string from `client.ts:250` breaks the `to` path, and `_app.tsx:11` drops search.
- L2. `client.ts:256-264`: a non-JSON 502 becomes `status:0 "Unknown error"`, which loses the HTTP status. `retry:1` also retries 401/403.
- L3. `app-shell.tsx:82-92`: the palette trigger dispatches a synthetic keydown, coupling it to the shortcut registry. Use a shared open-state store.
- L4. Dashboard `EmptyState actionLabel` has no `onAction`, so the button never renders (`dashboard-view.tsx:31-34`). `GpuStatusBar` click does not open the Render Queue (guidelines §6).
- L5. Unused deps: react-resizable-panels, web-vitals, and @radix-ui select/switch/tabs/popover/label. `sonner` is mounted in the root but `toast()` is never called.

## Bundle easy wins (entry 143.6KB gz)

The entry chunk carries `dompurify` (no consumer until ProseMirror; lazy-load it or install the policy in that phase), `zod` (used only by login: a hand check for `redirect` plus lazy form validation), and `sonner` (lazy `Toaster`). Together that is roughly 25-30KB gz off first paint (measured from the sourcemap, pre-minify). The `gpu-status-bar` chunk (18KB) is mostly Radix Tooltip plus floating-ui on every screen; a lazy tooltip, or a `title` plus a click-to-open popover, removes it. Fonts: add `<link rel=preload>` for Plex Sans latin-400 woff2 to cut FOUT.

## Tests

The suite passes, but it proves little about the risky paths. There is no test of the leader, bridge, or reconnect code. The spec's "3 tabs, 1 connection, re-established within 2s after the leader closes" check is missing. Use pages in *one* Playwright context: separate contexts do not share locks or BroadcastChannel. There is also no render-count test (spec: 500 events → ≤60 commits). `sse-cache.test.ts` asserts the H3 bug. The Playwright smoke is a local script and is not committed or in CI. To add: a vitest for the bridge with a fake `navigator.locks` and BroadcastChannel (handoff, dead-tab unsubscribe, reconnect on `done`, 50-topic cap), plus a committed Playwright multi-page test.

## Design fidelity

Tokens match §2 exactly, and the contrast values I computed pass AA (muted-fg on accent 4.55, input border 3.38). The screens look generic, though: raw `render_episode` and lowercase `running` show as name and step (`dashboard-view.tsx:40-41`). The Dashboard duplicates the status-bar GPU line even though §6 names the status bar as the single source. The copy "Backup no backup run yet" is awkward (`status-bar.tsx:29`). There are no loading skeletons and no Dashboard stat values in the mono `text-xl` style. Map kinds and statuses to human labels (for example "Episode render · Rendering").

## Verdict on deviations

- **GPU polling:** acceptable for the MVP. The API is tenant-scoped (`pipelineapi/gpu.go:35-40`) and query dedupe keeps it to one request per tab every 4s. Later, add a tenant-scoped `gpu` event type on the existing hub (`Event.Type` already anticipates it), delivered via the leader bridge, and keep `/gpu` as the snapshot. Pause polling when `document.hidden`. Not blocking.
- **Bundle near the cap:** real, but the easy wins above give about 30-45KB of headroom. Fix M9 so the gate measures first-paint weight before phases 6/7 add to it.

## Recommended order

C1 → H1 → H2 → H3 (and fix the test) → H4 → H5 → H6 → M1 → M2/M3 → the rest.

## Unresolved questions

1. Is the per-run topic model final for list views, or will the API add a tenant-wide jobs topic? That determines whether H2 is fixed by capping or by an API change.
2. Is a single-tab session over plain HTTP ever supported? `crypto.randomUUID` and `navigator.locks` need a secure context (`sse-bridge.ts:304`), though the `__Host-`/Secure cookie already implies HTTPS or localhost.
3. Should 15s polling stay as a permanent safety net next to SSE, or be removed once H1-H3 are fixed? The spec says "no page refetch".
