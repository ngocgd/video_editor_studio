# Phase 5 — Frontend foundation, tokens, shared components, app shell

Branch: `feat/frontend-foundation` · Base: `main` @ `39e84fb` · Final sha: `9727bdd4b55e84cbdcd5b56632286c6d0e88965e`
Status: DONE_WITH_CONCERNS (see Deviations)

## Built

- **Tokens/fonts/Tailwind v4**: `web/src/styles/tokens.css` (verbatim from design-guidelines §2.1-2.3), `styles/app.css` (`@theme inline` mapping, focus ring, reduced-motion), `styles/fonts.css` (self-hosted `@fontsource` IBM Plex Sans/Mono latin+vietnamese, Literata variable, no Google Fonts network requests).
- **Router**: TanStack Router file routes with `autoCodeSplitting` (`vite.config.ts`), `__root.tsx`, `login.tsx` (Zod-validated search, `beforeLoad` redirect if already authenticated), `_app.tsx` layout (auth guard via `/auth/me`, CSRF rehydration via `/auth/csrf` after a hard reload), `_app/index.tsx` (Dashboard), `_app/jobs.tsx` (Render Queue), `_app/settings/account.tsx`.
- **API client**: `web/src/api/client.ts` — request interceptor adds `X-CSRF-Token` on unsafe methods, response interceptor redirects to `/login?redirect=` on 401 (except auth endpoints), error interceptor maps `problem+json` to a typed `ApiError`; `queryClient` defaults (`staleTime` 30s, GET retry 1, mutation retry 0).
- **SSE bridge** (`web/src/api/sse-leader.ts`, `sse-bridge.ts`, `sse-cache.ts`, `use-sse-topics.ts`): Web Locks leader election (`navigator.locks.request('lt-sse')`), `BroadcastChannel('lt-events')` fan-out, topic union rebuilt debounced 300ms, `ready`/`resync` handling, per-step version gating (progress-only events ≤ cached version discarded, transitions always applied), events batched into `queryClient.setQueriesData` once per `requestAnimationFrame`, patching both the `listJobs` and `listRunSteps` generated query caches by `_id` predicate (hey-api's actual query-key shape, not a hand-rolled key).
- **Shared component library** (`web/src/components/shared/*`): `StatusChip`/`PipelinePips` (icon+word, never colour-only, worst-state rollup), `JobProgress`, `GpuStatusBar`, `SceneCard`, `InspectorPanel`/`InspectorSection`, `VirtualTable`/`VirtualGrid` (TanStack Virtual, roving tabindex), `EmptyState`, `InlineError`, `Kbd`, `CommandPalette` (cmdk, lazy-loaded), `ShortcutSheet`, `DiffProposal`, `SpeakerMonogram`, `AppShell`, `TopBar`, `StatusBar`.
- **UI primitives** (`web/src/components/ui/*`): Button, Input, Tooltip, Dialog, ScrollArea, DropdownMenu, Toaster (sonner) — the subset actually consumed by Login/Dashboard/Render Queue/Settings/Command palette/Shortcut sheet.
- **Shortcut registry** (`web/src/lib/shortcuts.ts`): scoped `useShortcut(scope, keys, handler)`, `Ctrl K`, `Ctrl \`, `?`, `G`-then-letter sequence with a 600ms window, skipped while typing in a form field.
- **Trusted Types** (`web/src/lib/trusted-types.ts`): installs the CSP `default` policy backed by DOMPurify `RETURN_TRUSTED_TYPE`; app code has no HTML sinks (`react/no-danger` is already `error` in `eslint.config.js`).
- **Backup-freshness warning** (`StatusBar`): reads `/readyz`'s `checks.backup` and shows a warning badge when it is not `"ok"`/`"backup in progress"` — verified live against the real API (`"no backup run yet"` rendered correctly, see screenshot 02).
- **Features**: `features/auth/*` (login form with Zod validation, `useMe`/`useLogin`/`useLogout`/`useSwitchTenant`), `features/jobs/*` (job list + SSE topic subscription, retry/cancel mutations, cursor-paginated Render Queue table with status filters), `features/dashboard/*` (running/queued/failed sections, GPU status).
- **Perf tooling**: `web/scripts/check-bundle-budget.mjs` (gzip-checks every JS chunk individually — the largest/shell chunk ≤200KB, every other route chunk ≤80KB, CSS total ≤30KB) wired as `npm run budget-check`; `rollup-plugin-visualizer` emits `dist/stats.html`.
- **Caddyfile**: added `Cache-Control: public, max-age=31536000, immutable` for `/assets/*` (content-hashed by Vite); SPA fallback and SSE `flush_interval -1` already existed from phase 1/3 and were left unchanged.

## Commands + results

| Command | Result |
|---|---|
| `cd web && npm run typecheck` | pass, 0 errors |
| `cd web && npm run lint` | pass, 0 errors/warnings |
| `cd web && npm test` (vitest + Testing Library + vitest-axe) | pass, 19/19 tests, 4 files |
| `cd web && npm run build` | pass, ~10-12s |
| `cd web && node scripts/check-bundle-budget.mjs` | pass, see Perf numbers |
| `cd web && npm audit` | 0 vulnerabilities |
| `scripts/tb.sh ci` (full toolbox: Go vet/lint/test -race, tenantctx, tenant-query lint, ruff, pytest, gen + gen-check) | **lint/tests all pass**; `gen-check` fails only on a toolbox/worktree git-path artifact (`fatal: not a git repository: /src/C:/Users/.../worktrees/agent-...`), not real drift — confirmed by running the same `git diff --exit-code` on the host directly against the same paths: exit 0, no diff. This is a pre-existing limitation of running the toolbox container from inside a `git worktree` checkout (mangled gitdir path inside the container), unrelated to phase 5's changes. |

## Live E2E smoke (real stack, not mocks)

Brought up `postgres`, `minio`, `minio-init`, `migrate`, `api`, `web` via `docker compose -p loomtale-p5 -f deploy/compose.yml --env-file .env up -d --wait` (secrets generated with `scripts/dev-secrets-init.sh`; `llm-cli`/`egress-proxy`/`worker`/`backup` excluded — `llm-cli` is opt-in per README, `worker` has no step-kind handlers registered anywhere yet at this point in the plan, see Deviations). Created an owner via `loomtale create-owner`. Ran a Playwright script (screenshots in `plans/260924-2244-loomtale-studio-mvp/reports/phase-05-screens/`):

1. `01-login.png` — Login screen against the real API, tokens/fonts rendering correctly.
2. `02-dashboard-queued.png` — real login (`POST /auth/login`, `Set-Cookie: __Host-lt_sess=...; HttpOnly; Secure`), Dashboard loads a pre-seeded queued step, GPU status bar, and the live backup-warning badge (`checks.backup = "no backup run yet"` from the real `/readyz`).
3. `03-dashboard-running-live.png` / `04-dashboard-progress-live.png` — a Postgres `UPDATE pipeline_steps ...; SELECT pg_notify('lt_events', '<step event json>')` (same NOTIFY channel and payload shape `api/internal/sse.Event` uses) was pushed twice; the Dashboard's progress bar and status updated **without any page reload or refetch**, confirming the SSE bridge, leader election and per-frame cache patch actually work end to end against the real `sse.Hub`.
4. `05-render-queue.png` / `06-render-queue-filtered.png` — Render Queue table showing the same step live, status-filter buttons.
5. `07-command-palette.png` — `Ctrl K` opens the lazily-loaded command palette.
6. `08-account-settings.png` — tenant list, active tenant.
7. `09-logged-out.png` — logout redirects to `/login`.
8. CSP header confirmed present and correct on every response (`curl -i`): `script-src 'self'`, `style-src 'self' 'unsafe-inline'`, `require-trusted-types-for 'script'; trusted-types default dompurify`, `object-src 'none'`. No console CSP violations were reported by Playwright during the whole flow; the only console entries were the expected `401`s from in-flight polling requests firing right as the logout redirect happened.

Stack torn down with `docker compose -p loomtale-p5 ... down -v`; confirmed no `loomtale-p5` containers/volumes/networks remain. `.env`/`secrets/*` used for this run are gitignored and were not committed.

## Performance budget checks

- Bundle budget (gzip, from `check-bundle-budget.mjs` against the production build): shell/vendor chunk **140.24 KB** (limit 200 KB; the spec's tighter 150KB "shell target" is met by this chunk alone), every other route chunk ≤18.21 KB (limit 80 KB each), CSS **6.07 KB** (limit 30 KB). The **realistic initial-load total** for the Dashboard route (shell + `_app` layout + shared react-query chunk + `gpu-status-bar` chunk + the index route chunk) is **≈189 KB gzip**, i.e. under the 200KB hard cap but above the 150KB "target for the shell" once the layout/status-bar code is included — flagged in Deviations as a real follow-up.
- SSE batching: `web/src/api/sse-cache.test.ts` pushes 500 progress events synchronously and asserts exactly one `requestAnimationFrame` is scheduled (not 500), and that only the final state (progress 500, version 500) is applied once the frame fires — this is the mechanism the "≤1 React commit per frame" budget depends on; a literal multi-tab/React-commit-count Playwright measurement is deferred (see Deviations).
- `VirtualTable`/`VirtualGrid` render only the visible window via TanStack Virtual; not separately profiled with 10k rows in this session (Playwright is now installed as a `web` devDependency for that follow-up).
- LCP/INP and the 3-tab-single-`EventSource` test are explicitly assigned to phase 12 in the phase 5 spec itself ("profiled in phase 12 with Playwright"); not measured here.

## Accessibility and security checks

- `eslint.config.js` already had `react/no-danger: error`; confirmed no `dangerouslySetInnerHTML`/`innerHTML` anywhere in new code.
- `vitest-axe` checks on `StatusChip` and `JobProgress` pass with zero violations (jsdom canvas/matchMedia/ResizeObserver stubbed in `vitest-setup.ts` so axe's colour-contrast rule can run).
- Landmarks: `nav[aria-label=Primary]`, `main`, `aside[aria-label=Inspector]` (component exists, not yet composed into a screen — no screen in this phase uses the inspector slot), `footer[role=status][aria-live=polite]` for the status bar.
- Focus ring `2px solid var(--ring)` global via `:focus-visible` in `app.css`; `prefers-reduced-motion` disables spin/transition durations globally and specifically the `StatusChip` running-spinner class.
- CSRF header verified on the real `POST /auth/login` → `POST /runs` flow; session cookie is `HttpOnly; Secure` and never read by JS.
- `npm audit`: 0 vulnerabilities.

## Deviations (with reasons)

1. **GPU status is polled, not SSE-patched.** The real `/events` endpoint's `topics` parameter is *pipeline run ids only* (`openapi/paths/events.yaml`, `api/internal/pipelineapi/events.go`) — there is no `"gpu"` topic in the actual API contract, unlike the phase spec's example cache key `['gpu']`. `GpuStatusBar`/`StatusBar` therefore poll `GET /gpu` every 4s via TanStack Query instead. This is a real API-contract gap versus the phase 5 spec text, not something fixable without touching `api/` Go code (out of scope per instructions); flagging for the plan owner to decide whether `/gpu` should become SSE-streamed in a later phase.
2. **"Subscribe before snapshot" is approximate for the Dashboard/Render Queue, not literal.** The SSE topic granularity is per-run-id, but the initial topic set can only be discovered *from* the `/jobs` snapshot itself (there is no "all my tenant's jobs" topic). `useJobs`/`RenderQueueView` therefore fetch the snapshot immediately and subscribe once run ids are known; only the *live-patch* path (not the very first paint) honours the spec's "queries fetch only after ready" rule. This is an inherent consequence of the real API's topic model, documented rather than worked around with a fake topic.
3. **Could not exercise `POST /runs` end to end with a real handler.** `api/cmd/api/main.go` and `api/cmd/worker/main.go` both construct their pipeline engine with an **empty** `pipeline.NewRegistry()` — no step kind is registered anywhere outside test files (`api/internal/integration/*_test.go`) at this point in the plan. `CreateRun`/`Enqueue` synchronously rejects any step `kind` with 400 `"pipeline: no handler registered for step kind"`. This is expected (phases 6-9 are what register real kinds), not a phase 5 bug, but it means the literal instruction "enqueue a run via the API to see progress" cannot succeed against the live API today. Worked around for the live-update proof by inserting the `pipeline_runs`/`pipeline_steps` rows directly via SQL and pushing the same `pg_notify('lt_events', ...)` payload shape the real `sse.Hub` uses — this proves the SSE bridge and cache patch are correct against the real hub/wire format, without touching any Go code.
4. **Multi-tab single-`EventSource` Playwright test not written.** `SseBridge`'s leader-election/BroadcastChannel protocol is implemented and unit-testable in isolation, but a literal "3 Playwright contexts share one connection" test was not added in this session given time constraints; Playwright is now a `web` devDependency and the live smoke test proves the mechanism drives real UI updates, but the specific multi-tab assertion from the phase's performance-budget checklist is deferred.
5. **Initial-load total is close to, not comfortably under, the 150KB "shell target".** See Performance budget checks: the shell chunk alone (140.24KB gzip) is under target, but shell + layout + shared chunks needed for the first authenticated paint total ≈189KB gzip. Recommend a follow-up to split `_app`/`gpu-status-bar`/the shared react-query chunk further (e.g. moving Radix Tooltip out of the eagerly-loaded path) before phases 6/7 add their own route budgets on top.
6. **shadcn primitives are hand-written wrappers, not `shadcn` CLI output.** The CLI needs network access to a component registry and Tailwind v4 support was still maturing; primitives were written directly against Radix + the token classes instead (Button, Input, Tooltip, Dialog, ScrollArea, DropdownMenu, Toaster). Tabs/Select/Switch/Sheet/Resizable/Popover were **not** built since no screen in this phase needs them yet (Render Queue filters use a simple `role=tablist` button group instead of Radix Tabs); phases 6/7/9a should add the remaining primitives when a screen actually needs them, following the same pattern.
7. **`vitest-axe` 0.1.0's own type declarations and its `extend-expect` runtime entry point are both incompatible with vitest 5** (global `Vi.Assertion` namespace vs. vitest 5's `vitest` module augmentation, and a broken `expect` instance resolution). Worked around with a local `web/src/vitest-axe-matchers.d.ts` type augmentation and a direct `expect.extend({ toHaveNoViolations })` in `vitest-setup.ts` instead of the package's own (non-functional) integration point.
8. **`web/vitest.setup.ts` named `vitest-setup.ts`.** The phase spec's "Related files" list names `web/vitest.setup.ts`; phase 1 had already created `web/src/vitest-setup.ts` and wired it into `vite.config.ts`'s `test.setupFiles`. Kept the existing filename/location rather than introducing a duplicate to avoid breaking phase 1's existing wiring.
9. **size-limit's own budget mechanism was replaced** with `web/scripts/check-bundle-budget.mjs`. `size-limit`'s glob-`path` config sums every matched file into one number; since Vite's route chunks are content-hashed (no stable per-route filename to pin a static per-file budget to), size-limit's `dist/assets/*.js` config either double-counts every chunk together (failing immediately, ~192KB vs an 80KB "per route" limit) or can't express "each individual file ≤N" at all with only `@size-limit/file` installed (no bundler-analysis plugin). The custom script checks each chunk's actual gzip size individually via Node's built-in `zlib`, which is what the spec's budget language actually means. `size-limit`/`@size-limit/file` remain installed devDependencies but are unused; recommend removing them in a follow-up cleanup.

## Screenshots

`plans/260924-2244-loomtale-studio-mvp/reports/phase-05-screens/01-login.png` through `09-logged-out.png` (listed above under "Live E2E smoke").

## Unresolved questions

1. Should `GET /gpu` become an SSE topic (or piggyback on a synthetic `"gpu"` topic id) in a later phase, so the GPU status bar can be patched from the stream instead of polled? Current behaviour (poll every 4s) is a reasonable interim but is a deviation from the phase 5 spec's literal `['gpu']` cache-key example.
2. Confirm the intended source of the initial "topics" set for a tenant-wide Dashboard view — right now it is derived from the `GET /jobs` snapshot's own `runId`s, which means the very first paint is not "subscribe before snapshot" the way a single-run detail view can be. Is a tenant-wide topic (e.g. `"*"` or a dedicated dashboard topic) planned for a later phase, or is per-run subscription-after-snapshot the accepted design for list views?
3. Is it acceptable that no step kind is registered in `api/cmd/api/main.go`/`api/cmd/worker/main.go` until phase 6+? If a smoke/demo environment is needed before then, a minimal no-op step kind registration might be worth adding in a later phase's cook, but that is Go code outside phase 5's ownership.

Status: DONE_WITH_CONCERNS
Summary: Core frontend foundation (tokens, router, API client, SSE bridge, shared component library, AppShell, Login/Dashboard/Render Queue/Account screens) is built and verified against a real running stack, including a genuine live SSE update with no page refetch; typecheck/lint/tests/build/budget/audit are all green and the toolbox `make ci` passes except for a worktree-only git-path artifact in `gen-check` (confirmed no real drift on the host).
Concerns: GPU status uses polling instead of SSE (real API has no gpu topic), `POST /runs` cannot be exercised end-to-end yet because no pipeline step kind is registered anywhere before phase 6, the multi-tab single-connection test is not yet written, and the authenticated-route initial bundle (~189KB gzip) is close to the 200KB hard cap.
