# Choreo — one entry point for every routine task.
#
# Operating rule 1: if something has to be done, it is a target here, not a
# command someone remembers. That is what makes the machine reproducible.

SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

COMPOSE := docker compose --env-file .env -f deploy/compose.yaml
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

# --- database ---------------------------------------------------------------

.PHONY: migrate
migrate: .env ## Apply pending migrations and exit
	CHOREO_VERSION=$(VERSION) $(COMPOSE) run --rm server -migrate-only

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
	cd app && dart run build_runner build --force-jit --delete-conflicting-outputs

.PHONY: test
test: test-server test-app ## Run every test

.PHONY: test-server
test-server: ## Run the Go tests
	cd server && go test -race ./...

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
