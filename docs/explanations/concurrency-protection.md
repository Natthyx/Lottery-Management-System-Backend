# Concurrency Protection Strategy

## Overview

Booking an event goes through three layers, each with one job:

| layer | where | job |
|---|---|---|
| 1. per-event FIFO queue | in-process, `internal/booking/gate.go` | make contending requests **wait their turn in arrival order** instead of failing, without holding a DB connection |
| 2. Redis lock | `internal/booking/service.go` | serialise the one request per replica that got through its queue against the other replicas |
| 3. `SELECT ... FOR UPDATE` | the booking transaction | **guarantee correctness**: no oversell and no duplicates, even if layers 1–2 are bypassed or Redis misbehaves |

Measured behaviour (local, single machine) is in [loadtest/REPORT.md](../../loadtest/REPORT.md). How the design got here is in
[loadtest/baseline/FINDINGS.md](../../loadtest/baseline/FINDINGS.md): the previous design was a try-once Redis lock, which
rejected most simultaneous requests for a hot event with `409 event is busy` and could starve retrying clients indefinitely.

## Problem Statement

When many users book the same event at once:
- Without protection, every request reads "9/10 booked", believes it can book, and inserts, so the event is **oversold**.
- With a try-once lock, nothing is oversold, but most requests are refused even though seats are free, and successful bookings per second *fall* as more clients compete.

## Layer 1: In-process FIFO queue per event

```go
// internal/booking/gate.go:29-49
select {
case gt.slot <- struct{}{}:   // got the event's single slot
    return leave, nil
case <-ctx.Done():            // LOCK_WAIT_TIMEOUT elapsed
    return nil, ctx.Err()
}
```

Each event has a channel with capacity 1. Entering sends into it; leaving receives. Go queues blocked senders in arrival
order and hands the slot to the oldest one, so waiters are served first-come first-served and none can be starved by later
arrivals (unit test: `TestEventGates_ServesWaitersInArrivalOrder`). While waiting, a request holds no database connection, so
one hot event cannot drain the pool that every other endpoint needs. Idle gates are removed.

## Layer 2: Owner-safe Redis lock with a bounded wait

**Key:** `lock:booking:event:{event_id}` · **Value:** random 16-byte token · **TTL:** `LOCK_TTL` (5 s)

```go
// internal/booking/service.go:128-150 (abridged)
for {
    acquired, err := s.redis.SetNX(setCtx, key, token, s.lockTTL).Result()
    if acquired { return nil }
    // jittered backoff 2 ms → 50 ms, until LOCK_WAIT_TIMEOUT
}
```

Because layer 1 already serialises requests within a replica, only one request per replica per event ever polls Redis. Each
`SET NX` uses its own short timeout instead of the request's wait deadline, so a deadline can never fire after Redis applied
the `SET` but before the app learns it owns the lock, which would orphan the lock until its TTL.

Release is owner-safe: a Lua compare-and-delete (`service.go:36-41`, run at `:115`) only deletes the key if it still holds this
request's token, so a request whose TTL expired cannot release someone else's lock.

## Layer 3: PostgreSQL row lock (correctness)

```go
// internal/booking/service.go:157-215
SELECT status, capacity FROM events WHERE id = $1 FOR UPDATE   -- row lock
SELECT EXISTS(SELECT 1 FROM bookings WHERE event_id = $1 AND user_id = $2)  -- duplicate?
-- status must be 'open'; COUNT(*) < capacity
INSERT INTO bookings (event_id, user_id) VALUES ($1, $2)       -- UNIQUE(event_id, user_id) backstop
COMMIT
```

The row lock serialises every transaction that books this event, from any replica, whatever happened in Redis. A duplicate
is checked first, so a repeated request is reported as `already_booked` even if the event has since filled or closed.

## Outcomes and status codes

| outcome | status | `code` |
|---|---|---|
| booked | 201 | – |
| already booked by this user | 409 | `already_booked` |
| event full | 409 | `event_full` |
| event not open | 409 | `event_not_open` |
| did not get a turn within `LOCK_WAIT_TIMEOUT` (default 10 s) | **503** + `Retry-After: 1` | `event_busy` (retryable) |
| Redis or Postgres unreachable | 500 | `internal` |

## Timeline: two requests for the last seat

```
Request A                                   Request B (same replica)
    │                                           │
enter queue ✓ (slot free)                   enter queue … waits (FIFO)
Redis SET NX ✓                                  │
BEGIN; SELECT … FOR UPDATE ✓                    │
not a duplicate; open; 9/10 → INSERT            │
COMMIT; release Redis lock; leave queue ──────▶ gets the slot
                                            Redis SET NX ✓
                                            BEGIN; SELECT … FOR UPDATE ✓
                                            10/10 → 409 event_full
```

A request on another replica would wait at layer 2 (backoff) or, if Redis were bypassed, at layer 3 (row lock). In every case
the capacity check runs under the row lock against committed data.

## Failure modes

- **Redis down:** acquiring the lock fails and bookings return 500. This fails closed: it is correct, but bookings are unavailable until Redis returns. (Not load-tested.)
- **Lock TTL expires mid-transaction:** another replica may enter layer 3, but it blocks on the row lock and re-checks capacity afterwards. No oversell.
- **PostgreSQL down:** bookings fail; there is no fallback (correctness over availability).
- **A replica crashes while holding the Redis lock:** other replicas wait until the TTL (5 s) expires. Requests waiting longer than `LOCK_WAIT_TIMEOUT` get `503 event_busy`.

## Performance characteristics

All of one event's bookings pass through one short critical section, so per-event throughput is bounded by that section's
latency, and **waiting time grows linearly with the number of concurrent requests for the same event**. Different events
do not share a queue or a lock. Numbers measured on a laptop are in [loadtest/REPORT.md](../../loadtest/REPORT.md); they
are not production capacity figures.

## Code references

| component | file | lines |
|---|---|---|
| FIFO gate | `internal/booking/gate.go` | 29-49 |
| `BookWithLock` (three layers) | `internal/booking/service.go` | 86-121 |
| Redis acquire with backoff | `internal/booking/service.go` | 128-150 |
| Owner-safe release script | `internal/booking/service.go` | 36-41, 115 |
| Transaction (`FOR UPDATE`, duplicate, capacity, insert) | `internal/booking/service.go` | 157-215 |
| Status codes | `internal/booking/handler.go` | 37-58 |

## Testing

- Unit tests (`-race`): FIFO ordering, timeout and cleanup of the gate (`internal/booking/gate_test.go`).
- Integration tests against real Postgres and Redis (run in CI): 300 concurrent bookings for 150 seats give exactly 150 bookings and
  150 `event_full`, with no busy responses; 10 parallel duplicates give exactly one booking; a held lock produces `event_busy` only after the
  wait budget (`internal/integration/integration_test.go`).
- Load tests with k6 and SQL invariant checks: `loadtest/`.
