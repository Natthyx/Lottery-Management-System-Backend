# Lottery Management System — Backend Engineering Case Study

## Project Overview

**Role:** Senior Backend Engineer  
**Tech Stack:** Go 1.22, PostgreSQL 16, Redis 7, Docker, Kubernetes  
**Architecture:** REST API with defense-in-depth concurrency control  
**Duration:** Production-grade implementation  

This project demonstrates senior-level backend engineering through a provably fair, highly concurrent lottery management system. The system handles event creation, ticket booking with capacity constraints, and cryptographically secure winner selection with full audit trails.

---

## The Business Problem

Organizations running lottery-style events face critical technical challenges:

1. **Fairness Requirements:** Winners must be selected using unpredictable, cryptographically secure randomness — not predictable pseudo-random generators
2. **Concurrency Control:** High-traffic events with limited capacity require precise coordination to prevent overselling
3. **Audit Compliance:** Results must be append-only and tamper-evident for regulatory compliance
4. **Reliability:** The system must maintain correctness even during infrastructure failures

Traditional approaches using single-layer protection or weak randomness create vulnerabilities: overselling tickets, predictable winners, or lost audit trails.

---

## Technical Solution

### High-Level Architecture

The system implements a layered architecture with defense-in-depth principles:

- **API Layer:** Chi HTTP router with structured middleware (auth, rate limiting, logging, recovery)
- **Service Layer:** Domain-driven services (Auth, Events, Bookings, Lottery) with clear boundaries
- **Data Layer:** PostgreSQL for durable storage with ACID guarantees, Redis for distributed coordination
- **Deployment:** Multi-stage Docker builds targeting distroless containers, Kubernetes-ready with health probes

**Key Design Decisions:**
- No payment integration (noted in verification document — not implemented)
- No webhook handlers (not implemented)
- Pure in-memory lottery with database persistence (no external API calls)
- Embedded migrations for deployment simplicity

---

## Critical Engineering Challenges

### Challenge 1: Concurrency Control Without Overselling

**Problem:** When 100 users simultaneously try to book the last 10 spots in an event, naive implementations oversell (e.g., 120 bookings for 100 capacity).

**Solution: Three-Layer Protection**

**Layer 1 — In-Process FIFO Queue (per event):**
- Concurrent requests for the same event wait in arrival order instead of being rejected
- Waiting requests hold no database connection, so one hot event cannot drain the pool
- Waiting is bounded by `LOCK_WAIT_TIMEOUT` (default 10s); only then does the client get `503` with `Retry-After` and code `event_busy`

**Layer 2 — Redis Distributed Lock (cross-replica):**
```go
acquired, err := s.redis.SetNX(setCtx, "lock:booking:event:42", randomToken, s.lockTTL).Result()
```
- Serializes booking requests across API replicas
- Acquired with bounded, jittered backoff rather than failing on first contention
- Released as soon as the booking completes (the TTL is only a safety net for crashed holders)
- Owner-safe release using Lua compare-and-delete prevents one request from unlocking another's section

**Layer 3 — PostgreSQL Row Lock (correctness guarantee):**
```go
SELECT status, capacity FROM events WHERE id = $1 FOR UPDATE
```
- Acquires exclusive database row lock within a transaction
- Duplicate, status and capacity are all checked under this lock
- Guarantees correctness even if layers 1–2 are bypassed (lock expiry, direct database access)

**Why Layers?**
- Redis-only: lock expiry or a bypass → overselling
- PostgreSQL-only: correct, but every waiting request holds a DB connection while blocked on the row
- Combined: the queue and Redis lock absorb contention, PostgreSQL guarantees correctness

**Final outcomes** are explicit: `201` on success, `409` with `already_booked`, `event_full` or `event_not_open`. A duplicate is reported as `already_booked` even if the event has since filled up or closed.

**Result:** Bookings never oversell, including if Redis fails. If Redis is unreachable, bookings fail closed with `500` — correct, but unavailable until Redis recovers.

---

### Challenge 2: Cryptographically Fair Winner Selection

**Problem:** Standard `math/rand` is deterministic — if an attacker knows or guesses the seed, they can predict all lottery winners.

**Solution: crypto/rand + Fisher-Yates**

**Randomness Source:**
```go
jBig, err := rand.Int(rand.Reader, maxBig)
```
- `crypto/rand` reads from `/dev/urandom` (kernel entropy pool)
- Same source used for TLS key generation
- Derived from hardware jitter, interrupt timing, CPU noise
- Computationally infeasible to predict

**Algorithm: Fisher-Yates Shuffle**
```go
for i := n - 1; i > 0; i-- {
    j := crypto_rand_int(0, i+1)
    swap(array[i], array[j])
}
```
- Guarantees uniform distribution (every permutation equally likely)
- O(n) time, O(n) space
- Naive approaches like "sort by random score" have subtle statistical biases

**Testing:**
- 10,000 trial shuffle test validates uniform distribution (within 5% tolerance)
- Verifies no element appears in any position more frequently than expected

**Result:** Provably fair lottery with cryptographic-grade randomness, defensible in audit scenarios.

---

### Challenge 3: Append-Only, Tamper-Evident Audit Trail

**Problem:** Lottery results must be tamper-evident for compliance. Standard database tables allow UPDATE/DELETE.

**Solution: Triggers, a Least-Privilege Role, and a Cryptographic Seal**

**Schema Design:**
```sql
CREATE TABLE lottery_draws (
    id              BIGSERIAL PRIMARY KEY,
    event_id        BIGINT NOT NULL,
    winner_user_id  BIGINT NOT NULL,
    draw_rank       INT NOT NULL,
    entropy_source  TEXT NOT NULL DEFAULT 'crypto/rand+fisher-yates',
    drawn_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- migration 002: fires for every role, including the owner and superusers
CREATE TRIGGER trg_lottery_draws_append_only
    BEFORE UPDATE OR DELETE ON lottery_draws
    FOR EACH ROW EXECUTE FUNCTION forbid_audit_mutation();
CREATE TRIGGER trg_lottery_draws_no_truncate
    BEFORE TRUNCATE ON lottery_draws
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_audit_mutation();
```

**Protection Mechanism:**
- Triggers reject UPDATE, DELETE and TRUNCATE on `lottery_draws` and on `lottery_draw_seals` for every role
- `trg_events_drawn_is_final` freezes `status`, `winner_count` and `capacity` of a drawn event
- The app connects as the least-privilege role `lottery_app`: SELECT/INSERT only on the audit tables, and it owns nothing, so it cannot disable or drop the triggers. Migrations run separately as the owner via `MIGRATION_DATABASE_URL`
- Even compromised application code cannot tamper with past draws — the runtime role has neither the privileges nor the ownership to do so
- Every draw is sealed: a SHA-256 digest over the full ordered outcome is stored in the append-only `lottery_draw_seals` table, returned by `POST /events/{id}/draw`, and logged at warn level (an off-database copy)
- `GET /events/{id}/results` recomputes the digest and reports `integrity: "verified"` or `"mismatch"`

**Residual risk (stated honestly):** someone holding the database owner/superuser credentials can still disable the triggers or use `session_replication_role=replica`. That is not prevented — but it is detectable: in the load test, a committed superuser winner-swap flipped `/results` to `"mismatch"`.

**Atomic Lottery Transaction:**
```
BEGIN TRANSACTION
  FOR UPDATE lock on event
  Validate status = 'closed' and draw_at has passed (else 409 draw_not_due)
  Fetch participants
  Shuffle with crypto/rand
  INSERT all winner records
  INSERT all waitlist records
  INSERT seal (SHA-256 digest)
  UPDATE event status to 'drawn'
COMMIT
```
All steps succeed together or roll back — no partial draws.

**Result:** An append-only, tamper-evident audit trail. The application's database role cannot modify past draws, and any out-of-band change by a privileged database user is exposed by the seal check.

---

### Challenge 4: Graceful Degradation and Observability

**Health vs Readiness:**
- `/health` — Simple liveness check (process is up)
- `/ready` — Readiness check (PostgreSQL + Redis reachable)
- Kubernetes uses both for zero-downtime deployments

**Graceful Shutdown:**
```go
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
srv.Shutdown(shutdownCtx)
```
- SIGTERM triggers graceful drain (30s default)
- In-flight requests complete before process exits
- Prevents dropped requests during rolling updates

**Structured Logging:**
```go
log.Info().
    Str("request_id", requestID).
    Str("method", r.Method).
    Int("status", statusCode).
    Dur("latency", duration).
    Msg("http_request")
```
- Every request logged with latency, status, request ID
- Panic recovery middleware logs stack traces
- JSON output for centralized log aggregation

---

## Database Schema Design

**Event Status Machine:**
```
open → closed → drawn
  └───────────→ cancelled
```
- Bookings only allowed when `status='open'`
- Lottery draw only allowed when `status='closed'`
- Prevents race conditions via state validation

**Key Tables:**
- `users`: bcrypt password hashes (cost 12), role-based access control
- `events`: capacity, winner_count, draw_at, status machine
- `bookings`: UNIQUE constraint on (event_id, user_id) prevents duplicate bookings
- `lottery_draws`: append-only audit log (UPDATE/DELETE/TRUNCATE blocked by triggers)
- `lottery_draw_seals`: one append-only SHA-256 seal per draw

**Indexes:**
- `idx_bookings_event_id` — fast booking lookups per event
- `idx_events_status` — efficient filtering by status
- `idx_draws_event_id` — quick result retrieval

---

## Security Implementation

### Authentication & Authorization

**JWT with HS256:**
```go
token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
jwt.ParseWithClaims(token, claims, keyFunc, 
    jwt.WithValidMethods([]string{"HS256"}))
```
- Explicit algorithm validation prevents `alg:none` attacks
- Tokens signed with 32+ character secret
- 24-hour expiry (configurable)

**Password Security:**
- bcrypt cost 12 (industry standard)
- Timing-safe login: runs bcrypt comparison even when user doesn't exist (prevents enumeration)
- Same error message for "no such user" and "wrong password"

**Rate Limiting:**
- Auth endpoints: 10 requests/min per IP
- Login: additionally 10 attempts per 15 min per account (keyed by email), so attacks spread over many IPs are still capped
- API endpoints: 100 requests/min per IP
- Keyed on the TCP peer IP; `X-Forwarded-For`/`X-Real-IP` are honoured only from `TRUSTED_PROXIES`, so clients cannot spoof their IP to reset the limiter
- `/health` and `/ready` are not rate limited (probes never spend the API budget)

### Input Validation

- Body size limit: 1 MB maximum
- JSON decoder rejects unknown fields (catches typos)
- Email validation using standard library `net/mail`
- Parameterized queries (no SQL injection risk)

---

## Deployment & Infrastructure

### Docker Multi-Stage Build

**Stage 1: Builder**
```dockerfile
FROM golang:1.22-alpine
CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s"
```
- Static binary (no libc dependency)
- Stripped debug symbols for smaller size
- Cached dependency layer for fast rebuilds

**Stage 2: Runtime**
```dockerfile
FROM gcr.io/distroless/static-debian12:nonroot
USER nonroot:nonroot
```
- No shell, no package manager (minimal attack surface)
- Runs as non-root user
- Final image ~20 MB

### Kubernetes Readiness

```yaml
livenessProbe:
  httpGet:
    path: /health
readinessProbe:
  httpGet:
    path: /ready
```
- Rolling updates with zero downtime
- Automatic health-based traffic routing
- Graceful drain on SIGTERM

### Configuration Management

Environment variables for all configuration:
- `DATABASE_URL` — PostgreSQL connection string for the least-privilege runtime role `lottery_app` (SSL required in prod)
- `MIGRATION_DATABASE_URL` — owner connection used only to run migrations
- `REDIS_URL` — Redis connection (TLS support via `rediss://`)
- `JWT_SECRET` — Loaded from Kubernetes secrets
- `MIGRATE_ON_START=true` — Auto-apply migrations on boot

---

## Testing Strategy

### Unit Tests
```bash
go test ./... -race -count=1
```
- Race detector enabled
- Lottery fairness: 10,000-trial statistical validation
- Algorithm coverage: Fisher-Yates correctness proofs
- Concurrency tests with `-race` flag

### Integration Tests
- **Smoke test:** Full workflow (register → login → create event → book → draw → results)
- **`internal/integration`:** runs against real PostgreSQL + Redis — the runtime role cannot modify draws, triggers block even the owner, the seal detects superuser tampering, concurrent bookings queue and never oversell, duplicates yield exactly one booking, concurrent draws produce exactly one result, draws are refused before `draw_at`
- **CI/CD:** GitHub Actions with PostgreSQL + Redis service containers
- Validates real database interactions, not mocks

### Test Coverage
- Core algorithms: 100% (lottery, auth, concurrency)
- Handlers: High coverage via smoke tests
- No coverage target set (quality over metrics)

---

## Performance Characteristics

Measured results: [`loadtest/REPORT.md`](../../loadtest/REPORT.md) (local, single machine). No latency or throughput figures are claimed beyond that report.

**Booking Concurrency:**
- Bookings for one event are serialized (FIFO queue + Redis lock + row lock); contending requests wait their turn instead of being rejected
- Different events can be booked concurrently (lock granularity is per-event)
- PostgreSQL connection pool: 25 max connections per replica; queued requests hold no connection

**Lottery Draw:**
- O(n) shuffle for n participants
- One transaction: rows, seal and status change commit together

**Failure Recovery:**
- Redis failure: bookings fail closed with 500 (no overselling, but bookings unavailable until Redis recovers)
- PostgreSQL down: API returns 503 on readiness probe
- Lock TTL expiry (crashed holder): the next request re-validates capacity under `FOR UPDATE` (no overselling)

---

## Monitoring & Operations

**Observability:**
- Structured JSON logs (zerolog) with request IDs
- Every HTTP request logged with latency, status, IP
- Panic recovery with full stack traces
- Integration-ready for ELK, Datadog, CloudWatch

**Operational Commands:**
```bash
make up          # Start PostgreSQL + Redis
make run         # Run server locally
make test        # Unit tests with race detector
make smoke       # End-to-end smoke test
make build       # Build Docker image
```

**Migration Strategy:**
- Embedded SQL files in `internal/db/migrations/`
- Auto-run on startup when `MIGRATE_ON_START=true`
- Version tracking in `schema_migrations` table
- Idempotent: safe to re-run

---

## Engineering Principles Demonstrated

1. **Defense in Depth:** Multiple layers of protection (FIFO queue + Redis lock + PostgreSQL row lock, JWT validation, rate limiting)
2. **Correctness Over Availability:** System rejects requests rather than silently corrupting data
3. **Tamper-Evidence Where It Matters:** Append-only audit logs enforced by database triggers and a least-privilege runtime role, sealed with a SHA-256 digest
4. **Explicit Over Implicit:** Configuration via environment variables, no hidden defaults
5. **Fail Fast:** Input validation at API boundary, clear error messages
6. **Testability:** Comprehensive unit tests, statistical fairness validation
7. **Production Readiness:** Health probes, graceful shutdown, structured logging, Docker packaging

---

## What This Project Does NOT Include

*Based on comprehensive code audit:*

- **Payment integration:** No Stripe, PayPal, or payment provider implementations
- **Webhooks:** No webhook handlers or signature verification
- **Email notifications:** No email service integration
- **Background jobs:** No queue workers or async job processing
- **Financial ledger:** No double-entry accounting system
- **External API calls:** Pure database-backed implementation
- **Client-facing retry/backoff:** The only backoff is internal (Redis lock acquisition); clients receive `Retry-After` on `503 event_busy`
- **Idempotency keys:** Idempotency enforced via database constraints, not request headers

---

## Technical Skills Demonstrated

**Backend Engineering:**
- Concurrency control (distributed + database locks)
- Cryptographic algorithms (crypto/rand, Fisher-Yates)
- Database design (ACID transactions, row locking, audit trails)
- RESTful API design with consistent error handling
- Authentication & authorization (JWT, bcrypt, RBAC)

**Go Expertise:**
- `pgx` v5 connection pooling and transaction management
- `redis/go-redis` distributed locking with Lua scripts
- `chi` router with composable middleware
- `zerolog` structured logging
- Race detector and concurrent programming

**Infrastructure:**
- Docker multi-stage builds
- Kubernetes deployment patterns (health/readiness probes)
- Environment-based configuration
- Graceful shutdown and zero-downtime deploys

**Security:**
- Timing-safe comparisons
- Algorithm whitelisting (JWT)
- Database-level enforcement (append-only triggers, least-privilege runtime role)
- Input validation and spoof-resistant rate limiting

---

## Results & Benefits

**For the Business:**
- Provably fair lottery results defensible in audits
- No overselling risk regardless of traffic spikes
- Append-only, tamper-evident audit trail for compliance
- Horizontal scalability via stateless API design

**For Operations:**
- Single binary deployment (no runtime dependencies)
- Auto-migrations on startup
- Health probes for Kubernetes orchestration
- Structured logs for centralized monitoring

**For Users:**
- Booking response times: see measured results in [`loadtest/REPORT.md`](../../loadtest/REPORT.md) (local, single machine)
- Transparent draw results with full participant counts and a verifiable integrity seal
- Consistent API responses with clear error messages

---

## Why This Matters

This project demonstrates the ability to:
1. Design systems that remain correct under failure
2. Implement cryptographic algorithms with proper security analysis
3. Build concurrent systems with no data races
4. Deliver production-ready code with observability and testing
5. Make and document architectural trade-offs

The code prioritizes **correctness and clarity** over premature optimization, while maintaining production-grade operational characteristics. Every technical decision is documented and testable.

---

## Code Quality

- **No `panic()` in application code:** All errors returned explicitly
- **Zero external dependencies for core logic:** Uses only Go standard library for lottery algorithm
- **Comprehensive comments:** Every non-trivial function documented
- **Tested:** Unit tests with race detector, integration tests, fairness validation
- **Linted:** Passes `go vet`, `gofmt`, CI checks

---

## Open Source & Portfolio

**Repository Structure:**
```
cmd/server/       — Entry point, dependency wiring
internal/         — Core business logic
  ├── auth/       — JWT, bcrypt, user management
  ├── booking/    — FIFO queue + Redis lock + PostgreSQL row lock
  ├── event/      — CRUD, status machine
  ├── lottery/    — crypto/rand Fisher-Yates, SHA-256 draw seal
  └── middleware/ — Auth, logging, recovery, CORS
migrations/       — SQL schema files
scripts/          — Smoke tests
```

**Documentation:**
- Inline code comments explain "why" not just "what"
- README with quick start, environment variables, security notes
- Architecture diagrams (Mermaid)
- This case study

---

## Technical Contact

Available for technical deep-dives on:
- Concurrency control implementation details
- Cryptographic randomness vs pseudo-randomness
- PostgreSQL transaction isolation levels
- Distributed systems failure modes
- Docker security best practices
- Kubernetes deployment patterns

All claims in this case study are verified against the actual codebase. See `docs/portfolio/claim-verification.md` for line-by-line evidence.
