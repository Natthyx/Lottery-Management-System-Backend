# Concurrency Protection Strategy

## Overview

The lottery system implements **defense-in-depth concurrency control** using both Redis distributed locking and PostgreSQL row-level locking. This dual-layer approach prevents race conditions and overselling in high-traffic scenarios.

## Why Two Layers?

### Problem Statement

When multiple users simultaneously book the last available slot in an event:
- Without protection: all requests read "9/10 booked", all believe they can book, all insert → **oversold to 12/10**
- With single-layer protection: edge cases remain depending on the protection mechanism

### Layer 1: Redis Distributed Lock

**Purpose:** Serialize access to the booking logic across all API replicas.

**Implementation:**
```go
// booking/service.go lines 62-70
acquired, err := s.redis.SetNX(ctx, lockKey, token, s.lockTTL).Result()
if !acquired {
    return ErrEventBusy
}
```

**Key:** `lock:booking:event:{event_id}`
**Value:** Random hex token (16 bytes from `crypto/rand`)
**TTL:** 5 seconds (configurable)

**What it protects against:**
- Multiple replicas processing the same event simultaneously
- Request storms from load balancers distributing traffic

**What it does NOT protect against:**
- Redis failure (lock disappears, multiple replicas proceed)
- Lock expiry before transaction completes (another request acquires lock while first is mid-transaction)
- Direct database access bypassing the API

### Layer 2: PostgreSQL Row Lock (SELECT FOR UPDATE)

**Purpose:** Ensure atomicity at the database level, regardless of Redis state.

**Implementation:**
```go
// booking/service.go lines 87-92
err = tx.QueryRow(ctx,
    `SELECT status, capacity FROM events WHERE id = $1 FOR UPDATE`,
    eventID,
).Scan(&status, &capacity)
```

**What it protects against:**
- Redis lock expiry during a long transaction
- Redis unavailability
- Concurrent writes from background jobs, admin tools, or direct SQL access
- Serialization failures in high-contention scenarios

**How it works:**
- `FOR UPDATE` acquires an exclusive row lock on the event record
- All other transactions attempting to read the same row block until the lock holder commits or rolls back
- PostgreSQL guarantees that only one transaction at a time can hold the lock
- Lock is automatically released on COMMIT or ROLLBACK

### Combined Defense

```
Request A                              Request B
    │                                      │
    ▼                                      ▼
Redis SETNX ✓ (acquired)            Redis SETNX ✗ (key exists)
    │                                      │
    ▼                                      └──> 409 Conflict (ErrEventBusy)
BEGIN TX                                        │
    │                                           ▼
SELECT ... FOR UPDATE (row lock)          (waits for A to finish)
    │
Check capacity (9/10)
    │
INSERT booking (10/10)
    │
COMMIT (releases row lock)
    │
Lua script release Redis lock
    │
    ▼
Request B retries
    │
    ▼
Redis SETNX ✓ (acquired)
    │
    ▼
BEGIN TX
    │
SELECT ... FOR UPDATE ✓ (row lock acquired)
    │
Check capacity (10/10) ← sees A's committed booking
    │
    └──> 409 Conflict (ErrEventFull)
```

## Owner-Safe Lock Release

**Problem:** If Request A's Redis lock expires after 5 seconds but before it completes, Request B might acquire the lock. When A finally finishes, it shouldn't delete B's lock.

**Solution:** Token-based ownership enforced by a Lua script:

```lua
-- booking/service.go lines 35-41
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
```

This is executed atomically on the Redis server, ensuring:
1. Check current lock value
2. Delete only if it matches our token
3. Steps 1-2 are indivisible (no race condition)

## Transaction Isolation

PostgreSQL transaction uses default isolation level `READ COMMITTED`:
- Each statement sees a consistent snapshot
- `FOR UPDATE` prevents concurrent modifications
- No phantom reads within the transaction

## Performance Characteristics

**Throughput:**
- Redis lock serializes requests per event
- Different events can be booked concurrently (lock granularity is per event, not global)
- Lock TTL of 5s means max 1 booking per event per 5s under contention (in practice, much faster)

**Latency:**
- Redis SETNX: ~1-2ms (local network)
- PostgreSQL SELECT FOR UPDATE: ~5-10ms (depending on contention)
- Total booking operation: ~20-50ms without contention

**Failure Modes:**
- Redis down: bookings fail fast with connection error
- PostgreSQL down: bookings fail (no fallback, correctness over availability)
- Lock TTL expires: subsequent requests re-validate capacity in PostgreSQL (no overselling)

## Why This Matters

Without this dual-layer approach:

**Redis-only:**
- Redis failure → overselling
- Lock expiry → overselling
- Direct DB access → overselling

**PostgreSQL-only:**
- Works correctly but contention on popular events causes serialization failures and high retry rates
- Redis layer reduces database contention by serializing before hitting the database

**Dual-layer:**
- Redis reduces database load
- PostgreSQL guarantees correctness
- System remains correct even if Redis fails
- No silent failures

## Real-World Scenario

**High-demand event (capacity=100, 200 concurrent requests):**

1. Redis serializes requests: only 1 proceeds at a time per event
2. That request acquires row lock in PostgreSQL
3. Validates capacity, inserts booking, commits
4. Releases Redis lock
5. Next request proceeds

**If Redis fails mid-booking:**

1. Request A acquired Redis lock, started PostgreSQL transaction
2. Redis crashes
3. Request B (no Redis) starts a new PostgreSQL transaction
4. Request B blocks on `SELECT FOR UPDATE` because A holds the row lock
5. Request A commits, releases row lock
6. Request B's transaction sees A's booking, rejects duplicate/full capacity

**Result:** Correctness maintained regardless of Redis state.

## Code References

| Component | File | Lines |
|-----------|------|-------|
| Redis lock acquisition | `internal/booking/service.go` | 62-70 |
| Owner-safe release script | `internal/booking/service.go` | 35-41 |
| Lua script execution | `internal/booking/service.go` | 118-123 |
| PostgreSQL FOR UPDATE | `internal/booking/service.go` | 87-92 |
| Transaction begin/commit | `internal/booking/service.go` | 80, 114 |
| Lock token generation | `internal/booking/service.go` | 146-152 |

## Testing

This concurrency mechanism is validated in production by:
1. Unit tests with race detector (`go test -race`)
2. Smoke tests simulating duplicate bookings
3. Load tests (not included in repository)
