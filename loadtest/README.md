# Load tests and benchmarks

Everything needed to reproduce the numbers in [REPORT.md](REPORT.md) (current code)
and [baseline/REPORT.md](baseline/REPORT.md) (commit `8cd482e`, before the fixes; it produced
[baseline/FINDINGS.md](baseline/FINDINGS.md)). The status of every finding is in [FINDINGS.md](FINDINGS.md).
All numbers are **local, single machine**: k6, the app, Postgres and Redis share one laptop (see
[results/ENVIRONMENT.md](results/ENVIRONMENT.md)).

## Run it

Prerequisites: Docker (Compose v2.24 or later, for `!reset`/`!override` in the override
file), k6, Go (for the helpers), `jq`, `openssl`, `perl` (all present on stock macOS
except k6 and Go: `brew install k6 go jq`).

```bash
loadtest/run.sh            # full suite: fresh stack, all scenarios, 3 runs each (about 40 min on the test laptop)
loadtest/run.sh l4 l6      # selected scenarios against an already initialised stack
RUNS=1 loadtest/run.sh     # quicker smoke pass
```

`run.sh` does the following:

1. Generates throwaway secrets (JWT secret, admin password, user password,
   Postgres password) into `loadtest/.state/` (gitignored). Nothing secret is
   committed; tokens never leave that folder.
2. Starts an isolated stack: compose project `lottery-loadtest`, containers
   `lt_app`, `lt_postgres` and `lt_redis`, with no Postgres/Redis ports published, and the app on
   `127.0.0.1:18080`. It never touches a developer's `lottery_*` stack or its volume.
   Before any reset it checks that the containers carry that project's label.
3. Runs the scenarios and writes raw output to `loadtest/results/`.
4. Writes `results/AGGREGATE.md`: median and [min – max] across runs, computed from the
   raw k6 JSON by `cmd/aggregate`.

Resets between runs truncate `lottery_draw_seals`, `lottery_draws`, `bookings` and `events`
(users are kept so JWTs stay valid) and delete leftover `lock:booking:event:*` keys. Since the
audit tables refuse TRUNCATE, the reset runs as the superuser with
`session_replication_role = replica`, which is test-only and is exactly the bypass the draw seals detect (L6).
`init` drops and recreates only the `lottery-loadtest_postgres_data` volume.

## Configuration changes (and only these)

[docker-compose.loadtest.yml](docker-compose.loadtest.yml) is used on top of the
unchanged base `docker-compose.yml`:

| setting | test value | code default | why |
|---|---|---|---|
| `API_RATE_LIMIT_REQUESTS` | 10,000,000 | 100 | one load generator = one IP; would otherwise be mostly 429s |
| `AUTH_RATE_LIMIT_REQUESTS` | 10,000,000 | 10 | setup logs in 1000 users |
| `AUTH_ACCOUNT_RATE_LIMIT_REQUESTS` | 10,000,000 | 10 | per-account login limit (added by the fix) |
| `API_RATE_LIMIT_WINDOW`, `AUTH_RATE_LIMIT_WINDOW`, `AUTH_ACCOUNT_RATE_LIMIT_WINDOW` | 1m, 1m, 15m | same | |
| `TRUSTED_PROXIES` | empty | empty | L8 section C sets it to the one address the app sees for host requests |
| `LOG_LEVEL` | warn | info | suppress one log line per request (L8 and L9 use `info`) |

L8 runs with all rate-limit values set back to the code defaults. Container names, ports and
the project name are changed for isolation only. The app runs exactly as docker-compose.yml configures it,
including the least-privilege `lottery_app` database role.

## Scenarios

| id | what it does | what it can prove |
|---|---|---|
| Phase 0 | catalog queries on roles, owner, ACL, triggers ([sql/phase0_roles.sql](sql/phase0_roles.sql)) | who the app connects as and what protects `lottery_draws` |
| L1 | register and log in 1000 users once ([cmd/setup-users](cmd/setup-users)) | setup only; JWTs are reused, so bcrypt never runs in a measured phase |
| L2a | N users (200, 1000) each send **one** booking at the same moment; no retry | what the per-event Redis lock does to simultaneous requests |
| L2b | same, clients retry the "busy" response (bounded and unbounded); capacity N/2 | all eligible users get in; **no oversell** under contention |
| L2c | stepped VU ramp 1→200 on one hot event at a time, with and without retry | how throughput and tail latency change as concurrency rises |
| L3 | each user sends 5 identical bookings in parallel | exactly one booking per user |
| L4 | 20 simultaneous `POST /draw` (after `draw_at`) | exactly one draw and one seal; DB, `GET /results` and the 200 body agree; integrity `verified` |
| L5 | chi-square on 10 participants / 5 winners, by calling the algorithm directly and through the API | sanity check of uniformity (not a proof) |
| L6 | `UPDATE`/`DELETE`/`TRUNCATE`/trigger tampering on the audit tables as the runtime role, the owner/superuser and a non-superuser owner, all rolled back ([sql/l6_tamper.sql](sql/l6_tamper.sql)); then a **committed** superuser winner swap that bypasses triggers | what blocks each attempt, and whether a bypass is detected by the draw seal |
| L7 | constant-arrival-rate reads at 300, 1500 and 3000 req/s for 60 s | read latency percentiles and error rate; where the single machine saturates |
| L8 | default limits: 120 × `GET /events`, 15 × `POST /auth/login`, spoofed IP headers, `/health` under limit; then behind a trusted proxy: forwarded IPs and the per-account login limit | limiter works at defaults and cannot be bypassed by headers; per-account limit holds across IPs |
| L9 | SIGTERM during a booking burst | in-flight requests complete; no partial or orphaned bookings |

Invariants are checked in SQL after every run ([sql/](sql/)). The booking check also
compares the number of rows in `bookings` with the number of 201 responses k6 saw,
so a booking that was committed without the client being told, or the reverse,
would show up.

## Layout

```
loadtest/
  run.sh, lib/common.sh            entry point and helpers
  docker-compose.loadtest.yml      the only config override
  k6/                              k6 scripts (lib.js = shared outcome classification)
  sql/                             reset, invariant and tamper SQL
  cmd/setup-users                  L1
  cmd/fairness, cmd/fairness-api   L5 (algorithm level, service level)
  cmd/aggregate                    median/min/max tables from raw k6 JSON
  stats/                           chi-square p-values (stdlib only, unit-tested)
  results/                         raw outputs of the current run; every number in REPORT.md cites a file here
  baseline/                        report, findings and raw outputs of the run against 8cd482e
```

## Reading k6 numbers

* `st_*` counters are the outcome distribution, classified by the response `code`:
  `st_503_busy` (retryable), `st_409_already_booked`, `st_409_full`, `st_409_not_open`. (In the baseline,
  busy was a 409 and the counter was `st_409_busy`.)
* `http_req_duration` covers every request, including fast 409s. `book_201_duration` covers
  only successful bookings. `book_end_to_end_duration` is per logical booking, including
  retries and backoff sleeps.
* Throughput labelled "per s (run)" is count / k6 `testRunDurationMs`. For the
  sweep, "per s (stage)" is count / stage length (10 s).
