# Baseline load-test report (before fixes, commit `8cd482e`)

> **Historical.** This is the run that found the issues in [FINDINGS.md](FINDINGS.md). They were fixed in
> commit `6ee3b75`; the current report is [../REPORT.md](../REPORT.md). Harness files linked from here
> are at their current versions; the exact versions used for this run are in commit `a725147`.

# Load-test report: Lottery Management System backend

**All numbers are local, single machine**: Apple M2 (8 cores, 8 GB). k6, the app, Postgres 16 and Redis 7
shared the laptop, with traffic going through Docker Desktop's port forwarding. See [results/ENVIRONMENT.md](results/ENVIRONMENT.md).
They describe this setup only. They are not production capacity.

* Commit `8cd482e`, no application code changed. Rate limits were raised for every scenario except L8; `LOG_LEVEL=warn`.
  See [README](../README.md#configuration-changes-and-only-these).
* Each measured scenario ran **3 times** after a warm-up. Cells read `median [min – max]` across the 3 runs.
  They come from [results/AGGREGATE.md](results/AGGREGATE.md), which `cmd/aggregate` computes from the raw k6
  JSON files cited in each table.
* Reproduce everything with `loadtest/run.sh`.
* In total, the final runs sent 7,936,273 HTTP requests across 51 k6 runs. Transport errors, timeouts, 4xx or 5xx
  occurred only in L7 at 3000 req/s (timeouts) and L9 after SIGTERM (connection refused/EOF) ([results/totals.txt](results/totals.txt)).
* Several first attempts were thrown out for harness bugs. They are kept and explained in
  [results/superseded/](results/superseded/README.md) and FINDINGS F12.

## Invariants at a glance

| invariant | runs | result | evidence |
|---|---|---|---|
| no duplicate (event, user) booking | 39 booking runs (L2, L2u, L3, L9) | **PASS 39/39** | `sql_run*.txt` in each scenario dir |
| no event over capacity | 39 | **PASS 39/39** | same |
| rows in `bookings` = 201 responses k6 received | 39 | **PASS 39/39** | same |
| capacity filled exactly, no oversell, when it was reached (N/2 seats) | 9 (L2b n200 ×3, L2u ×6) | **PASS 9/9** (100/100, 500/500) | [l2/retry_unbounded_*](results/l2) |
| exactly one booking per user with 5 parallel duplicates (retrying client) | 6 | **PASS 6/6** | [l3/dup5_retry_*](results/l3) |
| at most one booking per user (no client retry) | 6 | **PASS 6/6** | [l3/dup5_noretry_*](results/l3) |
| 20 concurrent draws → exactly one draw, status `drawn`, counts right, DB = `/results` = 200 body | 3 | **PASS 3/3** (7 SQL checks + 2 diffs each) | [l4/draw20_n1000](results/l4/draw20_n1000) |
| audit table `lottery_draws` cannot be modified | 1 | **FAIL**: app role can UPDATE, DELETE, TRUNCATE | [l6/tamper.txt](results/l6/tamper.txt), FINDINGS F1 |
| rate limiter enforces defaults | 3 | **PASS 3/3** | [l8](results/l8) |
| rate limiter cannot be bypassed by the client | 3 | **FAIL**: spoofed `X-Forwarded-For`/`X-Real-IP` resets it | [l8](results/l8), FINDINGS F2 |
| SIGTERM: in-flight requests complete, no orphaned bookings | 3 | **PASS 3/3** | [l9](results/l9) |

---

## L1 Setup (not a measured scenario)

| item | value | source |
|---|---|---|
| users registered + logged in once (bcrypt cost 12), 8 workers | 1000 users in 81.1 s, 1 run | [results/l1/setup_users.txt](results/l1/setup_users.txt) |

N=200 scenarios use the first 200 of these 1000 users. JWTs are reused, so no login happens in a measured phase.

## L2a Hot event, every user sends one booking at the same moment (no retry)

`shared-iterations`, VUs = N, one `POST /events/{id}/book` per user, capacity N.

| N | 201 | 409 busy | other | latency p50 / p95 / p99 (ms, all requests) | wall time (s) | raw |
|---|---|---|---|---|---|---|
| 200 | 5 [4 – 6] | 195 [194 – 196] | 0 | 28.7 / 36.4 / 37.0 | 0.05 [0.04 – 0.06] | [l2/single_noretry_n200](results/l2/single_noretry_n200) |
| 1000 | 19 [9 – 20] | 981 [980 – 991] | 0 | 37.4 / 74.9 / 83.0 | 0.15 [0.11 – 0.16] | [l2/single_noretry_n1000](results/l2/single_noretry_n1000) |

Each run lasts a fraction of a second, so the k6 "req/s" figures for these runs are not meaningful and are not reported.
The point is the distribution. The per-event Redis lock is try-once, so concurrent requests are rejected, not queued (FINDINGS F3).
SQL: no duplicates, no oversell, DB rows = 201s in all 6 runs (`sql_run*.txt`).

## L2b Hot event, clients retry the busy 409, capacity N/2

Client policy: on 409 busy, sleep 5–25 ms (uniform jitter) and retry. "Bounded" gives up after 200 attempts;
"unbounded" retries until it gets any other answer. `book_end_to_end` is per user and includes retries and sleeps.

| variant | N | seats | 201 | 409 full | gave up | 409 busy (total) | attempts per user p50 / max | end-to-end p50 / p99 (ms) | 201 latency p50 / p99 (ms) | wall time (s) | raw |
|---|---|---|---|---|---|---|---|---|---|---|---|
| bounded 200 | 200 | 100 | 100 [100 – 100] | 100 | 0 | 1773 [1682 – 1873] | 11 / 18 | 195 / 292 | 1.99 / 14.7 | 0.31 | [l2/retry200_capacity_half_n200](results/l2/retry200_capacity_half_n200) |
| bounded 200 | 1000 | 500 | **421 [409 – 444]** | **0** | **579 [556 – 591]** | 165,233 [164,012 – 168,143] | 200 / 200 | 8414 / 8609 | 38.7 / 125 | 8.65 | [l2/retry200_capacity_half_n1000](results/l2/retry200_capacity_half_n1000) |
| unbounded | 200 | 100 | 100 [100 – 100] | 100 | 0 | 1744 [1220 – 1843] | 11 / 18 | 194 / 298 | 1.85 / 34.8 | 0.31 | [l2/retry_unbounded_capacity_half_n200](results/l2/retry_unbounded_capacity_half_n200) |
| unbounded | 1000 | 500 | **500 [500 – 500]** | **500** | 0 | 211,697 [209,226 – 215,196] | 256 / 321 | 11,258 / 12,745 | 39.0 / 165 | 12.8 | [l2/retry_unbounded_capacity_half_n1000](results/l2/retry_unbounded_capacity_half_n1000) |

* **No oversell.** Whenever capacity was reached, bookings equalled capacity exactly (`bookings=500 capacity=500` and
  `100/100` lines in `sql_run*.txt`), and every later user got "full".
* **Bounded retry at N=1000 never filled the event.** More than half the users gave up while about 60–90 seats were still free
  (FINDINGS F4). The oversell path was only reached by the unbounded variant at that N.

## L2c Concurrency sweep: one hot event, stepped VU ramp

`constant-vus` stages of 1 → 200 VUs, 10 s each, closed loop, all VUs of a stage on the same event. Per-second
figures = count / 10 s. Latency in ms. Source: [l2/sweep_noretry](results/l2/sweep_noretry) and [l2/sweep_retry](results/l2/sweep_retry)
(per-stage rows in [results/AGGREGATE.md](results/AGGREGATE.md)).

**Without client retry** (each iteration = one request):

| VUs | requests/s | successful bookings/s | busy 409s in stage | 201 latency p50 / p99 | all-request latency p50 / p99 |
|---|---|---|---|---|---|
| 1 | 967 [919 – 1186] | 967 [919 – 1186] | 0 | 0.77 / 2.18 | 0.77 / 2.18 |
| 5 | 6484 [6297 – 6768] | 879 [798 – 904] | 55,799 | 1.34 / 5.30 | 0.52 / 2.89 |
| 10 | 9104 [9026 – 9916] | 609 [597 – 658] | 84,947 | 2.04 / 9.24 | 0.81 / 4.33 |
| 25 | 15152 [14384 – 15612] | 410 [382 – 420] | 147,418 | 3.34 / 14.0 | 1.31 / 6.91 |
| 50 | 18690 [9502 – 18840] | 291 [146 – 294] | 183,991 | 5.18 / 20.6 | 2.12 / 11.3 |
| 100 | 20692 [19056 – 21443] | 192 [178 – 194] | 204,997 | 9.33 / 28.5 | 4.01 / 17.3 |
| 200 | 19268 [18632 – 22058] | 109 [102 – 118] | 191,586 | 18.6 / 53.2 | 8.64 / 33.3 |

**With client retry** (each iteration = one logical booking; retry busy after 5–25 ms, at most 200 attempts; no iteration reached 200, max was 177):

| VUs | requests/s | successful bookings/s | end-to-end p50 / p99 | 201 latency p50 / p99 | max attempts |
|---|---|---|---|---|---|
| 1 | 1030 [923 – 1054] | 1030 [923 – 1054] | 1 / 3 | 0.73 / 2.33 | 1 |
| 5 | 1332 [1295 – 1334] | 1081 [1050 – 1086] | 1 / 69.9 | 0.73 / 2.45 | 11 |
| 10 | 1536 [1521 – 1608] | 987 [968 – 1054] | 1 / 107 | 0.76 / 4.90 | 18 |
| 25 | 2588 [2571 – 2626] | 1133 [1094 – 1141] | 1 / 168 | 0.78 / 4.04 | 26 |
| 50 | 3948 [3784 – 4003] | 960 [886 – 1026] | 26 / 308 | 0.99 / 7.03 | 49 |
| 100 | 6765 [6621 – 7097] | 823 [770 – 962] | 81 / 574 | 1.51 / 8.50 | 70 |
| 200 | 11028 [10824 – 12336] | 448 [423 – 590] | 314 / 2072 | 2.91 / 28.2 | 172 [126 – 177] |

How to read it: one event's critical section (Redis `SETNX` → transaction with `FOR UPDATE` → `COUNT` → `INSERT` → commit → Lua release)
is serialised, so successful bookings per event stay around 1000/s no matter how many clients there are, and
**they fall** as more clients compete. Without retry, the extra clients just turn into fast 409s. With retry, they turn into tail
latency: end-to-end p99 goes from 3 ms at 1 VU to about 2 s at 200 VUs. This is expected behaviour of the
design, not a failure. All 6 sweep runs: no duplicates, no oversell, DB rows = 201s (`sql_run*.txt`).
"Stage" rates use the fixed 10 s stage length, so in-flight iterations finishing during the graceful stop are included (small).
In the no-retry sweep, a few `already_booked` 409s (0–2 per run) come from the (user, event) mapping wrapping at 200 events;
they are counted, not hidden.

## L3 Duplicate bookings: each user sends 5 identical requests in parallel

`http.batch` of 5, VUs = min(N, 200), capacity 5N. Retrying client: same jitter as L2b, no cap reached (no give-ups).

| client | N | users with exactly 1 booking | users with >1 | users with 0 | 201 | 409 already booked | 409 busy | wall time (s) | raw |
|---|---|---|---|---|---|---|---|---|---|
| retry | 200 | **200 / 200** in all 3 runs | **0** | 0 | 200 | 800 | 178,137 [169,887 – 201,295] | 9.87 [9.67 – 12.7] | [l3/dup5_retry_n200](results/l3/dup5_retry_n200) |
| retry | 1000 | **1000 / 1000** in all 3 runs | **0** | 0 | 1000 | 4000 | 648,361 [589,023 – 649,930] | 34.8 [31.5 – 37.1] | [l3/dup5_retry_n1000](results/l3/dup5_retry_n1000) |
| no retry | 200 | 4, 13, 8 | **0** | 196, 187, 192 | 8 [4 – 13] | 0 | 992 [987 – 996] | 0.53 | [l3/dup5_noretry_n200](results/l3/dup5_noretry_n200) |
| no retry | 1000 | 10, 21, 13 | **0** | 990, 979, 987 | 13 [10 – 21] | 0 [0 – 3] | 4987 [4976 – 4990] | 0.36 | [l3/dup5_noretry_n1000](results/l3/dup5_noretry_n1000) |

The 4 "already booked" per user in the retry runs are the duplicates being rejected by the `UNIQUE (event_id, user_id)`
constraint. That constraint, not an application pre-check, is what stops duplicates (FINDINGS P0-1).

## L4 Twenty concurrent draws

Event with 1000 bookings, capacity 1000, winner_count 100, closed; then 20 `POST /events/{id}/draw` at once (20 VUs × 1).

| metric | value | raw |
|---|---|---|
| 200 responses | 1 [1 – 1] | [l4/draw20_n1000/run*.txt](results/l4/draw20_n1000) |
| 409 "already been drawn" | 19 [19 – 19] | same |
| latency of the 20 requests p50 / max (ms) | 125 [116 – 139] / 127 [117 – 140] | same |
| SQL: status `drawn`; 1 distinct `drawn_at`; rows = entrants (1000); ranks exactly 1..1000; drawn users = booked users; 100 winners; 900 waitlist | **7/7 PASS** in each of 3 runs | [sql_run1](results/l4/draw20_n1000/sql_run1.txt), [2](results/l4/draw20_n1000/sql_run2.txt), [3](results/l4/draw20_n1000/sql_run3.txt) |
| DB rows vs `GET /results` vs the one 200 body (rank, user, W/L) | **IDENTICAL** (1000 lines each) in 3/3 runs | [compare_run1](results/l4/draw20_n1000/compare_run1.txt), [2](results/l4/draw20_n1000/compare_run2.txt), [3](results/l4/draw20_n1000/compare_run3.txt) |

The 19 losers waited on `SELECT ... FOR UPDATE` until the winner committed (1000 single-row inserts), which is why all 20
take about 125 ms. Note that the table holds one row per entrant, not one per draw (FINDINGS F6).

## L5 Fairness sanity check (10 participants, 5 winners)

Two statistics are reported for "how often did each participant win", plus a rank-1 test. The **textbook Pearson** χ² is
miscalibrated here and too lenient (see the calibration row). The **variance-adjusted** χ² uses the exact null
variance of drawing 5 distinct from 10, and it rejected at 5.00% in calibration, so it is the one to quote. df = 9 throughout.

| source | draws | win-frequency χ² adjusted (p) | win-frequency χ² textbook (p) | rank-1 χ² (p) | raw |
|---|---|---|---|---|---|
| algorithm, run 1 | 5000 | 5.69 (0.771) | 3.16 (0.958) | 8.00 (0.535) | [algorithm_5000_run1.txt](results/l5/algorithm_5000_run1.txt) |
| algorithm, run 2 | 5000 | 5.67 (0.772) | 3.15 (0.958) | 14.19 (0.116) | [algorithm_5000_run2.txt](results/l5/algorithm_5000_run2.txt) |
| algorithm, run 3 | 5000 | 2.58 (0.979) | 1.44 (0.998) | 7.71 (0.564) | [algorithm_5000_run3.txt](results/l5/algorithm_5000_run3.txt) |
| algorithm, one large run | 200,000 | 13.91 (0.126) | 7.73 (0.562) | 12.00 (0.213) | [algorithm_200000.txt](results/l5/algorithm_200000.txt) |
| **API**, run 1 (create, book 10, close, draw) | 1000 | **18.50 (0.030)** | 10.28 (0.329) | 14.70 (0.0995) | [api_1000_run1.txt](results/l5/api_1000_run1.txt) |
| **API**, run 2 | 1000 | 10.64 (0.301) | 5.91 (0.749) | 6.84 (0.654) | [api_1000_run2.txt](results/l5/api_1000_run2.txt) |
| **API**, run 3 | 1000 | 7.00 (0.637) | 3.89 (0.919) | 6.08 (0.732) | [api_1000_run3.txt](results/l5/api_1000_run3.txt) |
| calibration: 2000 independent fair experiments × 5000 draws | | p<0.05 in **5.00%**, p<0.01 in 0.80% | p<0.05 in **0.05%** | p<0.05 in 4.95% | [algorithm_calibration_2000x5000.txt](results/l5/algorithm_calibration_2000x5000.txt) |

Interpretation, stated plainly. 20 of the 21 test results have p > 0.05. API run 1's adjusted win-frequency test gave
p = 0.030. With 21 tests at α = 0.05, about one such result is expected by chance even for a perfect RNG; the
calibration row shows the test fires about 5% of the time on fair data. The other two API runs and every larger
algorithm-level sample show no deviation. **This is a sanity check that found no evidence of bias; it is not proof
of fairness.** The 200,000-draw algorithm run (adjusted p = 0.126, rank-1 p = 0.213) is also unremarkable. In the API test,
participants are always booked in the same order, so a bias tied to booking order would have shown up as one index
winning more. The API helper retried 0 transport errors in the final runs (`transport_errors_retried=0`).

## L6 Audit-log tamper test

| attempt | app role `lottery` (superuser, owner) | non-superuser owner | non-owner, SELECT+INSERT only | mechanism | raw |
|---|---|---|---|---|---|
| UPDATE | **allowed** | **allowed** | denied, `42501 permission denied` | default privileges, **not** the REVOKE (control table without REVOKE behaves identically) | [l6/tamper.txt](results/l6/tamper.txt) |
| DELETE | **allowed** | **allowed** | denied, `42501` | same | same |
| TRUNCATE | **allowed** | – | – | – | same |
| swap the rank-1 winner | **allowed** (`UPDATE 2`) | – | – | – | same |
| trigger present? | **no** (0 triggers, 0 rules, RLS off) | | | | [phase0/db_roles_and_privileges.txt](results/phase0/db_roles_and_privileges.txt) |

All attempts were rolled back; the table digest before and after is identical (`83845ee0…`). **The audit table is not
protected against the role the app runs as.** FINDINGS F1. Not fixed; it is the owner's decision.

## L7 Read load (constant arrival rate, 60 s)

Total rate split evenly over `GET /events` (4.7 KB), `GET /events/{id}` (0.3 KB) and `GET /events/{id}/results`
(167 KB: a drawn event with 1000 entrants). 50 events, 1000 bookings ([l7/dataset.txt](results/l7/dataset.txt)). k6 request timeout 30 s.

| target rate | achieved requests (60 s) | dropped by k6 | errors | all p50 / p95 / p99 (ms) | `/events` p99 | `/events/{id}` p99 | `/results` p99 | raw |
|---|---|---|---|---|---|---|---|---|
| 300/s | 18,002 [18,001 – 18,002] | 0 | **0** (all 2xx) | 1.98 / 4.78 / 10.6 | 9.26 | 6.65 | 12.9 | [l7/rate300](results/l7/rate300) |
| 1500/s | 89,963 [89,926 – 89,980] | 40 [23 – 76] | **0** (all 2xx) | 1.74 / 12.6 / 64.1 | 35.9 | 28.3 | 91.2 | [l7/rate1500](results/l7/rate1500) |
| 3000/s | 116,517 [106,110 – 135,371] | 63,486 [44,632 – 73,893] | **2058 [1749 – 2233] timeouts** (no 4xx/5xx); failed rate 0.02 | 190 / 1760 / 30000 | 30000 | 30000 | 30000 | [l7/rate3000](results/l7/rate3000) |

At 1500 req/s about 0.04% of scheduled iterations were dropped (k6 had no free VU in time); every request that was sent succeeded.
**At 3000 req/s the single machine saturated.** About a third of scheduled requests were never sent, p95 was 1.8 s, and about 2% timed out
at the 30 s client limit. These runs do not identify the bottleneck: it could be the app, Postgres, k6 itself, or Docker Desktop's port forwarding.
The `/results` endpoint moves 167 KB per call (about 167 MB/s at 1000 calls/s). The supportable claim is therefore
"1500 req/s with zero errors and p99 64 ms on this laptop", not a server capacity.

## L8 Rate limiter at default settings (100/min general, 10/min auth, per IP)

| check | run 1 | run 2 | run 3 | raw |
|---|---|---|---|---|
| general: 120 × `GET /events` after 1 counted `/health` probe | 99 × 200, then 21 × 429 | same | same | [l8/run1.txt](results/l8/run1.txt), [run2](results/l8/run2.txt), [run3](results/l8/run3.txt) |
| 429 headers | `Retry-After: 60`, `X-Ratelimit-Limit: 100`, `Remaining: 0` | same | same | same |
| auth: 15 × `POST /auth/login` (`{}` body) | 10 × 401, then 5 × 429 | same | same | same |
| after 429, add `X-Forwarded-For: 203.0.113.x` (API) | **3 × 200** (bypassed) | same | same | same |
| after 429, add `X-Real-IP: 198.51.100.x` (login) | **3 × 401** (bypassed) | same | same | same |

Limits in effect: [l8/limits_in_effect.txt](results/l8/limits_in_effect.txt). The limiter works exactly at the configured counts,
but any client can reset it by sending a header (FINDINGS F2).

## L9 Graceful shutdown (SIGTERM during a booking burst)

50 VUs, closed loop, booking across 50 events; `docker kill -s TERM` about 6 s in. "In flight" = started before `docker kill`
began and finished after it (host clock, per-request timestamps).

| metric | run 1 | run 2 | run 3 | raw |
|---|---|---|---|---|
| requests in flight at SIGTERM | 50 | 50 | 44 | [timeline_run1](results/l9/timeline_run1.txt), [2](results/l9/timeline_run2.txt), [3](results/l9/timeline_run3.txt) |
| … that completed with an HTTP response | **50** (46 × 201, 4 × 409) | **50** (50 × 201) | **44** (38 × 201, 6 × 409) | same |
| … that failed | **0** | **0** | **0** | same |
| started after `docker kill` began, still served before the listener closed | 269 | 204 | 230 | same |
| started after, failed: connection refused / EOF (no response) | 6650 / 150 | 6700 / 150 | 6658 / 150 | same |
| app log: "shutdown signal received" → "server stopped cleanly" | 37 ms | 22 ms | 25 ms | same (log timestamps) |
| bookings in DB = 201s received; no duplicates; no over-capacity | 22,620 = 22,620 PASS | 23,226 = 23,226 PASS | 26,757 = 26,757 PASS | [sql_run1](results/l9/sql_run1.txt), [2](results/l9/sql_run2.txt), [3](results/l9/sql_run3.txt) |

No request that failed (no response) created a booking, and no booking was created without its 201 reaching the client.
Docker did not restart the container afterwards (FINDINGS F12). After an error each VU paused 100 ms, so the refused count reflects
that pacing, not a property of the app.

---

## Claims supported by evidence

Each is true **for this code, on this local single-machine setup, under the conditions stated**.

1. "Under concurrent booking of a single event (up to 1000 simultaneous clients, plus a 1→200 VU sweep), the system never
   oversold and never created a duplicate booking, across 39 runs and 7,254,341 booking requests (most of them rejected
   as busy by design); database rows always equalled the 201 responses clients received."
   [l2](results/l2), [l3](results/l3), [l9](results/l9), `sql_run*.txt`, [totals.txt](results/totals.txt).
2. "With 1000 clients competing for 500 seats and retrying until answered, exactly 500 were booked and 500 refused as full, in 3/3 runs."
   [l2/retry_unbounded_capacity_half_n1000](results/l2/retry_unbounded_capacity_half_n1000).
3. "When each of 1000 users fired 5 identical bookings in parallel, every user ended up with exactly one booking (3/3 runs, with
   clients retrying the busy response); without retries no user ever got more than one." [l3](results/l3).
4. "20 simultaneous draw requests produced exactly one draw (1 × 200, 19 × 409) in 3/3 runs; the persisted draw, the results API
   and the draw response were identical (1000 entrants, 100 winners, 900 waitlist)." [l4/draw20_n1000](results/l4/draw20_n1000).
5. "On a single event, the booking critical section sustained about 1,000–1,100 successful bookings/s locally, with
   0.7 ms median and about 2.3 ms p99 at concurrency 1." [l2/sweep_retry](results/l2/sweep_retry) c1–c25 rows.
6. "Read endpoints sustained 1500 req/s for 60 s with zero errors, p50 1.7 ms and p99 64 ms (local, single machine, 3 runs)."
   [l7/rate1500](results/l7/rate1500).
7. "Chi-square sanity checks on 10 participants / 5 winners (algorithm: 3 × 5,000 and 1 × 200,000 draws; through the API:
   3 × 1,000 draws) found no evidence of bias; 20 of 21 tests had p > 0.05, and the one exception (p = 0.03) is in line with chance."
   [l5](results/l5). Say "no evidence of bias", not "proven fair".
8. "On SIGTERM, all requests in flight (44–50 per run) completed, shutdown took 22–37 ms, and no booking was lost or orphaned (3/3 runs)."
   [l9](results/l9).
9. "The auth and API rate limits trigger at exactly the configured counts (10 and 100 per minute per IP)." [l8](results/l8).
   Mention F2 if asked.
10. "Load-tested with k6 (scripts, SQL invariant checks and raw results in the repo; one command reproduces it), and the testing
    found two real issues: a mutable audit table and a header-spoofable rate limiter." [FINDINGS.md](FINDINGS.md).

## Claims NOT supported

Do not put these on a resume or say them in an interview as fact.

* **"Immutable" / "append-only" / "tamper-proof" audit log, "enforced at the database level", "even compromised code can't change history."**
  False. The app's DB role can UPDATE, DELETE and TRUNCATE `lottery_draws` and swap winners (L6, F1). The REVOKE does nothing.
* **"Brute-force protection" on login / "rate limited per IP" without qualification.** A client bypasses it with one header (L8, F2).
* **Any requests-per-second figure as server or production capacity.** All numbers are from one laptop with the load generator on the
  same machine and Docker Desktop networking. No multi-replica, no separate DB host, no network latency.
* **"Handles 1000 concurrent bookings" in the sense of 1000 users all getting a booking.** With 1000 simultaneous single-shot requests,
  only 9–20 succeeded (L2a). With retrying clients capped at 200 attempts, 556–591 of 1000 users gave up while seats were free (L2b, F4).
  Correctness held; availability under a hot event did not.
* **"Throughput scales with concurrency" for a single event.** It falls: from about 1000 bookings/s at 1 VU to 109/s (no retry) or 448/s
  (with retry) at 200 VUs (L2c).
* **"p99 under X ms under high contention"** without stating client behaviour. Retrying clients saw end-to-end p99 ≈ 2.1 s at 200 VUs and
  ≈ 12.7 s with 1000 clients (L2c, L2b).
* **"Sustains 3000 req/s."** At 3000 req/s the setup saturated: about 2% timeouts, a third of requests not sent (L7).
* **"Provably fair" / "statistically proven random."** A χ² sanity check cannot prove fairness. Also, do not quote the textbook Pearson
  p-values (0.9+); that test is miscalibrated here (0.05% false-alarm rate in calibration).
* **"Even if Redis fails, the system stays correct and available."** Not tested. Per code, every booking returns 500 while Redis is down.
* **"Lock expiry cannot cause oversell"**, **multi-replica behaviour**, **Postgres or Redis failover**, **long-running soak stability**,
  **memory and CPU usage**: not tested.
* **"Different events are booked fully concurrently / scale linearly."** Not tested; every contention scenario used a single hot event at a time.
* **Latencies from `docs/AUDIT_SUMMARY.md`'s table** (e.g. "booking ~20 ms p50"): those were estimates, and they do not match the measurements (F11).
