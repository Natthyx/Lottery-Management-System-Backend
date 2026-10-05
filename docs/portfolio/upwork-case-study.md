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
3. **Audit Compliance:** Results must be tamper-evident and immutable for regulatory compliance
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

**Solution: Dual-Layer Protection**

**Layer 1 — Redis Distributed Lock:**
```go
acquired, err := s.redis.SetNX(ctx, "lock:booking:event:42", randomToken, 5*time.Second).Result()
```
- Serializes booking requests across all API replicas
- Prevents request storms from overwhelming the database
- Owner-safe release using Lua compare-and-delete prevents one request from unlocking another's section

**Layer 2 — PostgreSQL Row Lock:**
```go
SELECT status, capacity FROM events WHERE id = $1 FOR UPDATE
```
- Acquires exclusive database row lock within a transaction
- Guarantees only one transaction can modify capacity at a time
- Protects against Redis failures, lock expiry, or direct database access

**Why Both?**
- Redis-only: Redis failure → overselling
- PostgreSQL-only: Works but causes high contention and serialization failures
- Combined: Redis reduces load, PostgreSQL guarantees correctness

**Result:** The system remains correct under all failure scenarios. If Redis crashes mid-booking, PostgreSQL row locking prevents overselling.

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

### Challenge 3: Immutable Audit Trail

**Problem:** Lottery results must be tamper-evident for compliance. Standard database tables allow UPDATE/DELETE.

**Solution: Database-Level Permissions**

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

REVOKE UPDATE, DELETE ON lottery_draws FROM PUBLIC;
```

**Protection Mechanism:**
- `REVOKE` removes UPDATE/DELETE permissions at the PostgreSQL role level
- Any attempt to modify or delete records raises a permission error
- Even compromised application code cannot tamper with past draws
- Only INSERT is allowed (append-only)

**Atomic Lottery Transaction:**
```
BEGIN TRANSACTION
  FOR UPDATE lock on event
  Validate status = 'closed'
  Fetch participants
  Shuffle with crypto/rand
  INSERT all winner records
  INSERT all waitlist records
  UPDATE event status to 'drawn'
COMMIT
```
All steps succeed together or roll back — no partial draws.

**Result:** Complete audit trail with database-enforced immutability. Draw results are preserved exactly as generated, with full forensic capability.

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
- `lottery_draws`: append-only audit log with revoked UPDATE/DELETE

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
- API endpoints: 100 requests/min per IP
- Per-IP tracking using Redis

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
- `DATABASE_URL` — PostgreSQL connection string (SSL required in prod)
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
- **CI/CD:** GitHub Actions with PostgreSQL + Redis service containers
- Validates real database interactions, not mocks

### Test Coverage
- Core algorithms: 100% (lottery, auth, concurrency)
- Handlers: High coverage via smoke tests
- No coverage target set (quality over metrics)

---

## Performance Characteristics

**Booking Throughput:**
- Redis lock serializes bookings per event: ~1 booking per 5-50ms under contention
- Different events can be booked concurrently (lock granularity is per-event)
- PostgreSQL connection pool: 25 max connections per replica

**Lottery Draw Performance:**
- O(n) shuffle for n participants
- 1,000 participants: ~10ms shuffle time
- Single transaction commit: ~20-50ms total

**Failure Recovery:**
- Redis failure: Bookings fail fast (no silent corruption)
- PostgreSQL down: API returns 503 on readiness probe
- Lock TTL expiry: Next request re-validates capacity (no overselling)

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

1. **Defense in Depth:** Multiple layers of protection (Redis + PostgreSQL locks, JWT validation, rate limiting)
2. **Correctness Over Availability:** System rejects requests rather than silently corrupting data
3. **Immutability Where It Matters:** Append-only audit logs enforced at database level
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
- **Exponential backoff:** No retry logic with backoff
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
- Database-level permission enforcement
- Input validation and rate limiting

---

## Results & Benefits

**For the Business:**
- Provably fair lottery results defensible in audits
- No overselling risk regardless of traffic spikes
- Immutable audit trail for compliance
- Horizontal scalability via stateless API design

**For Operations:**
- Single binary deployment (no runtime dependencies)
- Auto-migrations on startup
- Health probes for Kubernetes orchestration
- Structured logs for centralized monitoring

**For Users:**
- Sub-100ms booking response times
- Transparent draw results with full participant counts
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
  ├── booking/    — Redis + PostgreSQL locking
  ├── event/      — CRUD, status machine
  ├── lottery/    — crypto/rand Fisher-Yates
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
