# Findings

Code references are to commit `8cd482e` (see [results/env/environment_raw.txt](results/env/environment_raw.txt)).
No application code was changed. Nothing listed here was fixed.

---

## Phase 0: answers from reading the code

### P0-1. `POST /events/{id}/book` responses

All responses use the envelope `{"success": bool, "data": ..., "error": "..."}` ([internal/httpx/respond.go:15-25](../internal/httpx/respond.go#L15-L25)).
The handler is [internal/booking/handler.go:25-54](../internal/booking/handler.go#L25-L54); error values are in [internal/booking/service.go:21-28](../internal/booking/service.go#L21-L28).

| case | status | `error` body | where |
|---|---|---|---|
| success | **201** | none; `data.message` = `"booking confirmed — you are now eligible for the lottery"` | handler.go:51-53 |
| Redis lock not acquired | **409** | `event is busy, please retry` | service.go:78-84 → handler.go:39-41 |
| duplicate (same user, same event) | **409** | `you have already booked this event` | UNIQUE (event_id, user_id) [001_init.sql:46](../internal/db/migrations/001_init.sql#L46) → `pgerr.IsUnique` service.go:143-145 |
| event not open (closed, drawn or cancelled) | **409** | `event is not open for booking (current status: <s>)` | service.go:124-126 |
| event full | **409** | `event is fully booked (<booked>/<capacity>)` | service.go:128-136 |
| event does not exist | 404 | `event not found` | service.go:117-120 |
| bad id | 400 | `invalid event id` | handler.go:26-29 |
| missing or invalid JWT | 401 | `missing Authorization header` / `invalid or expired token` | [internal/middleware/auth.go:58-78](../internal/middleware/auth.go#L58-L78) |
| Redis error, DB error | 500 | `could not create booking` | service.go:79-81, handler.go:46-47 |
| rate limited | 429 | plain text from `httprate` | [cmd/server/main.go:100](../cmd/server/main.go#L100) |

Consequences that matter under load:

* **The lock does not wait.** `SETNX` either succeeds or the request fails immediately with 409 busy
  (service.go:78-84). There is no queueing and no server-side retry. Under contention on one event, most
  simultaneous requests fail with busy (measured in L2a).
* **Four different 409s, one status code.** A client can only tell "retry" (busy) from "final"
  (duplicate/full/closed) by string-matching the error message.
* **Check order is lock → status → capacity → insert.** A duplicate request on a full event gets
  "fully booked", and on a closed event "not open", rather than "already booked". Duplicates are not
  pre-checked; the database's unique constraint is what rejects them.

### P0-2. Capacity, winners and waitlist

* **Capacity exists and is required:** `events.capacity INT NOT NULL CHECK (capacity > 0)` ([001_init.sql:26](../internal/db/migrations/001_init.sql#L26)).
  It is enforced **only in application code**: `SELECT ... FOR UPDATE` on the event row, then `COUNT(*)` of bookings,
  then insert (service.go:113-140). No DB constraint or trigger enforces it. The row lock serialises bookings for one
  event even without Redis.
* **Winners relative to capacity:** `winner_count <= capacity` (CHECK at [001_init.sql:34](../internal/db/migrations/001_init.sql#L34), validated at
  [internal/event/service.go:63-65](../internal/event/service.go#L63-L65)). Every booking is an entrant. The draw shuffles all entrants and takes the first
  `min(winner_count, entrants)` as winners ([internal/lottery/algorithm.go:74-92](../internal/lottery/algorithm.go#L74-L92)).
* **Waitlist order:** the rest of the **random shuffle**, ranks `winner_count+1 .. n`
  ([internal/lottery/service.go:146-153](../internal/lottery/service.go#L146-L153)). It is not ordered by booking time. Entrants are read
  `ORDER BY booked_at` (service.go:83-87), but that only fixes the input order of the shuffle. L5 checks that it
  creates no bias.

### P0-3. Two `POST /events/{id}/draw` at the same time

`RunDraw` locks the event row with `SELECT ... FOR UPDATE` inside one transaction
([internal/lottery/service.go:57-62](../internal/lottery/service.go#L57-L62)). The second request blocks until the first commits, then reads
`status = 'drawn'` and returns **409 `event has already been drawn`** (service.go:74-75, handler.go:35-39).
`UNIQUE (event_id, draw_rank)` ([001_init.sql:61](../internal/db/migrations/001_init.sql#L61)) is a second line of defence. Measured in L4.

### P0-4. Is `lottery_draws` protected?

* **No trigger, rule or RLS**: only `REVOKE UPDATE, DELETE ON lottery_draws FROM PUBLIC` ([001_init.sql:66-68](../internal/db/migrations/001_init.sql#L66-L68)).
  Catalog output: [results/phase0/db_roles_and_privileges.txt](results/phase0/db_roles_and_privileges.txt).
* **The app connects as `lottery`** (`DATABASE_URL` in [docker-compose.yml:40](../docker-compose.yml#L40), `POSTGRES_USER` default at line 6). The
  official Postgres image makes `POSTGRES_USER` a **superuser**, and that role also **owns** the table, since it ran the migrations.
* **PUBLIC never had UPDATE/DELETE**, so the REVOKE removes nothing. Postgres grants table privileges only to the
  owner by default. See F1 for what the tamper attempts actually did.

### P0-5. Pool sizes, lock TTL, timeouts in effect

| setting | value in effect | source |
|---|---|---|
| pgx pool | MaxConns **25**, MinConns 5, MaxConnLifetime 30 m, MaxConnIdleTime 5 m | hard-coded, [internal/db/postgres.go:23-27](../internal/db/postgres.go#L23-L27); overrides any `pool_max_conns` in the URL |
| Redis pool | PoolSize **20**, MinIdleConns 5 | [internal/config/config.go:137-144](../internal/config/config.go#L137-L144) |
| `LOCK_TTL` | **5 s** | default at config.go:95; not forwarded by docker-compose.yml, so always the default in compose |
| HTTP server | ReadTimeout 10 s, WriteTimeout 30 s, IdleTimeout 120 s, ShutdownTimeout 30 s | config.go:81-84 |
| per-request timeout | 30 s (`chi` `middleware.Timeout`) | [cmd/server/main.go:96](../cmd/server/main.go#L96) |
| Postgres `max_connections` | 100 | [results/env/environment_raw.txt](results/env/environment_raw.txt) |

The env vars actually set in the container are listed (secrets redacted) in [results/env/environment_raw.txt](results/env/environment_raw.txt).

---

## Findings from the tests

Severity is my judgement. "Owner decision needed" means I deliberately did not change anything.

### F1. HIGH: the audit log `lottery_draws` is not immutable (owner decision needed)

**What happens.** The app's database role can `UPDATE`, `DELETE` and `TRUNCATE` audit rows, including
swapping who won. Evidence: [results/l6/tamper.txt](results/l6/tamper.txt) (all attempts inside
transactions that were rolled back; digest before = after, lines 1-5 and 144-149).

| attempt | as app role `lottery` (superuser + owner) | as a non-superuser **owner** | as non-owner with SELECT, INSERT |
|---|---|---|---|
| `UPDATE` one row | **succeeds** (`UPDATE 1`) | **succeeds** (`UPDATE 1`) | blocked: `42501 permission denied` |
| `DELETE` one row | **succeeds** (`DELETE 1`) | **succeeds** (`DELETE 1`) | blocked: `42501 permission denied` |
| `TRUNCATE` | **succeeds** | not tried | not tried |
| swap rank-1 winner with last-ranked entrant | **succeeds** (`UPDATE 2`; new rank-1 user shown) | not tried | not tried |

Mechanism: no trigger, no rule, no RLS ([results/phase0/db_roles_and_privileges.txt](results/phase0/db_roles_and_privileges.txt)).
The only "protection" is `REVOKE UPDATE, DELETE ... FROM PUBLIC`
([001_init.sql:66-68](../internal/db/migrations/001_init.sql#L66-L68)), and it does nothing. PUBLIC never had those
privileges. The control table in section E of the tamper file was never REVOKEd, yet it denies the non-owner
in exactly the same way. The non-owner is blocked by Postgres's default privileges, not by the migration.

**Docs that this contradicts:** `docs/AUDIT_SUMMARY.md:16` ("Immutable audit trails (database-level permission enforcement)"),
`:105`, `:535`, `:659` ("Even compromised code can't change history"); `docs/DOCUMENTATION_INDEX.md:128,250`;
the migration comment at `001_init.sql:66-67` ("any attempt raises a real permission error").

**Reproduce:** `loadtest/run.sh init l6`, or by hand on any drawn event:
```sql
-- psql as the DATABASE_URL role
BEGIN; UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE id = (SELECT min(id) FROM lottery_draws); ROLLBACK;  -- UPDATE 1
```

**Not fixed.** Options for the owner: run the app as a separate non-owner, non-superuser role with only
`SELECT, INSERT` on `lottery_draws`; and/or add a `BEFORE UPDATE OR DELETE` trigger that raises, plus
`BEFORE TRUNCATE`. Note that a superuser or the owner can drop either control, so true immutability needs
something external, such as hash-chaining rows or shipping them off-box.

### F2. HIGH (security): the rate limiter can be bypassed with a spoofed `X-Forwarded-For` / `X-Real-IP`

`chiMiddleware.RealIP` ([cmd/server/main.go:93](../cmd/server/main.go#L93)) rewrites `RemoteAddr` from client-supplied
headers before `httprate.LimitByIP` runs ([main.go:100](../cmd/server/main.go#L100) and [:111](../cmd/server/main.go#L111)).
In L8, once a client had been limited to 429, the same client got 200 (API) and 401 (login, i.e. not
rate-limited) again just by adding a fake header: sections A3 and B2 of [results/l8/run1.txt](results/l8/run1.txt),
[run2.txt](results/l8/run2.txt) and [run3.txt](results/l8/run3.txt). That defeats the login brute-force protection
described at main.go:108-109.

Reproduce (default limits): send 11 × `curl -X POST -d '{}' -H 'Content-Type: application/json' :8080/auth/login`, and the 11th
returns 429. Then `curl -H 'X-Real-IP: 198.51.100.1' -X POST -d '{}' ...` returns 401 again.
Only trust these headers behind a proxy that overwrites them. **Not fixed.**

### F3. MEDIUM (performance and design): the booking lock is try-once, so hot-event goodput falls as concurrency rises

`SETNX` without waiting (service.go:78-84) means concurrent requests for one event mostly get 409 busy:

* N simultaneous single-shot users: 5 of 200 [range 4-6] and 19 of 1000 [9-20] got a booking.
  All others got 409 busy ([results/l2/single_noretry_n200](results/l2/single_noretry_n200), [single_noretry_n1000](results/l2/single_noretry_n1000)).
* Closed-loop sweep without retry: successful bookings/s on the hot event were 967 at 1 VU and 109 at
  200 VUs, while total request rate rose to about 19k/s, nearly all busy ([results/l2/sweep_noretry](results/l2/sweep_noretry), REPORT L2c).

Every one of these was handled correctly: no oversell, no duplicates, and DB rows = 201s in every run. It is a
throughput and fairness characteristic, not a correctness bug. A blocking lock with a short wait, or relying on the
row lock alone (it already serialises the transaction), would behave differently. That was **not tested**.

### F4. MEDIUM: under heavy contention, retrying clients can be starved, and seats can stay empty while users are turned away

* 1000 clients each retrying busy up to 200 times (5-25 ms jitter): **556-591 of 1000 users gave up**
  in about 8.7 s, and only **409-444 of 500 seats** were filled ([results/l2/retry200_capacity_half_n1000](results/l2/retry200_capacity_half_n1000)).
  So users were refused while capacity remained.
* Retrying without a cap: all 500 seats filled and the other 500 users got "full", but the median user needed
  256 attempts and 11.3 s end-to-end ([results/l2/retry_unbounded_capacity_half_n1000](results/l2/retry_unbounded_capacity_half_n1000)).
* The lock has no queue and no fairness. In the superseded first L3 run, a client that retried with no backoff
  left 37-50 of 200 users with zero bookings after 200 attempts each ([results/superseded/l3_first_attempt](results/superseded/l3_first_attempt)).

### F5. LOW (API design): one status code for four outcomes, and check order hides duplicates

409 means busy (retryable), duplicate, full, or not open (final). Clients must string-match `error` to know
whether to retry. Because capacity is checked before the insert (service.go:124-145), a duplicate booking on a
full event returns "fully booked", not "already booked". This was inferred from code; it was not separately measured.

### F6. Spec vs code: `lottery_draws` holds one row per entrant, not one row per draw

`RunDraw` inserts a row for every winner and every waitlisted entrant (draw_rank 1..n; [lottery/service.go:139-153](../internal/lottery/service.go#L139-L153)).
After the L4 draws there were 1000 rows for one draw. The L4 "exactly one draw" check was adapted accordingly: one
distinct `drawn_at`, ranks exactly 1..n, n = bookings, and every entrant exactly once ([sql/l4_invariants.sql](sql/l4_invariants.sql)).

### F7. Spec vs code: "exactly one booking per user" (L3) holds only if clients retry the busy 409

With 5 parallel identical requests and **no** client retry, most users get 0 bookings (L3 no-retry:
187-196 of 200 and 979-990 of 1000 users ended with none), because every request can lose the lock. The
property that holds unconditionally is **at most one** (no user had more than one in any run). With retry, it is
exactly one: [results/l3/dup5_retry_n200](results/l3/dup5_retry_n200), [dup5_retry_n1000](results/l3/dup5_retry_n1000).

### F8. LOW: `/health` and `/ready` share the general rate limit

The global limiter is installed before those routes (main.go:100-104). At the default 100/min per IP, a load balancer
or orchestrator probing from a single IP spends API budget, and enough clients behind that IP can get the probes
429'd. In L8 the readiness probe is visible in the API count; that is why A shows 99 × 200 after one probe.

### F9. LOW: configuration that is read but unused, or not forwarded

* `MAX_WINNERS` is parsed ([config.go:96](../internal/config/config.go#L96)) but never used anywhere.
* docker-compose.yml forwards neither `API_RATE_LIMIT_WINDOW`/`AUTH_RATE_LIMIT_WINDOW` nor `LOCK_TTL`, `READ_TIMEOUT`, etc.
  So under compose they are always the code defaults; the load-test override had to add the window variables.
* pgx pool size is hard-coded to 25 (postgres.go:23) and silently overrides `pool_max_conns` in `DATABASE_URL`.

### F10. LOW: a draw does not check `draw_at`

`RunDraw` never reads `draw_at` (lottery/service.go:47-179). An admin can close and draw an event the moment
it is created; L4 and L5 do exactly that with `draw_at` 7 days or 24 h in the future. Is this intended? (Owner question.)

### F11. Docs vs measurements

* `docs/explanations/concurrency-protection.md:135`: "Lock TTL of 5s means max 1 booking per event per 5s under contention".
  This is not what happens. The lock is released on completion. Measured: about 1000 bookings/s on one event at concurrency 1
  (REPORT L2c).
* `docs/AUDIT_SUMMARY.md:428-439` latency table (labelled as estimates): "Booking (no contention) ~20ms p50 / ~50ms p99".
  Measured on this machine at 1 VU: 0.73 ms p50 / 2.33 ms p99 (sweep_retry c1). The "high contention" estimate depends
  entirely on client behaviour (see F3, F4). The table should cite measurements or be removed.
* `docs/AUDIT_SUMMARY.md:653`: "Even if Redis fails completely, the system stays correct". Per code, a Redis error makes
  every booking return 500 (service.go:79-81), which is correct but unavailable. **Not tested** here.

### F12. Observations about the harness or environment (not app bugs), recorded for transparency

* **First L3 run invalid (harness bug):** the duplicates retry path had no backoff. Kept in
  [results/superseded/l3_first_attempt](results/superseded/l3_first_attempt) with an explanation. L3 was re-run after the fix.
* **One transport EOF in the L5 API helper:** `PUT /events/536/close: EOF` (no HTTP response). The event stayed `open`,
  the app had 0 restarts and logged nothing at warn. Across all k6 runs (millions of requests, keep-alive) no
  transport error, timeout or 5xx occurred outside L7 at 3000 req/s and L9 after SIGTERM. Cause unknown; Docker Desktop's
  port forwarding is one candidate. Helper now retries safely and counts these (0 in the final runs).
  [results/superseded/l5_first_attempt](results/superseded/l5_first_attempt).
* **First L8 run had a probe race:** right after an app restart, the first 22 requests got no response (`000`), because
  Docker Desktop's port forward lags the app's "listening" log line. Fixed by probing `/health` first.
  [results/superseded/l8_probe_race](results/superseded/l8_probe_race).
* **First L7 run did not classify failures** (raw log format dropped k6's error text), so it was re-run with status-class counters.
  [results/superseded/l7_without_status_classes](results/superseded/l7_without_status_classes).
* **L2 was re-run** once so that all L2 data comes from the final harness (with the give-up counter).
  [results/superseded/l2_pre_gaveup_counter](results/superseded/l2_pre_gaveup_counter). Invariants passed in both.
* `docker kill -s TERM` was **not** followed by an automatic restart despite `restart: unless-stopped`; Docker treats kill
  as a manual stop. Relevant only if someone relies on that policy after a SIGTERM.
* **Statistics caveat:** the textbook Pearson chi-square on win counts is far too lenient here (in 2000 simulated fair
  experiments it rejected at 0.05 only 0.05% of the time instead of 5%). Quote the variance-adjusted statistic or the rank-1 test,
  not the textbook one. [results/l5/algorithm_calibration_2000x5000.txt](results/l5/algorithm_calibration_2000x5000.txt).
