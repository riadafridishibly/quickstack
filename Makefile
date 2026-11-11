.DEFAULT_GOAL := help

-include .env

SQLC          := go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

PG_ADMIN_URL                 ?= postgres://localhost:5432/postgres?sslmode=disable
QUICKSTACK_DATABASE_URL      ?= postgres://quickstack_app@localhost:5432/quickstack?sslmode=disable
QUICKSTACK_MIGRATE_URL       ?= postgres://quickstack_owner@localhost:5432/quickstack?sslmode=disable
QUICKSTACK_TEST_DATABASE_URL ?= $(PG_ADMIN_URL)
E2E_DATABASE_URL             ?= postgres://quickstack_app@localhost:5432/quickstack_e2e?sslmode=disable
E2E_MIGRATE_URL              ?= postgres://quickstack_owner@localhost:5432/quickstack_e2e?sslmode=disable
export QUICKSTACK_DATABASE_URL QUICKSTACK_MIGRATE_URL QUICKSTACK_TEST_DATABASE_URL

.PHONY: help setup hooks db-setup db-reset db-up psql migration dev dev-vite dev-go seed gen fmt build build-linux check check-deps test e2e clean

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-11s %s\n", $$1, $$2}'

setup: hooks ## Install pnpm packages and Playwright's Chromium
	pnpm install
	pnpm exec playwright install chromium

hooks: ## Run make check before every git push
	git config core.hooksPath .githooks

db-setup: ## Create the roles and the quickstack database on PG_ADMIN_URL
	psql "$(PG_ADMIN_URL)" -v ON_ERROR_STOP=1 -q -f internal/db/roles.sql
	psql "$(PG_ADMIN_URL)" -tAc "SELECT 1 FROM pg_database WHERE datname = 'quickstack'" | grep -q 1 \
		|| psql "$(PG_ADMIN_URL)" -qc "CREATE DATABASE quickstack OWNER quickstack_owner"

db-reset: ## Drop and recreate the quickstack database
	psql "$(PG_ADMIN_URL)" -qc "DROP DATABASE IF EXISTS quickstack WITH (FORCE)"
	@$(MAKE) db-setup

db-up: ## Start PostgreSQL 17 in Docker instead of a local server
	docker compose up -d --wait db

psql: ## Open psql as the app role
	psql "$(QUICKSTACK_DATABASE_URL)"

migration: ## New migration: make migration name=create_things
	@test -n "$(name)" || { echo "usage: make migration name=create_things"; exit 1; }
	@dir=internal/db/migrations; \
		ts=$$(date -u +%Y%m%d%H%M%S); \
		last=$$(ls $$dir | sed -n 's/^\([0-9]\{14\}\)_.*/\1/p' | sort | tail -1); \
		[ -n "$$last" ] && [ "$$ts" -le "$$last" ] && ts=$$((last + 1)); \
		f=$$dir/$${ts}_$(name).sql; \
		printf -- '-- +goose Up\n\n-- +goose Down\n' > $$f && echo $$f

dev: ## Vite + Go with live reload; open http://localhost:7360
	@$(MAKE) -j2 dev-vite dev-go

dev-vite:
	pnpm exec vite

dev-go: export QUICKSTACK_DEV = 1
dev-go:
	go tool templ generate -watch -proxy=http://localhost:8360 -proxyport=7360 -open-browser=false -cmd="go run ./cmd/quickstack"

seed: ## Fill the local database with sample accounts and workspaces: make seed [args="--reset"]
	go run ./cmd/seed $(args)

gen: ## Generate Go from .templ and .sql files
	go tool templ generate
	$(SQLC) generate

fmt: ## Format Go, templ, and TypeScript
	go tool templ fmt .
	$(GOLANGCI_LINT) fmt
	pnpm run fix

build: gen ## Build the frontend and bin/quickstack
	pnpm run build
	go build -o bin/quickstack ./cmd/quickstack

build-linux: gen ## Static bin/linux-amd64/quickstack
	pnpm run build
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/linux-amd64/quickstack ./cmd/quickstack

test: ## Go tests (needs PostgreSQL, see QUICKSTACK_TEST_DATABASE_URL)
	go test ./...

check: gen check-deps ## Everything that must pass before work is done
	go tool templ fmt -fail .
	go vet ./...
	$(GOLANGCI_LINT) run
	go test ./...
	pnpm run check
	pnpm run build

check-deps:
	@if go list -deps ./cmd/quickstack | grep -q '/internal/seed'; then \
		echo "cmd/quickstack must not depend on internal/seed"; exit 1; fi

e2e: build ## Playwright tests against the built app
	psql "$(PG_ADMIN_URL)" -v ON_ERROR_STOP=1 -q -f internal/db/roles.sql \
		-c "DROP DATABASE IF EXISTS quickstack_e2e WITH (FORCE)" \
		-c "CREATE DATABASE quickstack_e2e OWNER quickstack_owner"
	QUICKSTACK_DATABASE_URL="$(E2E_DATABASE_URL)" QUICKSTACK_MIGRATE_URL="$(E2E_MIGRATE_URL)" \
		pnpm exec playwright test

clean: ## Remove build output
	rm -rf bin test-results playwright-report
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete
