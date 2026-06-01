# Lottery System

A production-grade event lottery system built in Go.
Users book events and become eligible for a cryptographically fair lottery draw.

## Architecture

```
Client → Go HTTP Server (chi)
              ├── Auth service      (JWT, bcrypt)
              ├── Event service     (CRUD, status machine)
              ├── Booking service   (distributed lock, capacity tracking)
              └── Lottery service   (crypto/rand, Fisher-Yates, audit log)
                        ↓                    ↓
                   PostgreSQL             Redis
              (persistent store)   (locks + capacity cache)
```

## Key Technical Decisions

### Why `crypto/rand` for the lottery
`math/rand` is a pseudo-random number generator — predictable if you know the seed.
`crypto/rand` reads from OS entropy (`/dev/urandom`), making results genuinely unpredictable.
This is the same entropy source used for TLS keys and cryptographic nonces.

### Why Fisher-Yates shuffle
A naive shuffle produces a biased distribution.
Fisher-Yates guarantees every permutation is equally likely — O(n) and mathematically proven fair.

### Why a distributed lock for booking
Without a Redis lock, 1000 concurrent booking requests can all read "1 slot left"
simultaneously and all succeed — overselling the event.
`SETNX` (Set if Not eXists) is atomic at the Redis server level.
Only one request acquires the lock; others get a retryable error.

### Why the audit log is append-only
Database rules block `UPDATE` and `DELETE` on `lottery_draws`.
Once a draw result is written, it cannot be changed — the audit trail is tamper-evident.

## Quick Start

```bash
# 1. Start Postgres + Redis
make up

# 2. Run the server
make run

# 3. In another terminal, run the smoke test
chmod +x scripts/smoke_test.sh
./scripts/smoke_test.sh
```

## Running Tests

```bash
# All tests
make test

# Algorithm tests only (fairness + correctness)
make test-algorithm

# Coverage report
make test-cover
```

## API Reference

### Auth
| Method | Path            | Auth     | Description        |
|--------|-----------------|----------|--------------------|
| POST   | /auth/register  | None     | Register user      |
| POST   | /auth/login     | None     | Login, get JWT     |
| GET    | /auth/me        | Bearer   | Get current user   |

### Events
| Method | Path                    | Auth        | Description              |
|--------|-------------------------|-------------|--------------------------|
| GET    | /events                 | Bearer      | List all events          |
| GET    | /events/:id             | Bearer      | Get event by ID          |
| POST   | /events                 | Admin       | Create event             |
| PUT    | /events/:id/close       | Admin       | Close event for booking  |
| POST   | /events/:id/book        | Bearer      | Book an event            |
| GET    | /events/:id/bookings    | Admin       | List all bookings        |
| POST   | /events/:id/draw        | Admin       | Run lottery draw         |
| GET    | /events/:id/results     | Bearer      | View draw results        |

### Health
| Method | Path    | Auth | Description    |
|--------|---------|------|----------------|
| GET    | /health | None | Health check   |

## Event Status Machine

```
open → closed → drawn
  └──────────────────→ cancelled
```

A booking is only valid while the event is `open`.
The lottery can only be drawn once the event is `closed`.

## Environment Variables

| Variable     | Required | Default     | Description                |
|--------------|----------|-------------|----------------------------|
| DATABASE_URL | Yes      | —           | Postgres connection string |
| REDIS_URL    | Yes      | —           | Redis connection string    |
| JWT_SECRET   | Yes      | —           | JWT signing key            |
| PORT         | No       | 8080        | HTTP port                  |
| ENV          | No       | development | Environment name           |

## Project Structure

```
lottery-system/
├── cmd/server/         # Entry point, dependency wiring
├── internal/
│   ├── config/         # Environment config
│   ├── db/             # Postgres + Redis clients
│   ├── middleware/      # JWT auth, logger, recoverer
│   ├── user/           # User domain (model, repo, service, handler)
│   ├── event/          # Event domain
│   ├── booking/        # Booking domain + distributed lock
│   └── lottery/        # Lottery algorithm + audit log
├── migrations/         # SQL schema
├── scripts/            # Smoke tests
├── docker-compose.yml
├── Dockerfile
└── Makefile
```
