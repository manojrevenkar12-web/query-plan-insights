# Fast feedback first: `make check test` runs in well under a minute without a database.
GO      ?= go
PG_DSN  ?= postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable

.PHONY: check test test-db web web-test eval heldout overhead report screenshots run db

check:                      ## formatting, vet, types, lint
	test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)
	$(GO) vet ./...
	cd web && npx tsc --noEmit && npx eslint src test

test:                       ## unit tests, Go and web, no database needed
	$(GO) test -race -short ./...
	cd web && npx vitest run

test-db:                    ## everything, including one real scenario against PostgreSQL
	$(GO) test -race ./...

db:                         ## roles, database and extension (server must preload pg_stat_statements)
	psql "$(PG_DSN)" -v ON_ERROR_STOP=1 -f scripts/setup-db.sql

web:                        ## build the web app into web/dist (served by qpi)
	cd web && npm ci && npx vite build

eval:                       ## development scenarios: tune on these
	$(GO) run ./cmd/qpi-eval -set dev

heldout:                    ## held-out scenarios: pinned before tuning, run once
	$(GO) run ./cmd/qpi-eval -set heldout

overhead:                   ## collector overhead on a closed loop
	$(GO) run ./cmd/qpi-eval -overhead

report:                     ## reports/results.json, reports/RESULTS.md and the README block
	$(GO) run ./cmd/qpi-eval -report

screenshots:                ## needs `qpi` on :8080 and `npx vite` on :5173
	node scripts/screenshots.mjs http://localhost:5173 docs/img

run:                        ## watch a database: QPI_MONITOR_DSN=... QPI_APP_ROLE=... make run
	$(GO) run ./cmd/qpi
