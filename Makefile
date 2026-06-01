.PHONY: up down build test run logs clean seed

# ── Docker ────────────────────────────────────────────────────────

up:
	docker compose up -d postgres redis
	@echo "Waiting for services to be healthy..."
	@sleep 3
	@echo "Postgres and Redis are ready."

down:
	docker compose down

build:
	docker compose build app

start: up
	docker compose up app

logs:
	docker compose logs -f app

# ── Local development (without Docker for the app) ────────────────

run: up
	DATABASE_URL=postgres://lottery:lottery_secret@localhost:5432/lottery_db \
	REDIS_URL=redis://localhost:6379 \
	JWT_SECRET=dev_secret_change_in_prod \
	PORT=8080 \
	ENV=development \
	go run ./cmd/server

# ── Testing ───────────────────────────────────────────────────────

test:
	go test ./... -v -count=1

test-algorithm:
	go test ./internal/lottery/... -v -run TestFair -count=1

test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

# ── API smoke test (requires running server) ─────────────────────

# Register → Login → Create event → Book → Close → Draw → Results
smoke:
	@echo "\n=== 1. Register admin user ==="
	@curl -s -X POST http://localhost:8080/auth/register \
	  -H "Content-Type: application/json" \
	  -d '{"email":"admin@lottery.dev","password":"Admin1234!","full_name":"Admin User"}' | jq .

	@echo "\n=== 2. Login ==="
	$(eval TOKEN := $(shell curl -s -X POST http://localhost:8080/auth/login \
	  -H "Content-Type: application/json" \
	  -d '{"email":"admin@lottery.dev","password":"Admin1234!"}' | jq -r '.token'))
	@echo "Token: $(TOKEN)"

# ── Clean ─────────────────────────────────────────────────────────

clean:
	docker compose down -v
	rm -f coverage.out
