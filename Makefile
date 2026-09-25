.PHONY: gen gen-check lint test test-integration test-integration-toolbox vuln audit build up down logs migrate ci gen-openapi gen-sqlc gen-proto gen-migrations-sync

GOFLAGS := -mod=mod

## Codegen: openapi bundle -> Go + TS, sql -> sqlc, proto -> Go + Python.
## Generated code is committed; gen-check fails CI on drift.
gen: gen-migrations-sync gen-openapi gen-sqlc gen-proto web/node_modules
	cd web && npm run gen

## Fresh clones (CI) have no web deps yet; install them from the lockfile once.
web/node_modules: web/package-lock.json
	cd web && npm ci --no-audit --no-fund
	touch web/node_modules

gen-migrations-sync:
	rm -f api/internal/db/migrations/*.sql
	cp db/migrations/*.sql api/internal/db/migrations/

gen-openapi:
	redocly bundle openapi/root.yaml -o openapi/openapi.gen.yaml
	cd api && go tool oapi-codegen -config ../openapi/oapi-codegen.yaml ../openapi/openapi.gen.yaml

gen-sqlc:
	cd api && go tool sqlc generate -f ../db/sqlc.yaml

gen-proto:
	cd proto && buf generate

gen-check: gen
	git -c safe.directory='*' diff --exit-code -- api/internal/db/gen api/internal/httpapi/gen api/internal/workerpb workers-python/src/loomtale_worker/pb web/src/api/gen openapi/openapi.gen.yaml api/internal/db/migrations \
		|| (echo "generated code is out of date; run 'make gen' and commit the diff" && exit 1)

lint:
	cd api && go vet ./...
	cd api && golangci-lint run ./...
	cd tools && go vet ./...
	cd tools && go build -o ../api/bin/tenantctx ./tenantctx/cmd/tenantctx
	cd api && ./bin/tenantctx ./...
	bash scripts/lint-tenant-queries.sh
	cd workers-python && uv run ruff check .
	cd web && npm run lint

test:
	cd api && go test ./... -race -count=1
	cd tools && go test ./... -race -count=1
	cd workers-python && uv run pytest -q

test-integration:
	cd api && go test ./internal/workerpb/... ./internal/integration/... -tags=integration -run "$(RUN)" -v -count=1

## Runs the integration suite against a running stack from inside a
## container on its own network, so it needs no Go on the host. Bring the
## stack up first with deploy/compose.integration.yml applied and pass the
## same project name here, e.g.:
##   docker compose -p loomtale-dev -f deploy/compose.yml -f deploy/compose.integration.yml --env-file .env up -d --wait --build
##   PROJECT=loomtale-dev make test-integration-toolbox
##   docker compose -p loomtale-dev -f deploy/compose.yml -f deploy/compose.integration.yml --env-file .env down -v
test-integration-toolbox:
	bash scripts/test-integration-toolbox.sh

vuln:
	cd api && go tool govulncheck ./...
	cd workers-python && uv run pip-audit || true

audit:
	cd web && npm audit --audit-level=high

build:
	docker compose -f deploy/compose.yml build

up:
	docker compose -f deploy/compose.yml up -d --wait

down:
	docker compose -f deploy/compose.yml down

logs:
	docker compose -f deploy/compose.yml logs -f

migrate:
	cd api && go run ./cmd/loomtale migrate $(ARGS)

## ci mirrors the CI workflow: lint, unit tests, gen-check, vuln, audit.
ci: lint test gen-check vuln audit
