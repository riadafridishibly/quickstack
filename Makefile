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

# libvips comes through cgo. On macOS, each package that links it repeats
# its libraries, and the linker warns about every one.
ifeq ($(shell uname -s),Darwin)
export CGO_LDFLAGS += -Wl,-no_warn_duplicate_libraries
endif

# Go build tags for dev, build, dist and docker. TAGS=resvg also draws a
# social card from each SVG upload, with resvg from a quickstack-resvg kit
# (scripts/resvg-kit).
TAGS ?=
comma := ,
empty :=
space := $(empty) $(empty)
# TAGS may be separated by commas or spaces; go and the Dockerfile get
# commas.
GOTAGS := $(subst $(space),$(comma),$(strip $(subst $(comma), ,$(TAGS))))
HOST := $(shell go env GOOS)-$(shell go env GOARCH)
RESVG_PC := $(CURDIR)/dist/resvg/$(HOST)/lib/pkgconfig
ifneq (,$(filter resvg,$(subst $(comma), ,$(TAGS))))
export PKG_CONFIG_PATH := $(RESVG_PC)$(if $(PKG_CONFIG_PATH),:$(PKG_CONFIG_PATH))
RESVG_KIT := resvg-kit
endif

.PHONY: help setup check-vips resvg-kit hooks db-setup db-reset db-up s3-up psql migration dev dev-vite dev-go seed gen fmt build dist docker check check-resvg check-deps test e2e clean

help: ## List targets
	@grep -hE '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-11s %s\n", $$1, $$2}'

setup: check-vips hooks ## Install pnpm packages and Playwright's Chromium
	pnpm install
	pnpm exec playwright install chromium

check-vips:
	@pkg-config --exists 'vips >= 8.16' || { \
		echo "libvips 8.16 or later is needed to build: brew install vips, or apt install libvips-dev"; exit 1; }

resvg-kit:
	scripts/resvg-kit $(HOST) dist/resvg/$(HOST)

hooks: ## Run make check before every git push
	git config core.hooksPath .githooks

db-setup: ## Create the roles and the quickstack database on PG_ADMIN_URL
	psql "$(PG_ADMIN_URL)" -v ON_ERROR_STOP=1 -q -f internal/db/roles.sql
	psql "$(PG_ADMIN_URL)" -tAc "SELECT 1 FROM pg_database WHERE datname = 'quickstack'" | grep -q 1 \
		|| psql "$(PG_ADMIN_URL)" -qc "CREATE DATABASE quickstack OWNER quickstack_owner"

db-reset: ## Drop and recreate the quickstack database
	psql "$(PG_ADMIN_URL)" -qc "DROP DATABASE IF EXISTS quickstack WITH (FORCE)"
	@$(MAKE) db-setup

db-up: ## Start PostgreSQL 18 in Docker instead of a local server
	docker compose up -d --wait db

s3-up: ## Start SeaweedFS, an S3-compatible store, in Docker
	docker compose --profile s3 up -d --wait s3

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

dev: $(RESVG_KIT) ## Vite + Go with live reload; open http://localhost:7360 [TAGS=resvg]
	@$(MAKE) -j2 dev-vite dev-go

dev-vite:
	pnpm exec vite

dev-go: export QUICKSTACK_DEV = 1
dev-go:
	go tool templ generate -watch -proxy=http://localhost:8360 -proxyport=7360 -open-browser=false -cmd="go run -tags=$(GOTAGS) ./cmd/quickstack"

# QUICKSTACK_DEV puts the sample images where `make dev` serves them from.
seed: export QUICKSTACK_DEV = 1
seed: ## Fill the local database with sample accounts, workspaces and posts: make seed [args="--reset"]
	go run ./cmd/seed $(args)

gen: ## Generate Go from .templ and .sql files, and the code highlighting CSS
	go tool templ generate
	$(SQLC) generate
	go run ./internal/markdown/chromacss frontend/src/chroma.css

fmt: ## Format Go, templ, and TypeScript
	go tool templ fmt .
	$(GOLANGCI_LINT) fmt
	pnpm run fix

build: gen $(RESVG_KIT) ## Build the frontend and bin/quickstack
	pnpm run build
	go build -tags=$(GOTAGS) -o bin/quickstack ./cmd/quickstack

# dist cross-compiles with zig against a libvips kit from quickstack-vips
# (scripts/vips-kit), so the release carries its own libvips. resvg is a
# static library, so only its licences go into the release.
TARGET ?= linux-amd64
zig_target.linux-amd64   := x86_64-linux-gnu.2.28
zig_target.linux-arm64   := aarch64-linux-gnu.2.28
zig_target.freebsd-amd64 := x86_64-freebsd.14.0
DIST := dist/quickstack-$(TARGET)

dist: gen ## Release tarball for TARGET: linux-amd64 (default), linux-arm64 or freebsd-amd64 [TAGS=resvg]
	@test -n "$(zig_target.$(TARGET))" || { echo "TARGET must be linux-amd64, linux-arm64 or freebsd-amd64"; exit 1; }
	@command -v zig >/dev/null || { echo "zig is needed: brew install zig, or see ziglang.org"; exit 1; }
	pnpm run build
	scripts/vips-kit $(TARGET) dist/vips/$(TARGET)
	$(if $(RESVG_KIT),scripts/resvg-kit $(TARGET) dist/resvg/$(TARGET))
	rm -rf $(DIST) $(DIST).tar.gz
	mkdir -p $(DIST)/bin $(DIST)/lib $(DIST)/share
	GOOS=$(word 1,$(subst -, ,$(TARGET))) GOARCH=$(word 2,$(subst -, ,$(TARGET))) CGO_ENABLED=1 \
		CC="zig cc -target $(zig_target.$(TARGET))" \
		PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR=$(CURDIR)/dist/vips/$(TARGET)/lib/pkgconfig:$(CURDIR)/dist/resvg/$(TARGET)/lib/pkgconfig \
		CGO_LDFLAGS='-Wl,-rpath,$$ORIGIN/../lib' \
		go build -a -trimpath -tags=$(GOTAGS) -ldflags='-s -w' -o $(DIST)/bin/quickstack ./cmd/quickstack
	cp -a dist/vips/$(TARGET)/lib/*.so.* $(DIST)/lib/
	cp -R dist/vips/$(TARGET)/share/licenses $(DIST)/share/
	$(if $(RESVG_KIT),cp -R dist/resvg/$(TARGET)/share/licenses $(DIST)/share/licenses/quickstack-resvg)
	$(if $(RESVG_KIT),mkdir -p $(DIST)/share/licenses/dejavu-fonts && cp internal/media/fonts/LICENSE $(DIST)/share/licenses/dejavu-fonts/)
	cp scripts/install $(DIST)/install
	COPYFILE_DISABLE=1 tar --no-xattrs -czf $(DIST).tar.gz -C dist quickstack-$(TARGET)
	@echo $(DIST).tar.gz

docker: ## Build the quickstack Docker image [TAGS=resvg]
	docker build --build-arg TAGS=$(GOTAGS) -t quickstack .

test: ## Go tests (needs PostgreSQL, see QUICKSTACK_TEST_DATABASE_URL)
	go test ./...

check: gen check-deps ## Everything that must pass before work is done
	go tool templ fmt -fail .
	go vet ./...
	$(GOLANGCI_LINT) run
	go test ./...
	pnpm run check
	pnpm run build

# The resvg tag's code and tests, which make check doesn't build.
check-resvg: ## go vet, golangci-lint and go test with TAGS=resvg, for the packages it changes
	scripts/resvg-kit $(HOST) dist/resvg/$(HOST)
	PKG_CONFIG_PATH=$(RESVG_PC)$${PKG_CONFIG_PATH:+:$$PKG_CONFIG_PATH} sh -c '\
		go vet -tags=resvg ./internal/media/... ./internal/blog/... && \
		$(GOLANGCI_LINT) run --build-tags=resvg ./internal/media/... ./internal/blog/... && \
		go test -tags=resvg ./internal/media/... ./internal/blog/...'

check-deps:
	@deps=$$(go list -deps ./cmd/quickstack) && \
	if printf '%s\n' "$$deps" | grep -q '/internal/seed'; then \
		echo "cmd/quickstack must not depend on internal/seed"; exit 1; fi
	@mod=$$(go list -m) && deps=$$(go list -deps ./internal/auth/...) || exit 1; \
	bad=$$(printf '%s\n' "$$deps" | grep "^$$mod/" | grep -vE "^$$mod/internal/(auth|db|web|mail|jobs)(/|$$)"); \
	if [ -n "$$bad" ]; then \
		echo "internal/auth may use only db, web, mail and jobs, so it can be copied. It uses:"; \
		echo "$$bad"; exit 1; fi

e2e: build ## Playwright tests against the built app
	psql "$(PG_ADMIN_URL)" -v ON_ERROR_STOP=1 -q -f internal/db/roles.sql \
		-c "DROP DATABASE IF EXISTS quickstack_e2e WITH (FORCE)" \
		-c "CREATE DATABASE quickstack_e2e OWNER quickstack_owner"
	QUICKSTACK_DATABASE_URL="$(E2E_DATABASE_URL)" QUICKSTACK_MIGRATE_URL="$(E2E_MIGRATE_URL)" \
		pnpm exec playwright test

clean: ## Remove build output
	rm -rf bin dist test-results playwright-report
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete
