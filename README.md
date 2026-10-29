# Lottery Management System — Backend

A production-grade event lottery service in Go. Users register, book an event, and become eligible for a cryptographically fair lottery draw. Built on chi, pgx, Redis, and zerolog.

## Architecture

```
Client ─→ chi HTTP router
              │
              ├── Auth      (bcrypt + JWT HS256)
              ├── Events    (CRUD, status machine, RBAC)
              ├── Bookings  (per-event FIFO queue → Redis lock → Postgres FOR UPDATE)
              └── Lottery   (crypto/rand Fisher-Yates, append-only sealed audit log)
                       │              │
                  PostgreSQL         Redis
              (durable store +   (per-event lock,
               audit trail)       owner-safe release)
```

## Key technical decisions

- **`crypto/rand` + Fisher-Yates** for the lottery. `math/rand` is seeded and predictable. `crypto/rand` reads kernel entropy (`/dev/urandom`) — the same source used for TLS keys. Fisher-Yates produces a uniform permutation in O(n).
- **Bookings for a hot event queue instead of failing.** Concurrent requests for one event wait in an in-process FIFO queue (holding no DB connection), then take an owner-safe Redis lock (`SET NX` with a random token, Lua compare-and-delete release) with a bounded backed-off wait, then lock the event row with `SELECT ... FOR UPDATE`. The row lock is what guarantees no oversell, even across replicas or if Redis is bypassed. A request that cannot get its turn within `LOCK_WAIT_TIMEOUT` gets `503 event_busy` with `Retry-After`.
- **The whole draw runs in one transaction**, with the event row locked, status enforced as `closed`, `draw_at` enforced, participants fetched, audit log written and sealed, and status flipped to `drawn` — all-or-nothing.
- **The audit trail is append-only and tamper-evident.** Triggers reject `UPDATE`, `DELETE` and `TRUNCATE` on `lottery_draws` and `lottery_draw_seals` for every role, and a drawn event's result-shaping fields are frozen. The app connects as a least-privilege role (`lottery_app`) that can only read and insert audit rows and cannot disable triggers. Each draw is sealed with a SHA-256 digest (returned, logged, and re-verified by `GET /results`), so even a database superuser who bypasses the triggers is detected.
- **The load is measured, not estimated.** `loadtest/` holds a reproducible k6 + SQL invariant suite and its results ([loadtest/REPORT.md](loadtest/REPORT.md)).

## Quick start

```bash
# 1. Copy and edit env vars (JWT_SECRET must be set!)
cp .env.example .env
# generate a real secret:
echo "JWT_SECRET=$(openssl rand -base64 48)" >> .env

# also set APP_DB_PASSWORD (password of the least-privilege runtime role):
echo "APP_DB_PASSWORD=$(openssl rand -hex 24)" >> .env
# and use it in DATABASE_URL (see .env.example)

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
export APP_DB_PASSWORD=$(openssl rand -hex 24)
export BOOTSTRAP_ADMIN_EMAIL=admin@example.com
export BOOTSTRAP_ADMIN_PASSWORD=ChangeMeRightNow!
docker compose up --build
```

On a **fresh** Postgres volume, `deploy/postgres/initdb/01-app-role.sh` creates the runtime role `lottery_app`; migrations (run as the owner via `MIGRATION_DATABASE_URL`) grant it its privileges. A volume created before this script existed must get the role by hand (same statements) or be recreated (`docker compose down -v`, which deletes its data).

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
| `MIGRATION_DATABASE_URL`   | no       | _empty_          | Schema-owner DSN used only for migrations; empty = use `DATABASE_URL` |
| `APP_DB_PASSWORD`          | compose  | —                | Password of the `lottery_app` runtime role (docker compose)   |
| `DB_MAX_CONNS` / `DB_MIN_CONNS` | no  | `25` / `5`       | pgx pool size                                                 |
| `LOCK_TTL`                 | no       | `5s`             | Booking Redis lock TTL                                        |
| `LOCK_WAIT_TIMEOUT`        | no       | `10s`            | Max time a booking waits for its turn before `503 event_busy` |
| `MAX_WINNERS`              | no       | `100`            | Upper bound for an event's `winner_count`                     |
| `AUTH_RATE_LIMIT_REQUESTS` | no       | `10`             | Per-IP, applied to `/auth/*`                                  |
| `AUTH_RATE_LIMIT_WINDOW`   | no       | `1m`             |                                                               |
| `AUTH_ACCOUNT_RATE_LIMIT_REQUESTS` | no | `10`           | Per-account (email) limit on `/auth/login`, independent of IP |
| `AUTH_ACCOUNT_RATE_LIMIT_WINDOW`   | no | `15m`          |                                                               |
| `API_RATE_LIMIT_REQUESTS`  | no       | `100`            | Per-IP, applied to all routes except `/health`, `/ready`      |
| `API_RATE_LIMIT_WINDOW`    | no       | `1m`             |                                                               |
| `TRUSTED_PROXIES`          | no       | _empty_          | CSV of IPs/CIDRs allowed to set `X-Forwarded-For`/`X-Real-IP`. Empty = always use the TCP peer |
| `CORS_ALLOWED_ORIGINS`     | no       | _empty_          | CSV; use `*` for "any". Empty disables CORS                   |
| `MIGRATE_ON_START`         | no       | `true`           | Apply embedded SQL migrations at boot                         |
| `BOOTSTRAP_ADMIN_EMAIL`    | no       | _empty_          | If set with password, idempotently ensures an admin           |
| `BOOTSTRAP_ADMIN_PASSWORD` | no       | _empty_          | Required only at bootstrap; rotate via promote endpoint       |
| `BOOTSTRAP_ADMIN_NAME`     | no       | `System Admin`   |                                                               |

## API

All responses follow:

```json
{ "success": true|false, "data": ..., "error": "...", "code": "...", "meta": {...} }
```

`code` is a stable machine-readable error identifier on booking and draw errors, e.g. `event_busy` (503, retry after `Retry-After`), `already_booked`, `event_full`, `event_not_open` (409), `already_drawn`, `draw_not_due`, `event_not_closed` (409).

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
| POST   | `/events/{id}/draw`             | admin  | Run the lottery (requires closed and `draw_at` passed) |
| GET    | `/events/{id}/results`          | none   | Winners + waitlist + seal `digest` and `integrity` (`verified`/`mismatch`) |

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
- The lottery can only be drawn when `status='closed'` and `draw_at` has passed.
- Once `drawn`, an event's `status`, `winner_count` and `capacity` cannot change (DB trigger).

## Security notes

- Passwords hashed with bcrypt cost 12.
- JWTs are HMAC-SHA256, validated with an explicit `WithValidMethods` filter to defeat `alg:none` attacks.
- Same error message for "no such user" and "wrong password", plus an equivalent bcrypt comparison in the no-user branch — defends against user enumeration via timing and content.
- Per-IP rate limits on `/auth/login` and `/auth/register` (10/min default) plus a per-account login limit (10 per 15 min), so brute force spread over many IPs is still capped. The per-account limit can be used to temporarily lock a victim out of logging in; that is the usual trade-off.
- The client IP used for rate limiting is the TCP peer unless the peer is in `TRUSTED_PROXIES`; spoofed `X-Forwarded-For`/`X-Real-IP` headers are ignored.
- Audit trail: append-only by trigger for every role, runtime role has no `UPDATE`/`DELETE`/`TRUNCATE`, each draw sealed with a SHA-256 digest. **Residual risk:** whoever holds the owner/superuser credentials (migrations only) can still disable triggers; the seal makes that detectable, and keeping the digest outside the database (it is logged at `warn`) is what lets you prove it.
- Postgres and Redis are published on `127.0.0.1` only in docker compose.
- `nosniff`, `no-store`, `X-Frame-Options: DENY` response headers.
- 1 MiB request body cap protects against oversize-body DoS.
- Distroless container runs as `nonroot`.
- Rate-limit counters are in memory per replica: with N replicas the effective limit is N × the configured one.

## Migrations

Embedded SQL files in `internal/db/migrations` are applied on boot when `MIGRATE_ON_START=true` (default). The runner records applied versions in the `schema_migrations` table and never re-runs a recorded migration. Each migration runs in its own transaction.

To add a new migration, drop a `NNN_name.sql` file into `internal/db/migrations`. The number is the version; higher numbers run later. A migration that adds a table must also grant the `lottery_app` role what it needs (see `002_harden_audit.sql`).

## Testing

```bash
make test            # unit tests with -race (internal/integration also runs when
                     # TEST_DATABASE_URL and TEST_REDIS_URL are set, as in CI)
make test-algorithm  # lottery fairness tests only
make test-cover      # HTML coverage report

# Against a running server:
make run             # in one shell
make smoke           # in another
make e2e             # fuller end-to-end script
```

## Production checklist

- [ ] Provide a real `JWT_SECRET` from a secret manager (≥32 chars).
- [ ] Use a managed Postgres with `sslmode=require` in `DATABASE_URL`; connect as a non-owner runtime role, migrate as the owner (`MIGRATION_DATABASE_URL`).
- [ ] Ship logs (they contain each draw's seal digest) to storage the database admins cannot rewrite.
- [ ] Set `TRUSTED_PROXIES` to your load balancer's addresses, and nothing broader.
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
│   ├── booking/                # queued + locked booking flow
│   ├── config/                 # env validation
│   ├── db/                     # pgx pool, redis client, embedded migration runner
│   │   └── migrations/         # *.sql files embedded into the binary
│   ├── event/                  # event CRUD + status transitions
│   ├── httpx/                  # JSON envelope + decode helpers
│   ├── integration/            # tests against real Postgres + Redis (CI)
│   ├── lottery/                # crypto-fair draw algorithm + sealed audit log
│   ├── middleware/             # JWT, RBAC, client IP, security headers, logger, body limit, CORS
│   ├── models/                 # request/response/domain types
│   └── pgerr/                  # Postgres error classification
├── migrations/                 # mirror of the embedded migrations (for psql/manual use)
├── deploy/postgres/initdb/     # creates the least-privilege runtime role
├── loadtest/                   # k6 load tests, SQL invariants, results and reports
├── scripts/                    # smoke + e2e shell tests
├── Dockerfile                  # multi-stage distroless build
├── docker-compose.yml          # postgres + redis + app
├── Makefile                    # all common dev tasks
└── .env.example                # template
```
