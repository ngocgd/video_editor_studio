# Phase 1 verification: repo scaffold, toolbox, CI, codegen

Date 2026-09-25 (Asia/Bangkok). Picked up a mostly-complete scaffold from a prior agent stopped mid-verification. Reviewed all uncommitted diffs, ran the full test matrix, fixed every failure found, and re-verified.

## What existed already
Full tree per phase spec: `api/` (chi, health/readyz, sqlc, goose, oapi-codegen gen), `openapi/`, `db/migrations`, `proto/loomtale/worker/v1` + buf, `workers-python/` (uv, grpc health server), `web/` (Vite/React/TS, hey-api gen client), `deploy/` (compose core/gpu/tools, 4 Dockerfiles, Caddyfile), `scripts/tb.{sh,ps1}`, `.github/workflows/ci.yml`, `Makefile`, `.gitleaks.toml`, `.env.example`, `secrets/.gitkeep`. Uncommitted diffs from the prior agent (kept, all correct): defer-close error handling in 3 Go entrypoints, `go.mod` tool-directive reshuffle, updated golang base image digest + a checksum-pinned golangci-lint download replacing a flaky `install.sh` curl, embed.go comment fix, `workers-python` pinned to `>=3.12,<3.13`/`==3.12.*`.

## Step 0 (host gate)
`docker info --format '{{.MemTotal}} {{.NCPU}}'` → 20972777472 bytes (~19.5GB) / 12 CPU. Already verified before this session per task brief; re-confirmed here. Node 22.23.3 on host. Did not touch `.wslconfig` or restart Docker/WSL.

## Environment quirk found and worked around
This checkout is a git worktree (`C:/Users/ADMIN/orca/workspaces/.../burrfish`, common git dir at `C:/Users/ADMIN/orca/projects/aff-ytb-ntNocj/.git`). The toolbox container only bind-mounts the worktree (`..`), not the common git dir, so `git diff --exit-code` inside `make gen-check` fails with `fatal: not a git repository`. Worked around for verification by running `make gen` (regen only, no git call) inside the toolbox, then diffing on the host, which has full git access. This is a git-worktree artifact of the orchestration environment, not a scaffold defect — a normal clone has `.git` as a real directory inside the repo, which the same bind mount would include, so `make gen-check` runs fine as authored. Did not change the Makefile target.

## Commands run — pass/fail

| Command | Result |
|---|---|
| `docker info` mem/CPU | PASS — 19.5GB / 12 CPU |
| `bash scripts/tb.sh lint test vuln audit` (build image + `golangci-lint`, `go vet`, `ruff`, `eslint`, `go test -race`, `pytest`, `govulncheck`, `pip-audit`, `npm audit --audit-level=high`) | PASS after fixes (see below) |
| `bash scripts/tb.sh gen` then host `git diff` on generated paths (gen-check equivalent) | PASS — no drift beyond intentional regen |
| `bash scripts/tb.sh test-integration RUN=TestProtoRoundTrip` | PASS after fix (see below) |
| `docker compose -f deploy/compose.yml up -d --wait` | PASS after fixes (see below); all 6 containers healthy |
| `curl -fsS http://127.0.0.1:8080/api/v1/healthz` | PASS — `{"status":"ok","version":"dev"}` |
| `curl -fsS http://127.0.0.1:8080/api/v1/readyz` | PASS (after wiring the minio-init-printed app key into the API's env for this run only) — `{"checks":{"database":"ok","storage":"ok"},"status":"ok"}` |
| `docker stats --no-stream` idle | PASS — well under budget (below) |
| `docker compose -f deploy/compose.yml down` | done, containers/network/volumes removed |
| web: `npm ci`(equiv `install`)/`typecheck`/`lint`/`test`/`build`/`size-limit`/`audit` | PASS after fixes (see below) |
| `workers-python`: `uv run pytest -q` | PASS (1 passed) |
| gitleaks: plant fake AWS-shaped key, scan, confirm detection, remove, rescan clean | PASS — `zricethezav/gitleaks:latest dir` found 1 leak (`generic-api-key`, README.md) then 0 after removal |
| `actionlint` on `.github/workflows/ci.yml` | PASS after fix (see below); 0 issues |
| Security checklist (mechanical) | PASS — see below |
| Push to origin / trigger real CI run | Not done, per instructions (no push) |

## Failures found and fixed

1. **`workers-python` `pip-audit` missing** — `uv run pip-audit` errored "Failed to spawn: pip-audit" because it wasn't a project dependency. Added `pip-audit>=2.7.0` to the `dev` dependency group in `workers-python/pyproject.toml`, regenerated `uv.lock`. Result: 0 known vulnerabilities.
2. **Proto round-trip integration test flaky/failing** — `TestProtoRoundTripGoClientToPythonServer` timed out dialing the Python health server. Root cause: the toolbox's `uv` cache and installed-interpreter directories (`/root/.cache/uv`, `/root/.local/share/uv`) were not named volumes, only `workers-python/.venv` was (bind-mounted, thus host-persisted). Every fresh `docker compose run` container re-downloaded the CPython interpreter (~32MB) and found the persisted `.venv` pointing at a now-nonexistent interpreter path from the previous container, forcing a venv rebuild that ate into the test's 10s dial-retry window. Added `uvcache`/`uvpython` named volumes and `UV_LINK_MODE: copy` to `deploy/compose.tools.yml`. Test now passes consistently in ~6-7s total.
3. **`npm audit --audit-level=high` failing (18 vulns: 3 moderate/12 high/3 critical)** — stale devDependency ranges (`@hey-api/openapi-ts@^0.64.1`, `vitest@^2.1.8`, `@size-limit/preset-app@^11.1.6`) pulled vulnerable transitive deps (old `handlebars`, `nanoid`, `tar`, vulnerable `esbuild`/`vite` inside `vitest@2`). Bumped `@hey-api/openapi-ts` to `^0.99.0`, `vitest` to `^5.0.1` (peer-compatible with `vite@^7`). `@size-limit/preset-app` still pulled an unfixed-upstream `extract-zip`/`puppeteer-core` chain (via its bundled `@size-limit/time` load-time check, which this repo's config never uses) even at latest; switched to `@size-limit/file` alone, which only measures gzip size (the only thing `size-limit`'s package.json config actually checks) and drops the puppeteer chain entirely. `@hey-api/json-schema-ref-parser`'s pinned `js-yaml@4.2.0` (in the vulnerable 4.0.0–4.3.1 range) was fixed with an `overrides` pin to `js-yaml@^4.3.2`. Result: `npm audit --audit-level=high` → 0 vulnerabilities.
4. **web build/test broken by the openapi-ts 0.99 bump** — `@hey-api/client-fetch` is now bundled directly into generated code (no longer a separate runtime import) — regenerated the client and removed the now-unneeded direct dependency. `vite.config.ts`'s `test` field failed `tsc` because it imported `defineConfig` from `"vite"` (untyped for `test`) instead of `"vitest/config"` — fixed the import. The web test hung in `isLoading` (React Query default retry ~1s backoff masked it as a timeout): the generated `client.gen.ts` in 0.99 constructs a native `Request(url, init)` object before calling fetch, and Node's `Request`/`URL` (unlike a browser) has no document to resolve a relative URL against, so the default relative `baseUrl: '/api/v1'` threw "Invalid URL" in both Vitest+jsdom and any non-browser context. Added `web/src/api/client-setup.ts` (side-effect import from `app.tsx`) that calls `client.setConfig({ baseUrl: `${window.location.origin}/api/v1` })`, giving `Request()` an absolute URL everywhere while staying same-origin in the browser (works with the Caddy reverse-proxy setup unchanged). Also hit and fixed the unrelated npm/Windows `@rollup/rollup-win32-x64-msvc` optional-dependency bug (documented npm issue) via a clean `node_modules`/`package-lock.json` reinstall.
5. **`postgres` container failed to start under `cap_drop: [ALL]`** — official postgres entrypoint runs as root first to `chown`/`chmod` the mounted data volume, then de-escalates (`gosu`) to the `postgres` user; with all capabilities dropped it got `Operation not permitted` on both the chmod and the user switch. Added `cap_add: [CHOWN, DAC_OVERRIDE, FOWNER, SETUID, SETGID]` to the `postgres` service in `deploy/compose.yml` — the minimum set the entrypoint needs to do that one-time de-escalation; the running server process itself still ends up unprivileged.
6. **`minio-init` failing on `mc admin policy create ... - <<POLICY`** — this `mc` version doesn't support `-` for "read policy JSON from stdin"; it literally tried to open a file named `-`. Fixed `deploy/minio-init.sh` to write the policy JSON to a `mktemp` file and pass the path instead.
7. **`web` (Caddy) container failing `exec /usr/bin/caddy: operation not permitted`** — the upstream caddy image sets the `cap_net_bind_service` file capability on the caddy binary (so it can bind ports <1024 as non-root); combined with `no-new-privileges: true`, the kernel refuses to exec a binary carrying a file capability the process doesn't already have, even though this deployment only ever binds :8080. Stripped the capability at build time in `deploy/docker/web.Dockerfile` (`apk add libcap && setcap -r /usr/bin/caddy && apk del libcap`), keeping `no-new-privileges` intact.
8. **`/api/v1/healthz` and `/api/v1/readyz` through the web proxy returned the SPA's `index.html` instead of being proxied** — two compounding bugs in `deploy/caddy/Caddyfile`: (a) Caddy's automatic directive sort runs `try_files` before `reverse_proxy` regardless of source order, so any path that isn't a static file (including `/api/v1/healthz`) got rewritten to `/index.html` before the proxy matcher ever saw the original path — fixed by wrapping everything in an explicit `route { }` block, which preserves written order; (b) the path matcher `/api/**` in this Caddy version (v2.11.4) only matched a single path segment despite the double-star syntax (verified empirically: `/api/x` and `/api/v1` matched, `/api/v1/healthz` and `/api/a/b` did not) — switched to the documented trailing-single-star greedy form `/api/*`, which correctly matches the full multi-segment `/api/v1/*` path. Confirmed with `caddy adapt` (route order) and live traffic (`/api/v1/healthz` → 200 from the API, not the SPA).
9. **CI workflow `docker` job's mem_limit assertion was broken** — `docker compose config | python3 - <<'PY' ... PY` redirects stdin twice: the heredoc (needed so `python3 -` can read its own script) silently overrides the piped `docker compose config` output, so `sys.stdin` inside the script would read EOF instead of the rendered compose YAML (`cfg` would be `None`, crashing on `.get(...)`). Confirmed via `actionlint` (which runs shellcheck on `run:` blocks and flagged `SC2259: this redirection overrides piped input`) and reproduced locally. Fixed by writing `docker compose config` to a temp file and having the script open that file by path instead of reading stdin. Re-verified locally in a `python:3.12-slim` container against the real rendered compose config — correctly reports all 6 services have `mem_limit`.
10. **`make ci` target didn't run `audit`** — the phase spec's Tests section lists `lint, unit tests, gen-check, vuln and audit`; the Makefile's `ci` target only ran `lint test gen-check vuln`. Added `audit` to the target so `scripts/tb.sh ci` matches the documented contract.

## Measured performance

- `/healthz` p95, 50 requests, host `curl` → web:8080 (Caddy) → api: **10.9ms** (median ~7ms). Measured the same endpoint from inside the `web` container straight to `api:8080` (no host↔WSL NAT hop): consistently **<1ms**, meeting the spec's <5ms budget for the handler itself. The extra ~10ms on the host-side curl path is Windows/Docker-Desktop WSL2 NAT + one extra proxy hop, not API or Caddy processing time; this deviation is specific to this Windows dev host, not the container images.
- `/readyz` p95, 20 requests through Caddy: **5.8ms** (one 26.5ms cold-connection outlier), under the 50ms budget.
- `docker stats --no-stream` idle, all 5 running core services (api, worker, web, minio, postgres): **api 6.6MiB, web 13.2MiB, worker 2.1MiB, minio 405.3MiB, postgres 54.0MiB ≈ 481MiB total**, well under the 3GB budget.
- Second `make gen` (warm toolbox caches): **6.0-6.8s**, under the 30s budget.
- web bundle: `vite build` → `83.71 kB` gzip / `size-limit` → `72.08 kB` brotli, both under the 200KB budget and the 90KB "empty shell" target.

## Security checklist — mechanically verified

- Non-root `USER` in all 3 built runtime images: `docker inspect --format='{{.Config.User}}'` → api `nonroot:nonroot`, worker `nonroot:nonroot`, web `caddyapp`. (`toolbox` intentionally runs as root — dev/CI-only, never shipped, documented in its Dockerfile.)
- All 6 compose services declare `mem_limit` — confirmed by rendering `docker compose config` and checking every service dict (postgres, minio, minio-init, api, worker, web).
- No `docker.sock` mount anywhere in the rendered compose config.
- Only `127.0.0.1:8080` (web) and `127.0.0.1:9000` (minio) are published; confirmed via rendered config's `ports.*.host_ip`.
- MinIO root credentials come from `secrets/minio_root_user.txt` / `secrets/minio_root_password.txt` via compose `secrets:`, consumed only by `minio`/`minio-init`; the API only ever sees the scoped `loomtale-app` key that `minio-init` creates.
- `.env*` and `secrets/*` (except `.gitkeep`) are gitignored; confirmed with `git check-ignore -v`.
- Planted-and-removed fake secret confirmed gitleaks catches real leaks (see table above).

## Files touched this session (beyond the prior agent's kept diffs)

- `Makefile` — added `audit` to the `ci` target.
- `.github/workflows/ci.yml` — fixed the stdin-redirection bug in the `docker` job's mem_limit assertion.
- `deploy/compose.tools.yml` — added `uvcache`/`uvpython` named volumes + `UV_LINK_MODE: copy` for the toolbox.
- `deploy/compose.yml` — added `cap_add` for postgres's required de-escalation capabilities.
- `deploy/caddy/Caddyfile` — wrapped in `route {}` for order, switched `/api/**` to `/api/*` (Caddy's greedy trailing-star form).
- `deploy/docker/web.Dockerfile` — strip `cap_net_bind_service` from the caddy binary at build time.
- `deploy/minio-init.sh` — write the IAM policy to a temp file instead of a stdin dash.
- `workers-python/pyproject.toml`, `workers-python/uv.lock` — added `pip-audit` dev dependency.
- `web/package.json`, `web/package-lock.json` — bumped `@hey-api/openapi-ts`, `vitest`; swapped `@size-limit/preset-app` → `@size-limit/file`; added `js-yaml` override.
- `web/vite.config.ts` — import `defineConfig` from `vitest/config`.
- `web/src/app.tsx` — import the new client-setup side-effect module.
- `web/src/api/client-setup.ts` (new) — sets an absolute `baseUrl` on the generated client.
- `web/src/api/gen/**` — regenerated (openapi-ts 0.99 output shape, incl. new `client/` and `core/` subfolders).

Did not touch `plans/260924-2244-loomtale-studio-mvp/plan.md` or `plans/reports/spike-260924-model-downloads.md` (controller-owned, left as-is, not staged for commit).

## Deviations from spec (with reasons)

- `make gen-check` verified via `make gen` + host `git diff` instead of running the target literally inside the toolbox, because this specific checkout is a git worktree whose common git dir lives outside the bind-mounted directory. The target itself is unchanged and will work as authored on a normal clone.
- `/healthz` host-measured p95 (10.9ms) exceeds the 5ms budget; the handler itself (measured container-to-container) is <1ms. Documented as a Windows/WSL2 NAT artifact of this dev host, not a code or container issue.
- `readyz` was only exercised to `200 OK` after manually wiring the `minio-init`-printed one-time app secret into the API's environment for this verification run (never committed, never printed here). This matches the documented operator flow in `minio-init.sh`'s own output and `.env.example`'s comment; a totally fresh `compose up` without that manual step will report `readyz` as `503` until the operator does that one-time step, which is intended behavior, not a bug.

## Unresolved questions

- None blocking. The Caddy `/api/*` vs `/api/**` behavior (item 8b above) is worth a one-line callout in `docs/` or a code comment for future maintainers touching the Caddyfile, since it's counter to what the double-star name suggests; the Caddyfile itself now carries that comment, but if there's a docs page for `deploy/`-level gotchas, it may be worth mirroring there — deferred to whichever phase owns `docs/deployment-guide.md`.

Status: DONE
Summary: Verified and fixed every failing check in phase 1's test matrix (proto round-trip test, npm audit, postgres/minio-init/caddy compose startup, Caddy API routing, CI workflow shellcheck bug, pip-audit tooling gap); all listed commands now pass, all security-checklist items verified mechanically, and all measured performance numbers are within budget except host-measured `/healthz` latency, which is a Windows/WSL2 NAT artifact (container-to-container latency is <1ms).
Concerns/Blockers: none blocking; see unresolved questions above for a minor docs follow-up.
