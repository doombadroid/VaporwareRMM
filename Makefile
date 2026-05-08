.PHONY: test test-unit test-postgres test-e2e test-load up-test-db down-test-db help \
	agent-build agent-build-all clean-bin

PG_URL ?= postgres://test:test@localhost:5433/vaporrmm_test?sslmode=disable
LOAD_BASE ?= http://localhost:8080

# Wraps docker so the same target works whether or not the invoking shell already
# has the docker group (sg falls back through if you're not in the group yet).
DOCKER ?= $(shell docker info >/dev/null 2>&1 && echo "docker" || echo "sg docker -c 'docker'")
COMPOSE = $(DOCKER) compose -f docker-compose.test.yml
COMPOSE_SG = sg docker -c "docker compose -f docker-compose.test.yml"

help:
	@echo "Targets:"
	@echo "  test           run all unit + integration tests (no e2e, no load)"
	@echo "  test-unit      run Go unit tests against SQLite (fast)"
	@echo "  test-postgres  run Go tests against ephemeral Postgres (requires Docker access)"
	@echo "  test-e2e       run Playwright end-to-end tests (boots full stack)"
	@echo "  test-load      run k6 load tests against \$$LOAD_BASE (default $(LOAD_BASE))"
	@echo "  up-test-db     start ephemeral Postgres on :5433"
	@echo "  down-test-db   stop and wipe ephemeral Postgres"

test: test-unit test-postgres
	@echo "✓ unit + postgres integration suites passed"

test-unit:
	cd packages/server && go test -count=1 -timeout 120s ./...

up-test-db:
	@if docker info >/dev/null 2>&1; then \
		docker compose -f docker-compose.test.yml up -d postgres-test; \
	else \
		sg docker -c "docker compose -f docker-compose.test.yml up -d postgres-test"; \
	fi
	@echo "Waiting for Postgres to become healthy..."
	@for i in $$(seq 1 30); do \
		if (docker info >/dev/null 2>&1 && docker compose -f docker-compose.test.yml exec -T postgres-test pg_isready -U test -d vaporrmm_test >/dev/null 2>&1) \
		   || sg docker -c "docker compose -f docker-compose.test.yml exec -T postgres-test pg_isready -U test -d vaporrmm_test" >/dev/null 2>&1; then \
			echo "Postgres ready"; exit 0; \
		fi; sleep 1; \
	done; echo "Postgres did not become ready" >&2; exit 1

down-test-db:
	@if docker info >/dev/null 2>&1; then \
		docker compose -f docker-compose.test.yml down -v; \
	else \
		sg docker -c "docker compose -f docker-compose.test.yml down -v"; \
	fi

test-postgres: up-test-db
	cd packages/server && DATABASE_URL='$(PG_URL)' go test -count=1 -timeout 120s -run TestPostgres ./...
	@$(MAKE) -s down-test-db

test-e2e:
	cd apps/dashboard && rm -f /tmp/vaporrmm-e2e.db && npx playwright test

test-load:
	@which k6 >/dev/null 2>&1 || { echo "k6 not in PATH; expected ~/.local/bin/k6"; exit 1; }
	k6 run -e BASE_URL='$(LOAD_BASE)' -e AGENTS=100 -e DURATION=30s loadtest/agents.js
	k6 run -e BASE_URL='$(LOAD_BASE)' -e USERS=10 -e DURATION=30s loadtest/dashboard.js

# ── Agent cross-builds ──────────────────────────────────────────────
# Outputs land in bin/. Each target binary is named agent-<os>-<arch>[.exe].
# CGO is OFF for portability; the system tray is a no-op on darwin builds.
# To get a real Mac tray icon, build on a Mac with CGO_ENABLED=1.
BIN_DIR := bin
AGENT_TARGETS := linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64

agent-build:
	@mkdir -p $(BIN_DIR)
	cd packages/agent && CGO_ENABLED=0 go build -ldflags="-s -w" -o ../../$(BIN_DIR)/agent .

agent-build-all:
	@mkdir -p $(BIN_DIR)
	@for target in $(AGENT_TARGETS); do \
		os=$${target%/*}; arch=$${target#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=$(BIN_DIR)/agent-$$os-$$arch$$ext; \
		echo ">> $$os/$$arch -> $$out"; \
		(cd packages/agent && CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags="-s -w" -o ../../$$out .) || exit 1; \
	done
	@ls -la $(BIN_DIR)/

clean-bin:
	rm -rf $(BIN_DIR)/
