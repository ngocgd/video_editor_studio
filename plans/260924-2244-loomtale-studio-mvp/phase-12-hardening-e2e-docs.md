# Phase 12: Hardening, E2E, performance and security review, acceptance runs, docs

## Context links
- [plan.md](plan.md) (the AC → phase map) · [contract §2 acceptance criteria, §11 NFRs](../reports/brainstorm-260924-2128-story-video-studio-contract.md)
- [design-guidelines §9 accessibility](../../docs/design-guidelines.md) · [tech-stack](../../docs/tech-stack.md)
- Depends on phases 1–11 (including 1b and 9a–9c).

## Overview
- Priority: P1 · Status: pending · Effort: 16h of engineering, plus machine time for the acceptance renders <!-- RT#15 re-estimate -->
- This phase proves the contract end to end on the real machine. It runs the eight acceptance criteria with recorded evidence, Playwright E2E, perf traces against the §11 budgets, API load tests, and a security review with automated scans and an authz matrix. It also updates the evergreen docs.

## Requirements
- **Acceptance runs** (evidence stored in `plans/reports/acceptance-<date>.md`, with MP4 links, screenshots and step logs):
  - AC1: an EN series from settings produces an episode ≥30 min rendered 1080p with voice, aligned subtitles and ≥2 recurring characters. Consistency score ≥0.8 is required, and a human sign-off confirms the characters are recognisable.
  - AC2: the same in Vietnamese.
  - AC3: an imported chapter (text) is taken through translate and the pipeline into a video.
  - AC4: edit one scene and confirm only its segments re-render; edit a scene **during** a render and confirm the run is superseded and the new render reuses unchanged segments. Kill the worker and the API mid-render (`docker compose kill`), restart them, and the run completes with no step executed twice. <!-- RT#1 RT#9 -->
  - AC5: the UI shows live job progress and the GPU queue during the AC1 run (screen recording), including after a forced SSE `resync`.
  - AC6: a private upload with thumbnail, metadata, chapters and the synthetic flag is verified in Studio; a worker kill after the final chunk yields exactly one video. <!-- RT#3 -->
  - AC7: Analytics shows views, watch time, **CTR per video (Reporting API reach report, ≈2-day lag)** and retention. Evidence may use an existing public video tracked on the channel. <!-- RT#3 -->
  - AC8: starting from the seeded Ollama default, switch Ollama ↔ Claude CLI ↔ Gemini in Settings, and the next AI action uses it with no restart. <!-- RT#7 -->
- **Playwright E2E** (`web/e2e/`):
  - auth and RBAC
  - writer AI action accept and reject (tag `@claudecli`)
  - storyboard: filter, regenerate, takes
  - render settings and a start
  - review gate blocking publish until the checks pass
  - settings pages
  - CSP violation listener (the test fails on any `securitypolicyviolation`)
  - an axe scan on every route
- **Perf traces** (Playwright with Chrome tracing and web-vitals):
  - 300-scene grid scroll and a 3h timeline pan at 60fps (≤5% dropped frames)
  - INP <100ms for the main interactions (select scene, accept proposal, open inspector)
  - writer typing ≤16ms on 12k words
  - Dashboard LCP ≤1.5s
- **API load (k6):** list endpoints p95 ≤150ms at 50 rps, 200 concurrent SSE streams stable for 10 min with ≤200MB API RSS, and login rate limit enforcement.
- **Throughput and resources:** measure the machine hours per finished hour of the AC1 episode against the contract §3 estimate (≤3.5h per 1h), record the per-stage breakdown, and record peak VM RAM, swap and per-service RSS against the phase 1 `mem_limit` table (no container OOM-killed). <!-- RT#4 -->
- **Backup restore drill** <!-- RT#10 -->: restore the latest nightly `pg_dump` and bucket mirror from `${BACKUP_TARGET}` into a scratch compose project, run `loomtale migrate --check`, and open one episode and one render. Record the restore time.
- **Security review:**
  - OWASP ZAP **authenticated API scan** (`zap-api-scan.py -f openapi` against the bundled spec) over TLS at `https://studio.localhost` (Caddy internal CA), logged in as an editor through a ZAP auth script (session cookie + CSRF header), plus the baseline scan of the SPA <!-- RT#14 -->
  - an authz matrix test (each role × each operation from the spec, generated from `x-min-role`)
  - a tenant isolation sweep (tenant B cannot reach any tenant A resource ID across all GET routes)
  - a log secret scan (planted canaries)
  - containers non-root (`docker inspect`)
  - govulncheck, npm audit, pip-audit and gitleaks all clean
  - the canary injection corpus re-run through the `llm-cli` sidecar and Ollama, including an import-derived summary path; the sidecar's network isolation re-checked <!-- RT#11 RT#12 -->
  - CSP: the enforcing header is present on every route and no Report-Only header exists (CI check from phase 2) <!-- RT#14 -->
  - audit log immutability (UPDATE/DELETE/TRUNCATE fail), step-log access editor+ only, capability-URL log canary <!-- RT#14 -->
  - a review of the dependency and licence list (`go-licenses`, `license-checker`) for commercial compatibility
- **Docs** (update the smallest owning surface, verify against source): `docs/system-architecture.md` (components, data flow, queues, security model), `docs/deployment-guide.md` (WSL2 `.wslconfig` 20GB/12 CPU and `wsl --shutdown`, memory budget, GPU, secrets, KEK backup, nightly backups and the restore drill, OAuth app setup and channel verification, `claude setup-token` for the llm-cli sidecar, Reporting API job, model pulls), `docs/codebase-summary.md`, `docs/code-standards.md` (tenant query rule, codegen rule, reuse library rule), `docs/project-roadmap.md` (SaaS phase items), and `docs/tech-stack.md` (final model choices from the phase 9c sign-off, Ollama as the default LLM, and the compose file names corrected to `deploy/compose.yml` / `deploy/compose.gpu.yml` — the doc currently says `docker-compose.yml` / `docker-compose.gpu.yml`). <!-- RT#15 --> The root `README.md` gets the quick start.

## Architecture
There is no new runtime architecture. This phase adds the test harnesses `web/e2e/**`, `tests/load/*.js` (k6) and `tests/security/` (ZAP config, authz matrix generator in Go test form). CI gets a nightly workflow for E2E and load against the compose stack (CPU mode). The GPU acceptance runs are manual and recorded.

## Related files
- Create: `web/e2e/**`, `web/playwright.config.ts`, `tests/load/*.js`, `tests/security/**`, `api/internal/httpapi/authz_matrix_test.go`, `.github/workflows/nightly.yml`, `docs/{system-architecture,deployment-guide,codebase-summary,code-standards,project-roadmap}.md`.
- Modify: `deploy/caddy/Caddyfile` (a `studio.localhost` site with Caddy internal TLS on 127.0.0.1:8443 for the TLS scan and HSTS check), `docs/tech-stack.md`, `README.md`, and bug fixes in the owning packages as found (each fix with a regression test).

## Implementation steps
1. Write the Playwright setup (seeded tenant fixture through the CLI, auth storage state) and the E2E suites.
2. Write the perf traces with budget assertions, and a seeded 300-scene or 3h episode fixture generator (CLI `loomtale seed perf`, test data only).
3. Write the k6 scripts and the nightly workflow.
4. Run the security sweep: authenticated ZAP over TLS, the authz matrix, tenant sweep, log canary scan, container checks and the licence report. Fix the findings.
5. Run the backup restore drill.
6. Run acceptance AC1–AC8 on the real GPU machine and record the evidence, throughput and resource numbers.
7. Update the docs and verify every command and path in the docs by executing or grepping it.

## Todo checklist
- [ ] E2E suites green
- [ ] Perf traces within budgets
- [ ] k6 load within budgets
- [ ] Security sweep clean (findings fixed or documented non-issues)
- [ ] Backup restore drill recorded
- [ ] AC1–AC8 evidence recorded
- [ ] Throughput measured vs contract estimate
- [ ] Docs updated and verified

## Performance budget checks
- The contract §11 budgets are the gate: initial JS ≤200KB gzip, interactions <100ms, 300-scene grid and 3h timeline at 60fps, no API byte proxying (asserted by a log and trace check), cursor pagination everywhere (the spec lint forbids `offset` params).
- A regression in any budget fails the nightly run, and trends are stored as CI artifacts.

## Security checklist
- [ ] ZAP authenticated API scan (TLS) + baseline: no High/Medium unresolved
- [ ] Authz matrix and tenant sweep 100% pass
- [ ] CSP with no violations across E2E; HSTS verified when served over HTTPS
- [ ] No secrets in logs, images, repo history (gitleaks full-history scan)
- [ ] All containers non-root; only 127.0.0.1 ports published
- [ ] Dependency and model licences compatible with monetised use (report attached)

## Reuse points
- Reuse the seed CLI, the generated API client in E2E (typed fixtures) and the `x-min-role` spec extension for matrix generation (single source), and the phase 9a `bench` for throughput.
- Create the perf fixture generator and matrix generator, which are reused for SaaS regression later.

## Tests
- `cd web && npx playwright test`
- `k6 run tests/load/api-lists.js` and `k6 run tests/load/sse-streams.js`
- `scripts/tb.ps1 test-integration -run TestAuthzMatrix|TestTenantSweep`
- `docker run --rm -v ${PWD}/tests/security:/zap/wrk ghcr.io/zaproxy/zaproxy:stable zap-api-scan.py -t https://studio.localhost/api/v1/openapi.json -f openapi -c zap.conf` with the auth script `tests/security/zap-auth.js` and the Caddy root CA mounted
- `scripts/tb.ps1 ci`, which runs the full gate.

## Success criteria
- All eight acceptance criteria have recorded evidence in the acceptance report.
- The nightly E2E, perf and load runs are green, and the security sweep has no open High/Medium findings.
- The docs describe the as-built system, and every documented command runs.

## Risks + rollback
- Acceptance AC1/AC2 quality (voice naturalness, character consistency) falls short (Medium×High). Iterate on the model and preset choices using the phase 9c benchmark data, with the paid-provider fallbacks as a user decision.
- Perf budget misses late in the project (Medium×Medium). The budgets were enforced from phase 5 onwards, so misses here should be small. Fix the specific hot spot with a trace.
- Rollback: this phase adds tests and docs only. Code fixes ship as individual PRs with regression tests.

## Next steps
The user decides whether to go public: submit the YouTube API audit form, publish the OAuth consent screen, and start planning the SaaS phase (billing, remote GPU workers, RLS).
