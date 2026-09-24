# Phase 05: Frontend foundation, tokens, shared components, app shell

## Context links
- [plan.md](plan.md) · [design-guidelines (tokens §2–4, shell §6, components §7, keys §8, a11y §9)](../../docs/design-guidelines.md)
- Wireframes: [app-shell-dashboard](../../docs/wireframe/app-shell-dashboard.html), [render-queue](../../docs/wireframe/render-queue.html) (job table part)
- [contract §11 performance and reuse](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- Depends on phase 2 (auth API, generated client). Step 7 depends on phase 3's `/events`, `/jobs` and `/gpu` schemas. Owns `web/**` only.

## Overview
- Priority: P1 · Status: pending · Effort: 18h <!-- RT#15 re-estimate -->
- This phase delivers the single UI library and app shell that every screen reuses, the design tokens taken exactly from the guidelines, the generated API client with CSRF handling, a batched SSE-to-query-cache bridge, and CI-enforced performance budgets. The first real screens are Login, Dashboard (jobs plus GPU queue) and the global Render Queue job table.

## Requirements
- Stack: Vite 7, React 19, TS strict, Tailwind v4 (`@tailwindcss/vite`), shadcn/ui (Radix) generated into `web/src/components/ui`, TanStack Router (file routes, `autoCodeSplitting`), TanStack Query, TanStack Virtual, lucide-react, cmdk.
- Tokens: the `:root` CSS vars and the `@theme inline` mapping are copied from design-guidelines §2.4, and `[data-theme="light"]` is reserved. Fonts are self-hosted with `@fontsource` (IBM Plex Sans 400/500/600, Plex Mono 400/500, Literata variable), using **latin + vietnamese subsets only**, and `font-display: swap`. There are no Google Fonts requests, so the CSP stays `font-src 'self'`.
- API client: `web/src/api/gen` from hey-api (fetch client + Zod + TanStack Query options). One `apiClient` interceptor adds `X-CSRF-Token`, redirects to login on 401 while preserving the `redirect` search param, and maps problem+json to a typed `ApiError`.
- SSE bridge <!-- RT#2 -->:
  - **One `EventSource` per browser, not per tab:** a leader tab elected with the Web Locks API (`navigator.locks.request('lt-sse')`) owns the stream and fans events out over a `BroadcastChannel('lt-events')`; followers send their topic sets to the leader. When the leader closes, the lock passes to another tab, which reconnects. This keeps a multi-tab user under the server's per-user cap of 6.
  - Topic multiplexing through `?topics=`, rebuilt when the union of topics changes (debounced 300ms).
  - **Subscribe before snapshot:** queries for SSE-backed entities fetch only after the stream's `ready` event. Each cached entity stores its `version`; events with `version` ≤ the cached version are discarded.
  - Events are buffered and flushed once per `requestAnimationFrame` into `queryClient.setQueryData` on per-entity keys (`['step', id]`, `['gpu']`), so there is no page refetch. State transitions and terminal events are always applied (never coalesced client-side).
  - On `resync`, `error` or reconnect it invalidates only the active snapshot queries and refetches them after the next `ready`.
- Shared components (the reuse library):
  - `AppShell` (nav 224/56 rail, top bar 44, resizable inspector 320–480, status bar 28)
  - `StatusChip` and `PipelinePips` (icon + word, never colour-only; states done/running/queued/failed/stale/none; worst-state rollup)
  - `JobProgress` (4px bar, ETA mono, "Waiting for GPU slot (#n)", failed with Retry step / View log)
  - `GpuStatusBar`
  - `SceneCard` (16:9 `<picture>` with AVIF/WebP `srcset` from asset variants, lazy, sized to avoid CLS; index, timecode, 2-line clamp, pips, monograms)
  - `InspectorPanel` and `InspectorSection`
  - `VirtualGrid` and `VirtualTable` (TanStack Virtual wrappers with roving tabindex)
  - `EmptyState`, `InlineError` (cause + mono log excerpt + Retry/Open log), `Kbd`, `CommandPalette`, `DiffProposal` (used in phase 6), `SpeakerMonogram`
- Keyboard: a global shortcut registry (`useShortcut(scope, keys, handler)`) with a `?` sheet, `Ctrl K`, `Ctrl \`, `Ctrl .`, and G-then-D/P/R/S navigation.
- **CSP compatibility** <!-- RT#14 -->: the build emits no inline scripts. A Trusted Types `default` policy (named `default`, plus `dompurify`) sanitises any HTML sink through DOMPurify with `RETURN_TRUSTED_TYPE`; it exists only for third-party code paths (for example ProseMirror clipboard parsing in phase 6). App code never uses HTML sinks.
- System health: the status bar shows a warning when `/readyz` detail reports the last backup older than 36h or failed (phase 2 backup service). <!-- RT#10 -->
- Accessibility: landmarks, focus ring `2px var(--ring)`, `prefers-reduced-motion` handling, and `footer[role=status][aria-live=polite]`.

## Architecture
```
web/src/
  api/gen (generated) · api/client.ts · api/sse-bridge.ts · api/sse-leader.ts · lib/trusted-types.ts
  styles/tokens.css · styles/fonts.css · styles/app.css
  components/ui (shadcn) · components/shared/* (library above)
  features/{auth,dashboard,jobs}/ (hooks + views)
  routes/ (file routes: __root, login, _app/{index,jobs,settings/account})
  lib/{shortcuts,format(timecode,duration,bytes),virtual}
```
Data flow: a route loader calls `queryClient.ensureQueryData` (generated options). Components read with `useQuery`. The SSE bridge patches the entity caches, and the entity components re-render in isolation because each list row subscribes to its own entity key through a `select`.

## Related files
- Create: everything under `web/src/` above, `web/components.json`, `web/.size-limit.cjs`, `web/vitest.setup.ts`, `web/src/routes/login.tsx`, `web/src/routes/_app/{index,jobs}.tsx`, `web/src/routes/_app/settings/account.tsx`.
- Modify: `web/package.json`, `web/vite.config.ts`, `deploy/caddy/Caddyfile` (SPA fallback, immutable cache for `/assets/*`).

## Implementation steps
1. Set up Tailwind v4, the tokens, self-hosted fonts and shadcn init, then add only the used primitives (button, input, dialog, popover, tooltip, dropdown-menu, tabs, select, switch, resizable, sheet, scroll-area, sonner).
2. Build the router with `autoCodeSplitting`, an auth guard (`beforeLoad` → `/me`), and the Login page (Zod form).
3. Write the API client interceptors, the `ApiError`, and the query defaults (`staleTime` 30s, retry 1 for GET, 0 for mutations).
4. Build the shared components with Vitest and Testing Library tests and axe checks.
5. Build the AppShell and status bar, plus the shortcut registry and command palette.
6. Build the virtual helpers with a 1,000-row demo **only inside a test**.
7. Write the SSE bridge with leader election, `ready`/`resync`/version handling (after phase 3 merges) plus the Dashboard (running and queued jobs, GPU queue, failed-with-retry) and the Render Queue job table (filters All/Running/Queued/Failed/Done, cursor pagination, Retry/Cancel).
8. Add perf CI: size-limit budgets, `rollup-plugin-visualizer` report uploaded as an artifact, and a Vitest render-count test for the SSE bridge.

## Todo checklist
- [ ] Tokens + fonts + shadcn primitives
- [ ] Router + auth guard + login
- [ ] Generated client + interceptors
- [ ] Shared component library + tests
- [ ] AppShell, status bar, shortcuts, palette
- [ ] Virtual grid/table helpers
- [ ] SSE bridge (leader tab, ready/resync/version) + Dashboard + Render Queue table
- [ ] Trusted Types default policy + backup warning
- [ ] Budgets in CI

## Performance budget checks
- Initial JS ≤200KB gzip, with the target ≤150KB for the shell. Each route chunk is ≤80KB gzip (writer and storyboard get their own budgets in phases 6 and 7). CSS ≤30KB gzip. Fonts ≤120KB for the first paint subset.
- One SSE event batch leads to ≤1 React commit per frame. A test pushes 500 events in 1s and asserts ≤60 commits and that only the affected rows re-render.
- A test opens 3 tabs (Playwright contexts sharing storage) and asserts exactly one `EventSource` connection; closing the leader re-establishes one within 2s.
- `VirtualTable` with 10k rows mounts ≤60 DOM rows; keyboard navigation <16ms per step (profiled in phase 12 with Playwright).
- LCP of the Dashboard is ≤1.5s on a local prod build, measured with Playwright + web-vitals in phase 12.

## Security checklist
- [ ] ESLint `react/no-danger` = error; no `innerHTML`; user/LLM text rendered as text nodes only
- [ ] CSRF header on every unsafe request; cookies never read by JS (HttpOnly)
- [ ] Zod validation on every form before submit (the generated schemas)
- [ ] No third-party origins (fonts self-hosted); CSP-compatible build (no inline scripts); Trusted Types policy only sanitises, app code has no HTML sinks
- [ ] Media URLs only from API-issued presigned links; no user-supplied URLs in `src`

## Reuse points
- Create the component library listed above. Every later screen composes these, and new screens may not re-implement chips, progress, cards, empty or error states.
- `sse-bridge` is the only realtime path. `format.ts` is the only timecode formatter.

## Tests
- `cd web && npm run typecheck && npm run lint && npm run test` (Vitest, Testing Library, vitest-axe)
- `cd web && npm run build && npx size-limit`
- Manual: `compose up`, log in, start a test run through the API, and watch the Dashboard update live.

## Success criteria
- The Login → Dashboard flow works against the real API. The job progress and GPU queue update live from SSE without page refetches (AC5, UI half), and a forced server `resync` restores correct state.
- Several open tabs share one SSE connection.
- The size-limit CI gate is green and the axe tests are green.
- The shell matches the wireframe layout zones and token values (visual check against `docs/wireframes/app-shell-dashboard.png`).

## Risks + rollback
- shadcn with Tailwind v4 drift (Low×Medium): pin the shadcn CLI version and commit the generated primitives.
- SSE via a proxy buffering (Medium×Medium): Caddy `flush_interval -1` for `/api/v1/events`, verified by a curl `-N` check.
- Web Locks or BroadcastChannel unavailable (Low×Low): fall back to one stream per tab, still bounded by the server's per-user cap with a clear "too many tabs" message.
- Rollback: `web/**` only, so revert the PR.

## Next steps
Phases 6 and 7 build feature screens on this library. Phase 9a adds the model manager page.
