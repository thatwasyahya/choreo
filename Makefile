# Choreo — one entry point for every routine task.
#
# Operating rule 1: if something has to be done, it is a target here, not a
# command someone remembers. That is what makes the machine reproducible.

SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

COMPOSE := docker compose --env-file .env -f deploy/compose.yaml
COMPOSE_PROD := $(COMPOSE) -f deploy/compose.prod.yaml
COMPOSE_TEST := $(COMPOSE) -f deploy/compose.test.yaml

# Read from .env rather than duplicated here: one source for the credentials.
PGUSER = $(shell grep -E '^POSTGRES_USER=' .env | cut -d= -f2)
PGPASS = $(shell grep -E '^POSTGRES_PASSWORD=' .env | cut -d= -f2)
PGPORT = $(shell grep -E '^CHOREO_TEST_PGPORT=' .env | cut -d= -f2)
TEST_PGPORT = $(if $(PGPORT),$(PGPORT),5433)
TEST_DSN = postgres://$(PGUSER):$(PGPASS)@localhost:$(TEST_PGPORT)/choreo_test?sslmode=disable
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help
help: ## Show this help
	@echo "Choreo — available targets:"
	@grep -hE '^[a-zA-Z_.-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "} {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# .env is never committed. Anything touching compose depends on it existing.
.env:
	@cp .env.example .env
	@echo "→ .env created from .env.example. Review it before going any further."

# --- development ------------------------------------------------------------

.PHONY: dev
dev: .env ## Bring the whole stack up (postgres + server)
	CHOREO_VERSION=$(VERSION) $(COMPOSE) up --build -d
	@echo
	@$(COMPOSE) ps
	@echo
	@echo "→ http://localhost:$${CHOREO_DEV_PORT:-8080}/healthz"

.PHONY: down
down: ## Stop the stack, keep the data
	$(COMPOSE) down

.PHONY: logs
logs: ## Follow the server logs
	$(COMPOSE) logs -f server

.PHONY: ps
ps: ## Show what is running
	$(COMPOSE) ps

.PHONY: prod-up
prod-up: .env ## Bring the stack up behind Caddy (needs CHOREO_DOMAIN in .env)
	CHOREO_VERSION=$(VERSION) $(COMPOSE_PROD) up --build -d
	@$(COMPOSE_PROD) ps

.PHONY: prod-config
prod-config: .env ## Check the production composition without starting anything
	$(COMPOSE_PROD) config >/dev/null && echo "→ production composition is valid"

# --- database ---------------------------------------------------------------

.PHONY: migrate
migrate: .env ## Apply pending migrations and exit
	# --build is not optional: migrations are embedded in the binary, so a stale
	# image silently reports "no migrations to run".
	CHOREO_VERSION=$(VERSION) $(COMPOSE) run --rm --build server -migrate-only

.PHONY: db-reset
db-reset: .env ## Destroy the database and rebuild it from the migrations
	@echo "→ this erases every local datum. Ctrl-C within 3 seconds to cancel."
	@sleep 3
	$(COMPOSE) down -v
	$(MAKE) dev

.PHONY: psql
psql: .env ## Open a psql session on the local database
	$(COMPOSE) exec postgres psql -U "$$(grep -E '^POSTGRES_USER=' .env | cut -d= -f2)" \
		-d "$$(grep -E '^POSTGRES_DB=' .env | cut -d= -f2)"

# --- quality ----------------------------------------------------------------

.PHONY: generate
generate: ## Regenerate the Drift and localisation code
	# --force-jit is required, not optional: build_runner AOT-compiles its own
	# build script with `dart compile`, which refuses any package carrying a
	# native build hook — and sqlite3, under drift, carries one.
	cd app && dart run build_runner build --force-jit

.PHONY: test-db
test-db: .env ## Start PostgreSQL with a published port and create choreo_test
	# --wait rather than a sleep loop: compose already knows what healthy means.
	$(COMPOSE_TEST) up -d --wait postgres
	@$(COMPOSE_TEST) exec -T postgres psql -U $(PGUSER) -d postgres -tAc \
	    "SELECT 1 FROM pg_database WHERE datname='choreo_test'" | grep -q 1 \
	  || $(COMPOSE_TEST) exec -T postgres createdb -U $(PGUSER) choreo_test
	@echo "→ test database ready on port $(TEST_PGPORT)"

.PHONY: test
test: test-server test-app ## Run every test

.PHONY: test-server
test-server: ## Run the Go tests (integration tests skip without `make test-db`)
	cd server && CHOREO_TEST_DATABASE_URL="$(TEST_DSN)" go test -race ./...

.PHONY: test-app
test-app: generate ## Run the Flutter tests
	cd app && flutter test

.PHONY: lint
lint: lint-server lint-app ## Lint everything

.PHONY: lint-server
lint-server: ## Lint the Go code
	cd server && go vet ./... && golangci-lint run

.PHONY: lint-app
lint-app: generate ## Analyse the Dart code
	cd app && flutter analyze --fatal-infos --fatal-warnings

.PHONY: fmt
fmt: ## Format everything
	cd server && go fmt ./...
	cd app && dart format lib test

# --- mobile -----------------------------------------------------------------

.PHONY: apk
apk: generate ## Build a debug APK
	cd app && flutter build apk --debug

.PHONY: devices
devices: ## List the devices Flutter can see
	flutter devices

# --- build ------------------------------------------------------------------

.PHONY: build
build: ## Compile the server binary into server/bin/
	cd server && go build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION)" \
		-o bin/choreod ./cmd/choreod
	@echo "→ server/bin/choreod ($(VERSION))"
