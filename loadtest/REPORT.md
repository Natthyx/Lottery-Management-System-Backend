# Load-test report: Lottery Management System backend (after fixes)

**All numbers are local, single machine**: Apple M2 (8 cores, 8 GB). k6, the app, Postgres 16 and Redis 7
shared the laptop, with traffic going through Docker Desktop's port forwarding. See [results/ENVIRONMENT.md](results/ENVIRONMENT.md).
They describe this setup only. They are not production capacity.

* App code: commit `6ee3b75` (fixes for every finding in [baseline/FINDINGS.md](baseline/FINDINGS.md)). The run against the code
  before the fixes (`8cd482e`) is [baseline/REPORT.md](baseline/REPORT.md); same machine, same harness, same scenarios.
* Each measured scenario ran **3 times** after a warm-up. Cells read `median [min – max]` across the 3 runs, from
  [results/AGGREGATE.md](results/AGGREGATE.md) (computed by `cmd/aggregate` from the raw k6 JSON cited in each table).
* Rate limits were raised for every scenario except L8 (`LOG_LEVEL=warn`). The app otherwise ran exactly as `docker-compose.yml`
  configures it, connected as the least-privilege `lottery_app` role.
* Final runs: 51 k6 runs, 1,443,330 HTTP requests. Transport errors or timeouts occurred only in L9 after SIGTERM and in L7 at
  3000 req/s. **Zero 503 `event_busy`, 5xx, 4xx or 429 responses outside L8.** ([results/totals.txt](results/totals.txt))
* Two attempts were interrupted by Docker Desktop's port forwarder running out of ephemeral ports (not the app). The evidence,
  root cause and harness fix are in [results/superseded/README.md](results/superseded/README.md).
* Reproduce with `loadtest/run.sh`.

## Before vs after

| | baseline `8cd482e` | after `6ee3b75` | evidence (after) |
|---|---|---|---|
| 1000 users book one event at the same moment (no retry), capacity 1000 | **19** [9 – 20] booked, 981 "busy" | **1000** [1000 – 1000] booked, 0 busy | [l2/single_noretry_n1000](results/l2/single_noretry_n1000) |
| 1000 retrying clients (≤200 attempts) for 500 seats | 421 booked, **579 gave up**, seats left empty, 8.65 s | **500 booked + 500 "full"**, 1 attempt each, 0.93 s | [l2/retry200_capacity_half_n1000](results/l2/retry200_capacity_half_n1000) |
| successful bookings/s on one hot event, 200 VUs closed loop, no retry | **109**/s | **1964**/s | [l2/sweep_noretry](results/l2/sweep_noretry) |
| 5 parallel duplicates per user, no client retry: users with exactly one booking | 10–21 of 1000 | **1000 of 1000** (3/3 runs) | [l3/dup5_noretry_n1000](results/l3/dup5_noretry_n1000) |
| app DB role can UPDATE/DELETE/TRUNCATE draws | **yes** | **no** (`42501 permission denied`) | [l6/tamper.txt](results/l6/tamper.txt) |
| owner/superuser can UPDATE/DELETE/TRUNCATE draws | yes | **no** (trigger rejects; a superuser bypass is *detected*) | [l6/tamper.txt](results/l6/tamper.txt), [l6/tamper_detection.txt](results/l6/tamper_detection.txt) |
| spoofed `X-Forwarded-For` / `X-Real-IP` resets the rate limit | **yes** | **no** (stays 429) | [l8/run1.txt](results/l8/run1.txt) |
| brute force on one account spread over many IPs | no per-account limit | **429 after 10** attempts | [l8/proxy_run1.txt](results/l8/proxy_run1.txt) |
| `/health`, `/ready` and the API rate limit | probes counted against it (F8) | **not limited**: 200 while the API limit is exhausted | [l8/run1.txt](results/l8/run1.txt) |
| booking latency at 1 VU, p50 / p99 | 0.73 / 2.33 ms | 0.77 / 2.37 ms | [l2/sweep_retry](results/l2/sweep_retry) c1 |
| reads at 1500 req/s: p99, `/results` p99, errors | 64.1 ms, 91.2 ms, 0 | 73.7 ms, 106 ms, 0 | [l7/rate1500](results/l7/rate1500) |

The trade-off: requests that used to be refused instantly now **wait their turn**. Under heavy contention for one event,
latency grows with queue depth (p99 196 ms at 200 VUs, about 0.6 s for 1000 simultaneous users) instead of most users getting "busy".
Read latency is slightly worse at 1500 req/s. `/results` now runs one extra query and recomputes a SHA-256 over all rows on
every call (here 1000 rows, 168 KB), which is a plausible cause, but the cause was not isolated. 3000 req/s still saturates the laptop (L7).

## Invariants at a glance

| invariant | runs | result | evidence |
|---|---|---|---|
| no duplicate (event, user) booking | 39 booking runs (L2, L2u, L3, L9) | **PASS 39/39** | `sql_run*.txt` in each scenario dir |
| no event over capacity | 39 | **PASS 39/39** | same |
| rows in `bookings` = 201 responses k6 received | 39 | **PASS 39/39** | same |
| capacity filled exactly when contended (N/2 seats) | 12 (L2b bounded + unbounded, n200 + n1000) | **PASS 12/12** | [l2](results/l2) |
| exactly one booking per user, 5 parallel duplicates, **with or without** client retry | 12 | **PASS 12/12** | [l3](results/l3) |
| 20 concurrent draws → exactly one draw and one seal; status `drawn`; counts right; DB = `/results` = 200 body; integrity `verified` | 3 | **PASS 3/3** (8 SQL checks + 2 diffs + seal checks each) | [l4/draw20_n1000](results/l4/draw20_n1000) |
| runtime role cannot modify, truncate, or un-trigger the audit tables | 1 | **PASS** | [l6/tamper.txt](results/l6/tamper.txt) |
| owner/superuser modifications rejected by trigger | 1 | **PASS** | same |
| a superuser who bypasses triggers is detected | 1 | **PASS** (`integrity: mismatch` + error log) | [l6/tamper_detection.txt](results/l6/tamper_detection.txt) |
| rate limits enforce defaults; spoofed headers do not bypass them; per-account limit holds across IPs | 3 + 3 | **PASS 6/6** | [l8](results/l8) |
| SIGTERM: all in-flight requests complete, no orphaned bookings | 3 | **PASS 3/3** | [l9](results/l9) |

---

## L1 Setup (not measured)

1000 users registered and logged in once (bcrypt cost 12, 8 workers) in 87.1 s ([results/l1/setup_users.txt](results/l1/setup_users.txt)).

## L2a Hot event, every user books once at the same moment (no client retry)

| N | 201 | 503 busy | latency p50 / p95 / p99 (ms) | wall time (s) | raw |
|---|---|---|---|---|---|
| 200 | **200** [200 – 200] | 0 | 101 / 126 / 130 | 0.17 [0.15 – 0.20] | [l2/single_noretry_n200](results/l2/single_noretry_n200) |
| 1000 | **1000** [1000 – 1000] | 0 | 398 / 571 / 594 | 0.81 [0.74 – 0.86] | [l2/single_noretry_n1000](results/l2/single_noretry_n1000) |

Baseline: 5 of 200 and 19 of 1000. Every request is now served in arrival order; the latency is the time spent waiting in the queue.

## L2b Hot event, retrying clients, capacity N/2

| variant | N | 201 | 409 full | gave up | busy | attempts max | end-to-end p50 / p99 (ms) | wall (s) | raw |
|---|---|---|---|---|---|---|---|---|---|
| bounded (200) | 200 | 100 | 100 | 0 | 0 | 1 | 75.5 / 106 | 0.12 | [retry200_capacity_half_n200](results/l2/retry200_capacity_half_n200) |
| bounded (200) | 1000 | **500** | **500** | **0** | 0 | 1 | 522 / 816 | 0.93 | [retry200_capacity_half_n1000](results/l2/retry200_capacity_half_n1000) |
| unbounded | 200 | 100 | 100 | 0 | 0 | 1 | 74.5 / 100 | 0.13 | [retry_unbounded_capacity_half_n200](results/l2/retry_unbounded_capacity_half_n200) |
| unbounded | 1000 | **500** | **500** | 0 | 0 | 1 | 350 / 615 | 0.70 | [retry_unbounded_capacity_half_n1000](results/l2/retry_unbounded_capacity_half_n1000) |

Every value is identical across the 3 runs except the latencies (ranges in AGGREGATE.md). No client ever needed a retry, and
the event was filled exactly to capacity every time. Baseline at N=1000 bounded: 421 booked, 579 gave up, 8.65 s.

## L2c Concurrency sweep: one hot event, stepped VU ramp

`constant-vus` stages, 10 s each, closed loop, all VUs of a stage on the same event. Rates = count / 10 s. ms.
Raw: [l2/sweep_noretry](results/l2/sweep_noretry), [l2/sweep_retry](results/l2/sweep_retry); per-stage rows in [AGGREGATE.md](results/AGGREGATE.md).

**Without client retry:**

| VUs | successful bookings/s (after) | baseline | busy | 201 latency p50 / p95 / p99 (after) |
|---|---|---|---|---|
| 1 | 1041 [1016 – 1122] | 967 | 0 | 0.76 / 1.06 / 2.23 |
| 5 | 1590 [1122 – 1601] | 879 | 0 | 2.67 / 4.69 / 10.4 |
| 10 | 1661 [1263 – 1818] | 609 | 0 | 5.43 / 7.91 / 14.8 |
| 25 | 1600 [1437 – 1803] | 410 | 0 | 14.1 / 25.5 / 44.4 |
| 50 | 1711 [1668 – 1849] | 291 | 0 | 27.0 / 41.3 / 66.1 |
| 100 | 1812 [1733 – 1880] | 192 | 0 | 54.5 / 84.5 / 110 |
| 200 | **1964** [1881 – 2047] | **109** | 0 | 109 / 158 / 196 |

**With client retry** (never needed: max attempts 1 at every stage):

| VUs | successful bookings/s | baseline | end-to-end p50 / p99 | baseline end-to-end p50 / p99 |
|---|---|---|---|---|
| 1 | 1020 [1013 – 1057] | 1030 | 1 / 3 | 1 / 3 |
| 10 | 1758 [1723 – 1813] | 987 | 5 / 13 | 1 / 107 |
| 50 | 1864 [1770 – 1871] | 960 | 27 / 44 | 26 / 308 |
| 100 | 1896 [1836 – 1954] | 823 | 54 / 96 | 81 / 574 |
| 200 | **1996** [1957 – 2106] | **448** | 108 / **188** | 314 / **2072** |

How to read it. One event's bookings still pass through one short critical section, but the queue now keeps that section busy back
to back, so goodput *rises* with concurrency up to about 2000/s instead of collapsing. The cost is waiting time that grows roughly linearly
with queue depth (about 0.55 ms per queued request here). All 6 sweep runs: zero busy, zero errors, no duplicates, no oversell,
DB rows = 201s. Baseline numbers are from [baseline/REPORT.md](baseline/REPORT.md).

## L3 Duplicate bookings: each user sends 5 identical requests in parallel

| client | N | users with exactly 1 booking | >1 | 0 | 201 | 409 already booked | busy | wall (s) | raw |
|---|---|---|---|---|---|---|---|---|---|
| no retry | 200 | **200/200** ×3 | 0 | 0 | 200 | 800 | 0 | 0.51 | [l3/dup5_noretry_n200](results/l3/dup5_noretry_n200) |
| no retry | 1000 | **1000/1000** ×3 | 0 | 0 | 1000 | 4000 | 0 | 2.09 | [l3/dup5_noretry_n1000](results/l3/dup5_noretry_n1000) |
| retry | 200 | **200/200** ×3 | 0 | 0 | 200 | 800 | 0 | 0.52 | [l3/dup5_retry_n200](results/l3/dup5_retry_n200) |
| retry | 1000 | **1000/1000** ×3 | 0 | 0 | 1000 | 4000 | 0 | 2.05 | [l3/dup5_retry_n1000](results/l3/dup5_retry_n1000) |

Baseline without retry: 979–990 of 1000 users ended with **no** booking. Now "exactly one" holds whatever the client does.

## L4 Twenty concurrent draws

Event with 1000 bookings, winner_count 100, closed, `draw_at` passed; 20 simultaneous `POST /draw`.

| metric | value | raw |
|---|---|---|
| 200 / 409 `already_drawn` | 1 / 19 in each of 3 runs | [l4/draw20_n1000/run*.txt](results/l4/draw20_n1000) |
| latency p50 / max (ms) | 140 [131 – 169] / 141 [133 – 170] | same (baseline 125 / 127) |
| SQL (status drawn; one `drawn_at`; rows = 1000 entrants; ranks 1..1000; drawn = booked users; **exactly one seal**; 100 winners; 900 waitlist) | **8/8 PASS** ×3 | [sql_run1](results/l4/draw20_n1000/sql_run1.txt), [2](results/l4/draw20_n1000/sql_run2.txt), [3](results/l4/draw20_n1000/sql_run3.txt) |
| DB vs `GET /results` vs 200 body | **IDENTICAL** ×3 | [compare_run1](results/l4/draw20_n1000/compare_run1.txt), [2](results/l4/draw20_n1000/compare_run2.txt), [3](results/l4/draw20_n1000/compare_run3.txt) |
| `GET /results` integrity; POST digest = stored seal | `verified`; `true` ×3 | same |

## L5 Fairness sanity check (10 participants, 5 winners, df = 9)

| source | draws | win freq. χ² adjusted (p) | textbook (p) | rank-1 χ² (p) | raw |
|---|---|---|---|---|---|
| algorithm run 1 | 5000 | 10.87 (0.285) | 6.04 (0.736) | 9.00 (0.437) | [algorithm_5000_run1.txt](results/l5/algorithm_5000_run1.txt) |
| algorithm run 2 | 5000 | 8.19 (0.515) | 4.55 (0.872) | 6.32 (0.708) | [algorithm_5000_run2.txt](results/l5/algorithm_5000_run2.txt) |
| algorithm run 3 | 5000 | 6.79 (0.659) | 3.77 (0.926) | 8.01 (0.533) | [algorithm_5000_run3.txt](results/l5/algorithm_5000_run3.txt) |
| algorithm, large | 200,000 | 4.82 (0.849) | 2.68 (0.976) | 4.23 (0.896) | [algorithm_200000.txt](results/l5/algorithm_200000.txt) |
| **API run 1** | 1000 | **22.99 (0.0062)** | 12.77 (0.173) | **17.34 (0.044)** | [api_1000_run1.txt](results/l5/api_1000_run1.txt) |
| API run 2 | 1000 | 5.64 (0.775) | 3.14 (0.959) | 12.06 (0.210) | [api_1000_run2.txt](results/l5/api_1000_run2.txt) |
| API run 3 | 1000 | 6.62 (0.677) | 3.68 (0.931) | 13.42 (0.145) | [api_1000_run3.txt](results/l5/api_1000_run3.txt) |
| **API follow-up** (run because of API run 1) | 5000 | **3.23 (0.955)** | 1.79 (0.994) | **8.16 (0.518)** | [api_5000_followup.txt](results/l5/api_5000_followup.txt) |
| calibration: 2000 fair experiments × 5000 | | p<0.05 in 5.20% | p<0.05 in 0.15% | p<0.05 in 5.80% | [algorithm_calibration_2000x5000.txt](results/l5/algorithm_calibration_2000x5000.txt) |

Stated plainly. API run 1 gave p = 0.006 (adjusted win frequency) and p = 0.044 (rank-1). The participants that ran high in that run
(indices 1, 2, 6, 7) differ from the baseline's low run (3, 6) and from every other run. A bias tied to booking order or
user identity would repeat. A 5× larger API follow-up showed no deviation (p = 0.955 and 0.518; every participant within 1.8%
of 2500). With 24 tests in this table, one p < 0.01 is not unusual (about 1 in 5 chance). **The check found no evidence of bias; it does not
prove fairness.** The algorithm code is unchanged from the baseline. The API helper retried 0 transport errors.

## L6 Audit-log tamper test

| attempt | runtime role `lottery_app` (what the app uses) | owner/superuser `lottery` | non-superuser owner | raw |
|---|---|---|---|---|
| UPDATE / DELETE / TRUNCATE `lottery_draws` | **denied**: `42501 permission denied` | **denied**: trigger, "audit trail is append-only" | **denied** by trigger, *until it disables the trigger* (then `UPDATE 1`, rolled back) | [l6/tamper.txt](results/l6/tamper.txt) |
| DELETE / UPDATE `lottery_draw_seals` | denied (`42501`) | denied (trigger) | – | same |
| `ALTER TABLE … DISABLE TRIGGER` / `DROP TRIGGER` | **denied**: "must be owner" | (not tried) | allowed (residual risk) | same |
| `SET session_replication_role = replica` | **denied** | allowed (residual risk) | – | same |
| change a drawn event's status / winner_count | denied (trigger) | denied (trigger) | – | same |
| **committed** superuser winner swap with triggers skipped | – | succeeds, then **`GET /results` → `integrity: mismatch`** and the app logs an error; after restoring → `verified` | – | [l6/tamper_detection.txt](results/l6/tamper_detection.txt) |

Catalog: the app's connections belong to `lottery_app` (not a superuser, owns nothing, SELECT/INSERT only on audit tables); 6 triggers active
([phase0/db_roles_and_privileges.txt](results/phase0/db_roles_and_privileges.txt)). Rolled-back attempts left the table digest unchanged.

## L7 Read load (constant arrival rate, 60 s)

Same dataset as baseline: 50 events, one drawn with 1000 entrants (`/results` is now 168 KB) ([l7/dataset.txt](results/l7/dataset.txt)).

| target | achieved | dropped | errors | p50 / p95 / p99 (ms) | `/events` p99 | `/events/{id}` p99 | `/results` p99 | raw |
|---|---|---|---|---|---|---|---|---|
| 300/s | 18,003 | 0 | **0** | 1.53 / 5.17 / 9.59 | 7.30 | 5.49 | 11.6 | [l7/rate300](results/l7/rate300) |
| 1500/s | 89,964 [89,866 – 89,975] | 38 [27 – 137] | **0** | 2.42 / 21.6 / 73.7 | 41.1 | 34.0 | 106 | [l7/rate1500](results/l7/rate1500) |
| 3000/s | 88,612 [78,844 – 105,397] | 91,389 | **2437 [1849 – 2606] timeouts**, no 4xx/5xx | 258 / 5056 / 30000 | 30000 | 30000 | 30000 | [l7/rate3000](results/l7/rate3000) |

Baseline: 300/s p99 10.6; 1500/s p99 64.1 (`/results` 91.2); 3000/s 116,517 achieved, 63,486 dropped, 2058 timeouts. The setup still
saturates at 3000/s, slightly worse than before. The bottleneck was not isolated (app, Postgres, k6 or the Docker forwarder).

## L8 Rate limiter (code defaults: 100/min API, 10/min auth, 10/15 min per account)

| check | run 1 | run 2 | run 3 | raw |
|---|---|---|---|---|
| 120 × `GET /events` | 101 × 200, then 19 × 429 | 100 × 200, then 20 × 429 | 100 × 200, then 20 × 429 | [run1](results/l8/run1.txt), [run2](results/l8/run2.txt), [run3](results/l8/run3.txt) |
| after 429: spoofed `X-Forwarded-For` ×3, `X-Real-IP` ×3 | **all 429** | all 429 | all 429 | same |
| `/health`, `/ready` while limited | **200, 200** | 200, 200 | 200, 200 | same |
| 15 × `POST /auth/login` | 10 × 401, 5 × 429 | same | same | same |
| after 429: spoofed headers on login ×6 | **all 429** | all 429 | all 429 | same |
| *behind trusted proxy*: 11 logins, same forwarded IP | 10 × 401, 1 × 429; a different forwarded IP → 401 | same | same | [proxy_run1](results/l8/proxy_run1.txt), [2](results/l8/proxy_run2.txt), [3](results/l8/proxy_run3.txt) |
| *behind trusted proxy*: 12 wrong-password logins for **one account**, each from a **different** IP | **10 × 401, then 429** | same | same | same |
| another account, fresh IP | 401 (unaffected) | same | same | same |

Run 1 let a 101st request through. This is consistent with httprate's approximate sliding window at a minute boundary (seen
once before in a trial, never in the baseline's 3 runs); it is not a bypass. Not separately proven.

## L9 Graceful shutdown (SIGTERM during a booking burst, 50 VUs over 50 events)

| metric | run 1 | run 2 | run 3 | raw |
|---|---|---|---|---|
| requests in flight at SIGTERM → completed with a response | **50 → 50** (all 201) | **50 → 50** | **50 → 50** | [timeline_run1](results/l9/timeline_run1.txt), [2](results/l9/timeline_run2.txt), [3](results/l9/timeline_run3.txt) |
| in-flight requests that failed | 0 | 0 | 0 | same |
| "shutdown signal received" → "server stopped cleanly" | 33 ms | 18 ms | 42 ms | same |
| started after the signal: refused / EOF | 6800 / 150 | 6806 / 150 | 6551 / 150 | same |
| bookings in DB = 201s received; no duplicates; no over-capacity | 27,009 = 27,009 | 28,806 = 28,806 | 27,638 = 27,638 | [sql_run1](results/l9/sql_run1.txt), [2](results/l9/sql_run2.txt), [3](results/l9/sql_run3.txt) |

---

## Claims supported by evidence

True **for this code, on this local single-machine setup, under the stated conditions**.

1. "Never oversold and never double-booked under concurrent load: 39 runs, 846,603 booking requests, up to 1000 simultaneous
   clients on one event; database rows always equalled the 201s clients received." [totals.txt](results/totals.txt), `sql_run*.txt`.
2. "1000 users booking the same event at the same instant all got a booking, in arrival order, in under a second, with no 'busy'
   rejections (3/3 runs); before the redesign only 9–20 did." [l2/single_noretry_n1000](results/l2/single_noretry_n1000) vs baseline.
3. "Redesigned hot-event booking from try-once locking to a FIFO queue + distributed lock + row lock: per-event goodput went from
   109 to about 1,960 bookings/s at 200 concurrent clients, and p99 end-to-end for retrying clients from 2.1 s to 188 ms (local benchmark)."
   [l2/sweep_noretry](results/l2/sweep_noretry), [l2/sweep_retry](results/l2/sweep_retry), baseline.
4. "1000 users each firing 5 identical booking requests in parallel ended with exactly one booking each, with or without client
   retries (12/12 runs)." [l3](results/l3).
5. "20 simultaneous draw requests produced exactly one draw (3/3); the stored draw, the results API, the draw response and its SHA-256 seal all agreed."
   [l4/draw20_n1000](results/l4/draw20_n1000).
6. "The application's database role cannot modify, delete, truncate or un-trigger the audit trail; even the table owner is blocked by
   triggers; and a superuser who bypasses the triggers is detected by the draw seal." [l6](results/l6).
7. "Found and fixed a rate-limiter bypass via spoofed X-Forwarded-For; added a per-account login limit that holds across IPs."
   [l8](results/l8), [baseline/results/l8](baseline/results/l8).
8. "On SIGTERM, all in-flight requests (50/50 in each of 3 runs) completed, and no booking was lost or orphaned."
   [l9](results/l9).
9. "Reads sustained 1500 req/s for 60 s with zero errors, p99 74 ms (local, single machine)." [l7/rate1500](results/l7/rate1500).
10. "Load testing found real issues (a mutable audit log, a spoofable rate limiter, booking starvation under contention, ambiguous
    error codes, draws allowed before their scheduled time); all were fixed and re-verified with the same suite." [baseline/FINDINGS.md](baseline/FINDINGS.md), [FINDINGS.md](FINDINGS.md).

## Claims NOT supported

* **"Impenetrable", "unhackable", "tamper-proof", "immutable".** Whoever holds the owner/superuser DB credentials can still disable
  triggers or set `session_replication_role = replica` (L6 sections C and the detection test). That is detected, not prevented, and
  detection only helps if the seal digest is kept somewhere the attacker cannot rewrite (it is logged; shipping logs off-box is the operator's job).
* **Any req/s or bookings/s figure as production capacity.** One laptop, load generator on the same machine, Docker Desktop networking.
* **"Throughput scales with replicas" / multi-replica behaviour.** Only one app instance was tested. The FIFO queue is per process; across
  replicas, ordering between replicas is not FIFO.
* **"Low latency under contention."** Under contention, requests wait in line: about 0.6 s for 1000 simultaneous users on one event,
  growing linearly. Beyond `LOCK_WAIT_TIMEOUT` (10 s) they get 503. That limit was not reached in these tests.
* **"Reads got faster" or "sustains 3000 req/s".** Reads were slightly slower than the baseline at 1500/s, and 3000/s saturates.
* **"Provably fair."** χ² checks found no evidence of bias (including a 5000-draw API follow-up after one low p-value); that is not a proof.
* **"Rate limiting is global"/"distributed".** Counters are in memory per replica; N replicas allow N × the limit.
* **Behaviour when Redis or Postgres fail, lock expiry, soak/long-run stability, memory/CPU usage**: not tested.
* **"Different events are booked fully concurrently."** Not load-tested (every contention scenario used one hot event at a time).
