.PHONY: help up down build start logs clean run dev test test-algorithm test-cover lint vet tidy smoke e2e

# Default target prints a quick reference.
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ── Docker ────────────────────────────────────────────────────────

up: ## Start postgres + redis in the background
	docker compose up -d postgres redis
	@echo "Waiting for services to be healthy..."
	@sleep 3
	@echo "Postgres and Redis are ready."

down: ## Stop all docker services
	docker compose down

build: ## Build the app docker image
	docker compose build app

start: up ## Start postgres + redis + app via docker compose (uses .env)
	docker compose up app

logs: ## Tail app container logs
	docker compose logs -f app

clean: ## Tear everything down including volumes
	docker compose down -v
	rm -f coverage.out

# ── Local dev (server runs on host, datastores in docker) ─────────

ENV_FILE ?= .env

run: up ## Run the server locally (loads .env if present)
	@if [ ! -f $(ENV_FILE) ]; then cp .env.example $(ENV_FILE); echo "Created $(ENV_FILE) from template — edit it before running"; fi
	@set -a; . ./$(ENV_FILE); set +a; go run ./cmd/server

dev: run ## Alias for `run`

# ── Code quality ──────────────────────────────────────────────────

vet: ## go vet
	go vet ./...

lint: ## go vet + gofmt drift check
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt drift:"; echo "$$out"; exit 1; fi
	go vet ./...

tidy: ## go mod tidy
	go mod tidy

# ── Tests ─────────────────────────────────────────────────────────

test: ## Run unit tests
	go test ./... -race -count=1

test-algorithm: ## Run only the lottery fairness tests
	go test ./internal/lottery/... -v -run TestFair -count=1

test-cover: ## Generate an HTML coverage report
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

# ── API tests against a running server ────────────────────────────

smoke: ## Run smoke_test.sh against http://localhost:8080
	@chmod +x scripts/smoke_test.sh
	@./scripts/smoke_test.sh

e2e: ## Run end-to-end test script against http://localhost:8080
	@chmod +x scripts/test_e2e.sh
	@./scripts/test_e2e.sh
