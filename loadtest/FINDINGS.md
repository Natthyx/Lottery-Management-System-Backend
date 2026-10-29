# Findings: status after the fixes

The original findings (with Phase 0 answers and reproductions against `8cd482e`) are in
[baseline/FINDINGS.md](baseline/FINDINGS.md). Fixes are in commit `6ee3b75`; docs corrected in `6f4e8f8`.
Each row below was re-verified by the same suite ([REPORT.md](REPORT.md)) and, where possible, by an
integration test that runs in CI (`internal/integration/integration_test.go`).

| # | finding (baseline) | status | fix | verified by |
|---|---|---|---|---|
| F1 | Audit table `lottery_draws` mutable by the app's DB role (superuser + owner); `REVOKE … FROM PUBLIC` did nothing | **Fixed, with stated residual risk** | Migration `002_harden_audit.sql`: triggers reject UPDATE/DELETE/TRUNCATE on `lottery_draws` and `lottery_draw_seals` for every role, and freeze a drawn event. App runs as `lottery_app` (SELECT/INSERT only on audit tables, owns nothing); migrations run as owner via `MIGRATION_DATABASE_URL`. Each draw is SHA-256 sealed; `GET /results` reports `integrity` | L6 ([tamper.txt](results/l6/tamper.txt), [tamper_detection.txt](results/l6/tamper_detection.txt)); `TestAuditTrail_*` |
| F2 | Rate limiter bypassed by spoofed `X-Forwarded-For`/`X-Real-IP` | **Fixed** | `middleware.ClientIP`: headers honoured only from `TRUSTED_PROXIES` (default none), right-most untrusted hop. Plus a per-account login limit (10 / 15 min) | L8 A3/B2/C1–C4; `TestClientIP_*` |
| F3 | Try-once booking lock: on a hot event most simultaneous requests got "busy" and goodput fell with concurrency | **Fixed** | Per-event in-process FIFO queue → Redis lock with bounded backed-off wait → row lock. "Busy" only after `LOCK_WAIT_TIMEOUT` (10 s) | L2a, L2c (0 busy in all runs); `TestBooking_ConcurrentRequestsQueueAndNeverOversell` |
| F4 | Retrying clients starved; seats left empty while users were refused | **Fixed** | Same as F3 (FIFO order, no polling race) | L2b: 500/500 seats filled, 0 gave up, 1 attempt each; `TestEventGates_ServesWaitersInArrivalOrder` |
| F5 | 409 meant four things; duplicates hidden behind "full"/"not open" | **Fixed** | Machine-readable `code` on errors; busy is `503 event_busy` + `Retry-After`; duplicate checked first | L2/L3 classification by `code`; `TestBooking_DuplicateReportedBeforeFullOrClosed` |
| F6 | One `lottery_draws` row per entrant, not per draw (spec mismatch) | **Not a bug; unchanged** | Documented. "Exactly one draw" is checked as one `drawn_at`, ranks 1..n, and now exactly one seal | L4 |
| F7 | "Exactly one booking per user" only held if clients retried | **Fixed** (consequence of F3) | – | L3 no-retry: 1000/1000 users with exactly one booking, 3/3 runs |
| F8 | `/health`, `/ready` counted against the API rate limit | **Fixed** | Health routes mounted outside the limiter | L8 A4 (200 while limited) |
| F9 | `MAX_WINNERS` unused; compose did not forward settings; pool size hard-coded | **Fixed** | `MAX_WINNERS` enforced at event creation; `DB_MAX_CONNS`/`DB_MIN_CONNS`; compose forwards rate-limit windows, lock and proxy settings, and binds Postgres/Redis to 127.0.0.1 | config tests; compose config; [env capture](results/env/environment_raw.txt) |
| F10 | Draw allowed before `draw_at` | **Fixed** | `RunDraw` refuses with `409 draw_not_due` until `draw_at <= now()` (DB clock) | `TestDraw_RefusedBeforeDrawAt`; smoke test step 15a; L4/L5 wait for `draw_at` |
| F11 | Docs claimed immutability, estimated latencies, "max 1 booking per 5 s", "stays correct if Redis fails" | **Fixed** | Docs rewritten to match code and measurements (`6f4e8f8`) | grep for those claims finds only "do not claim" notes |
| F12 | Harness/environment observations | Recorded | – | [baseline/results/superseded](baseline/results/superseded/README.md), [results/superseded](results/superseded/README.md) |

## New observations in the after-fix run

* **N1: Docker Desktop port-forwarder exhaustion (environment, not the app).** Twice, back-to-back contention runs exhausted the
  Docker VM's 10,536 ephemeral ports. Clients saw EOF, and Docker's VM log shows `connect: cannot assign requested address`. The app was
  healthy throughout (0 restarts, no errors, and reachable from inside the Docker network). It hit this run and not the baseline, likely because
  queued requests keep connections open longer. The harness now waits for ports to free up between runs. No invariant was violated even in
  the interrupted runs. Evidence: [results/superseded/README.md](results/superseded/README.md).
* **N2: Reads slightly slower.** At 1500 req/s, p99 went from 64.1 to 73.7 ms and `/results` p99 from 91.2 to 106 ms. A plausible cause is
  `GET /results` now running an extra query and hashing every row to verify the seal; this was not isolated. If it matters, cache the
  verification per event (results are final once drawn).
* **N3: One low fairness p-value.** API run 1: adjusted p = 0.006. A 5000-draw API follow-up gave p = 0.955. See REPORT L5.
* **N4: Approximate sliding window.** In one L8 run the limiter allowed 101 requests (consistent with httprate's sliding-window
  approximation at a minute boundary). This is not a bypass.

## Residual risks (not fixed; owner decisions)

1. **Database superuser / owner credentials.** Whoever holds them can disable triggers or set `session_replication_role = replica`
   and rewrite history. This is detected by the seal (L6), not prevented. To make detection trustworthy, ship logs (each draw's digest is logged
   at warn) to storage the DB admins cannot rewrite, or publish digests. Keep `MIGRATION_DATABASE_URL` out of the running app if migrations can run as a separate step.
2. **Per-replica state.** The FIFO queue and the rate-limit counters are in process memory. With several replicas: ordering is FIFO per
   replica only (correctness still holds via the row lock), and rate limits multiply by the replica count. Moving limits into Redis
   would fix the latter. Not tested.
3. **Per-account login limit can lock out a victim** for 15 minutes if an attacker deliberately fails their logins. This is the usual trade-off;
   tune `AUTH_ACCOUNT_RATE_LIMIT_*` or add CAPTCHA/2FA.
4. **Hot-event latency.** Each event is still one serial critical section (about 0.5 ms per booking here). Very large simultaneous crowds wait
   in line, and past `LOCK_WAIT_TIMEOUT` they get 503. A counter-column booking (`UPDATE … SET booked = booked + 1 WHERE booked < capacity`)
   would shorten the section. Not implemented.
5. **Existing deployments.** The `lottery_app` role is created only on a fresh Postgres volume (`deploy/postgres/initdb`). Existing databases
   need the role created by hand before switching `DATABASE_URL` (documented in README).
6. **Not tested:** Redis/Postgres outages, lock-TTL expiry under load, multi-replica runs, long soak, resource usage, TLS.
