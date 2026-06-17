# Lottery Management System — Backend

A production-grade event lottery service in Go. Users register, book an event, and become eligible for a cryptographically fair lottery draw. Built on chi, pgx, Redis, and zerolog.

## Architecture

```
Client ─→ chi HTTP router
              │
              ├── Auth      (bcrypt + JWT HS256)
              ├── Events    (CRUD, status machine, RBAC)
              ├── Bookings  (Redis distributed lock + Postgres FOR UPDATE)
              └── Lottery   (crypto/rand Fisher-Yates, append-only audit log)
                       │              │
                  PostgreSQL         Redis
              (durable store +   (per-event lock,
               audit trail)       owner-safe release)
```

## Key technical decisions

- **`crypto/rand` + Fisher-Yates** for the lottery. `math/rand` is seeded and predictable. `crypto/rand` reads kernel entropy (`/dev/urandom`) — the same source used for TLS keys. Fisher-Yates produces a uniform permutation in O(n).
- **Owner-safe Redis lock** for booking. `SETNX` with a random token acquires the lock; release is a Lua "compare-and-delete" so a request whose TTL expired cannot unlock another request's section.
- **`SELECT ... FOR UPDATE`** on the event row inside the booking transaction. Defence in depth: the DB rejects an oversold booking even if the Redis lock is somehow bypassed.
- **The whole draw runs in one transaction**, with the event row locked, status enforced as `closed`, participants fetched, audit log written, and status flipped to `drawn` — all-or-nothing.
- **`lottery_draws` is append-only at the DB level**. `REVOKE UPDATE, DELETE ON lottery_draws FROM PUBLIC` so attempts raise a real permission error (no silent no-ops).

## Quick start

```bash
# 1. Copy and edit env vars (JWT_SECRET must be set!)
cp .env.example .env
# generate a real secret:
echo "JWT_SECRET=$(openssl rand -base64 48)" >> .env

# 2. Start Postgres + Redis
make up

# 3. Run the server locally
make run

# 4. In another shell, run the smoke test
make smoke
```

Or run everything in Docker:

```bash
export JWT_SECRET=$(openssl rand -base64 48)
export BOOTSTRAP_ADMIN_EMAIL=admin@example.com
export BOOTSTRAP_ADMIN_PASSWORD=ChangeMeRightNow!
docker compose up --build
```

## Environment variables

| Variable                   | Required | Default          | Notes                                                         |
| -------------------------- | -------- | ---------------- | ------------------------------------------------------------- |
| `DATABASE_URL`             | yes      | —                | Postgres connection string                                    |
| `REDIS_URL`                | one of   | —                | Preferred; standard `redis://[user:pass@]host:port/db` format |
| `REDIS_ADDR`               | one of   | `localhost:6379` | Used if `REDIS_URL` is unset                                  |
| `REDIS_PASSWORD`           | no       | _empty_          | Used with `REDIS_ADDR`                                        |
| `REDIS_DB`                 | no       | `0`              | Used with `REDIS_ADDR`                                        |
| `JWT_SECRET`               | **yes**  | —                | Must be ≥32 characters; load fails otherwise                  |
| `JWT_EXPIRY`               | no       | `24h`            | Go duration                                                   |
| `PORT`                     | no       | `8080`           | HTTP port                                                     |
| `ENV`                      | no       | `development`    | Free-form label                                               |
| `LOG_LEVEL`                | no       | `info`           | `debug`/`info`/`warn`/`error`                                 |
| `PRETTY`                   | no       | `false`          | Human-readable console logs when `true`                       |
| `READ_TIMEOUT`             | no       | `10s`            | HTTP read timeout                                             |
| `WRITE_TIMEOUT`            | no       | `30s`            | HTTP write timeout                                            |
| `IDLE_TIMEOUT`             | no       | `120s`           |                                                               |
| `SHUTDOWN_TIMEOUT`         | no       | `30s`            | Graceful drain window                                         |
| `MAX_REQUEST_BYTES`        | no       | `1048576`        | 1 MiB body cap                                                |
| `LOCK_TTL`                 | no       | `5s`             | Booking Redis lock TTL                                        |
| `AUTH_RATE_LIMIT_REQUESTS` | no       | `10`             | Per-IP, applied to `/auth/*`                                  |
| `AUTH_RATE_LIMIT_WINDOW`   | no       | `1m`             |                                                               |
| `API_RATE_LIMIT_REQUESTS`  | no       | `100`            | Per-IP, applied to all routes                                 |
| `API_RATE_LIMIT_WINDOW`    | no       | `1m`             |                                                               |
| `CORS_ALLOWED_ORIGINS`     | no       | _empty_          | CSV; use `*` for "any". Empty disables CORS                   |
| `MIGRATE_ON_START`         | no       | `true`           | Apply embedded SQL migrations at boot                         |
| `BOOTSTRAP_ADMIN_EMAIL`    | no       | _empty_          | If set with password, idempotently ensures an admin           |
| `BOOTSTRAP_ADMIN_PASSWORD` | no       | _empty_          | Required only at bootstrap; rotate via promote endpoint       |
| `BOOTSTRAP_ADMIN_NAME`     | no       | `System Admin`   |                                                               |

## API

All responses follow:

```json
{ "success": true|false, "data": ..., "error": "...", "meta": {...} }
```

### Auth

| Method | Path             | Auth   | Description                |
| ------ | ---------------- | ------ | -------------------------- |
| POST   | `/auth/register` | none   | Create a regular user      |
| POST   | `/auth/login`    | none   | Returns `{ token, user }`  |
| GET    | `/auth/me`       | Bearer | Returns the current user   |

### Events

| Method | Path                            | Auth   | Description                       |
| ------ | ------------------------------- | ------ | --------------------------------- |
| GET    | `/events`                       | none   | Paginated, optional `?status=`    |
| GET    | `/events/{id}`                  | none   | Includes live booking count       |
| POST   | `/events`                       | admin  | Create event (status=`open`)      |
| PUT    | `/events/{id}/close`            | admin  | open → closed                     |
| PUT    | `/events/{id}/cancel`           | admin  | open|closed → cancelled           |
| GET    | `/events/{id}/bookings`         | admin  | List bookings for the event       |
| POST   | `/events/{id}/book`             | Bearer | Book the event                    |
| POST   | `/events/{id}/draw`             | admin  | Run the lottery (requires closed) |
| GET    | `/events/{id}/results`          | none   | Winners + waitlist                |

### Bookings

| Method | Path           | Auth   | Description             |
| ------ | -------------- | ------ | ----------------------- |
| GET    | `/me/bookings` | Bearer | Bookings for the caller |

### Admin

| Method | Path                              | Auth  | Description                    |
| ------ | --------------------------------- | ----- | ------------------------------ |
| POST   | `/admin/users/{id}/promote`       | admin | Elevate a user's role to admin |

### Operations

| Method | Path     | Auth | Description                                   |
| ------ | -------- | ---- | --------------------------------------------- |
| GET    | `/health` | none | Liveness — process is up                      |
| GET    | `/ready`  | none | Readiness — Postgres + Redis reachable        |

## Event status machine

```
open ─→ closed ─→ drawn
  └────────────────→ cancelled
```

- Bookings are only valid while `status='open'`.
- The lottery can only be drawn when `status='closed'`.

## Security notes

- Passwords hashed with bcrypt cost 12.
- JWTs are HMAC-SHA256, validated with an explicit `WithValidMethods` filter to defeat `alg:none` attacks.
- Same error message for "no such user" and "wrong password", plus an equivalent bcrypt comparison in the no-user branch — defends against user enumeration via timing and content.
- Per-IP rate limits on `/auth/login` and `/auth/register` (10/min default).
- `lottery_draws` has `UPDATE`/`DELETE` revoked from `PUBLIC` — tamper-evident.
- 1 MiB request body cap protects against oversize-body DoS.
- Distroless container runs as `nonroot`.

## Migrations

Embedded SQL files in `internal/db/migrations` are applied on boot when `MIGRATE_ON_START=true` (default). The runner records applied versions in the `schema_migrations` table and never re-runs a recorded migration. Each migration runs in its own transaction.

To add a new migration, drop a `NNN_name.sql` file into `internal/db/migrations`. The number is the version; higher numbers run later.

## Testing

```bash
make test            # unit tests with -race
make test-algorithm  # lottery fairness tests only
make test-cover      # HTML coverage report

# Against a running server:
make run             # in one shell
make smoke           # in another
make e2e             # fuller end-to-end script
```

## Production checklist

- [ ] Provide a real `JWT_SECRET` from a secret manager (≥32 chars).
- [ ] Use a managed Postgres with `sslmode=require` in `DATABASE_URL`.
- [ ] Use a managed Redis (TLS) — `rediss://` URL.
- [ ] Set `CORS_ALLOWED_ORIGINS` to your frontend origins (avoid `*` with credentials).
- [ ] Set `BOOTSTRAP_ADMIN_EMAIL`/`PASSWORD` for the first boot, then unset both. Rotate via `POST /admin/users/:id/promote`.
- [ ] Point `/ready` at your load balancer / Kubernetes readiness probe and `/health` at liveness.
- [ ] Aggregate structured logs into your observability stack (zerolog emits JSON by default).
- [ ] Run behind TLS termination.

## Project structure

```
.
├── cmd/server/                 # entry point + dependency wiring + health/ready handlers
├── internal/
│   ├── auth/                   # registration, login, me, admin promotion
│   ├── booking/                # Redis-locked booking flow
│   ├── config/                 # env validation
│   ├── db/                     # pgx pool, redis client, embedded migration runner
│   │   └── migrations/         # *.sql files embedded into the binary
│   ├── event/                  # event CRUD + status transitions
│   ├── httpx/                  # JSON envelope + decode helpers
│   ├── lottery/                # crypto-fair draw algorithm + audit log
│   ├── middleware/             # JWT, RBAC, logger, recoverer, body limit, CORS
│   ├── models/                 # request/response/domain types
│   └── pgerr/                  # Postgres error classification
├── migrations/                 # mirror of the embedded migrations (for psql/manual use)
├── scripts/                    # smoke + e2e shell tests
├── Dockerfile                  # multi-stage distroless build
├── docker-compose.yml          # postgres + redis + app
├── Makefile                    # all common dev tasks
└── .env.example                # template
```
