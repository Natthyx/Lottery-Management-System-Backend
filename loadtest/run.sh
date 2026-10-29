#!/usr/bin/env bash
# Single entry point for the load-test suite. See loadtest/README.md.
#
#   loadtest/run.sh                 # everything, from a fresh test stack
#   loadtest/run.sh l2 l4           # selected scenarios (stack must be set up: run "init" once)
#
# Scenarios: init (fresh stack + env capture + phase0 + L1), l2, l2u, l3, l4, l5,
#            l6, l7, l8, l9, aggregate.
set -euo pipefail
source "$(dirname "$0")/lib/common.sh"

RUNS="${RUNS:-3}"
N_LIST="${N_LIST:-200 1000}"
SWEEP_LEVELS="${SWEEP_LEVELS:-1,5,10,25,50,100,200}"
SWEEP_STAGE_S="${SWEEP_STAGE_S:-10}"
SWEEP_EVENTS_PER_STAGE=200
L7_RATES="${L7_RATES:-300 1500 3000}"

cd "$REPO_DIR"
ensure_secrets

meta() { # meta DIR DESCRIPTION [stage_seconds]
  mkdir -p "$1"
  jq -n --arg d "$2" --argjson s "${3:-0}" '{description: $d, stage_seconds: $s}' >"$1/meta.json"
}

st201() { jq '.metrics.st_201.values.count // 0' "$1"; }

# bulk_events COUNT CAPACITY WINNERS: test setup only (not measured). Inserted
# with SQL because creating ~1400 events through the API would add minutes
# of setup without testing anything; the API path for event creation is
# exercised by every other scenario.
bulk_events() {
  psql_q "INSERT INTO events (title, capacity, winner_count, draw_at, created_by)
          SELECT 'bulk ' || g, $2, $3, now() + interval '7 days', (SELECT min(id) FROM users WHERE role = 'admin')
          FROM generate_series(1, $1) g" >/dev/null
}

warmup() {
  log "warm-up"
  reset_db
  local ev
  ev=$(create_event 1000 10 warmup)
  RESULTS_DIR="$RESULTS_DIR/warmup" run_k6 "warmup_$1" l2_hot_event.js -e N=200 -e VUS=20 -e EVENT_ID="$ev" -e RETRY=1
  reset_db
}

capture_env() {
  local d="$RESULTS_DIR/env"
  mkdir -p "$d"
  {
    echo "## date"; date -u +%Y-%m-%dT%H:%M:%SZ
    echo "## git"; git -C "$REPO_DIR" rev-parse HEAD; git -C "$REPO_DIR" status --short
    echo "## host"; sw_vers 2>/dev/null || uname -a
    sysctl -n machdep.cpu.brand_string hw.ncpu hw.memsize 2>/dev/null || true
    echo "## docker"; docker version --format 'client={{.Client.Version}} server={{.Server.Version}}'
    docker info --format 'docker_desktop_vm_cpus={{.NCPU}} docker_desktop_vm_mem_bytes={{.MemTotal}} os={{.OperatingSystem}} kernel={{.KernelVersion}}'
    docker compose version
    echo "## k6"; k6 version
    echo "## go (host, for helpers)"; go version
    echo "## app build image"; grep -m1 '^FROM golang' "$REPO_DIR/Dockerfile"
    echo "## postgres"; psql_q 'SELECT version()'; psql_q 'SHOW max_connections'
    echo "## redis"; docker exec "$REDIS_CONTAINER" redis-server --version
    echo "## app env (secrets redacted)"
    docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$APP_CONTAINER" |
      sed -E 's/^(JWT_SECRET|BOOTSTRAP_ADMIN_PASSWORD|DATABASE_URL)=.*/\1=<redacted>/' | sort
    echo "## container resource limits (0 = none)"
    for c in "$APP_CONTAINER" "$PG_CONTAINER" "$REDIS_CONTAINER"; do
      docker inspect -f "$c nano_cpus={{.HostConfig.NanoCpus}} memory={{.HostConfig.Memory}}" "$c"
    done
  } >"$d/environment_raw.txt" 2>&1
}

do_init() {
  ensure_secrets
  log "fresh test stack (drops only the lottery-loadtest volume)"
  if docker inspect "$PG_CONTAINER" >/dev/null 2>&1; then assert_test_stack; fi
  stack_destroy || true
  stack_up
  capture_env
  mkdir -p "$RESULTS_DIR/phase0"
  psql_f "$LT_DIR/sql/phase0_roles.sql" >"$RESULTS_DIR/phase0/db_roles_and_privileges.txt" 2>&1

  log "L1: register + login 1000 users (setup, bcrypt cost 12)"
  mkdir -p "$RESULTS_DIR/l1"
  (
    set -a; source "$COMPOSE_ENV"; set +a
    go run ./loadtest/cmd/setup-users -base "$BASE_URL" -n 1000 -c 8 \
      -password-file "$STATE_DIR/user_password" -admin-email "$(admin_email)" -out "$STATE_DIR/tokens.json"
  ) | tee "$RESULTS_DIR/l1/setup_users.txt"
  psql_q "SELECT role, count(*) FROM users GROUP BY role ORDER BY role" >>"$RESULTS_DIR/l1/setup_users.txt"
}

do_l2() {
  warmup l2
  local n r d ev
  for n in $N_LIST; do
    # L2a: every user sends ONE request at the same moment (VUs = N), no retry.
    d="$RESULTS_DIR/l2/single_noretry_n$n"
    meta "$d" "L2a hot event, N=$n users, VUs=$n, shared-iterations, one POST /book per user, no client retry. Capacity $n."
    for r in $(seq 1 "$RUNS"); do
      reset_db; ev=$(create_event "$n" 10 "l2a")
      RESULTS_DIR="$d" run_k6 "run$r" l2_hot_event.js -e N="$n" -e VUS="$n" -e EVENT_ID="$ev" -e RETRY=0
      psql_f "$LT_DIR/sql/booking_invariants.sql" -v expected_bookings="$(st201 "$d/run$r.json")" >"$d/sql_run$r.txt"
    done
    # L2b: same, but clients retry the busy 409 (bounded: 200 attempts), and
    # capacity is N/2 so the capacity check (oversell) can be exercised.
    d="$RESULTS_DIR/l2/retry200_capacity_half_n$n"
    meta "$d" "L2b hot event, N=$n users, VUs=$n, shared-iterations, client retries busy-409 (5-25 ms jitter, max 200 attempts, then gives up). Capacity $((n / 2))."
    for r in $(seq 1 "$RUNS"); do
      reset_db; ev=$(create_event $((n / 2)) 10 "l2b")
      RESULTS_DIR="$d" run_k6 "run$r" l2_hot_event.js -e N="$n" -e VUS="$n" -e EVENT_ID="$ev" -e RETRY=1 -e MAX_ATTEMPTS=200
      psql_f "$LT_DIR/sql/booking_invariants.sql" -v expected_bookings="$(st201 "$d/run$r.json")" >"$d/sql_run$r.txt"
    done
  done
  do_l2_sweep
}

# L2b-unbounded: clients retry busy until they get a definitive answer
# (201, full, ...), so capacity is always reached and oversell is really tested.
do_l2u() {
  warmup l2u
  local n r d ev
  for n in $N_LIST; do
    d="$RESULTS_DIR/l2/retry_unbounded_capacity_half_n$n"
    meta "$d" "L2b-unbounded hot event, N=$n users, VUs=$n, shared-iterations, client retries busy-409 until a non-busy answer (5-25 ms jitter, cap 100000 attempts). Capacity $((n / 2))."
    for r in $(seq 1 "$RUNS"); do
      reset_db; ev=$(create_event $((n / 2)) 10 "l2u")
      RESULTS_DIR="$d" run_k6 "run$r" l2_hot_event.js -e N="$n" -e VUS="$n" -e EVENT_ID="$ev" -e RETRY=1 -e MAX_ATTEMPTS=100000
      psql_f "$LT_DIR/sql/booking_invariants.sql" -v expected_bookings="$(st201 "$d/run$r.json")" >"$d/sql_run$r.txt"
      psql_q "SELECT 'bookings=' || count(*) || ' capacity=' || $((n / 2)) FROM bookings WHERE event_id = $ev" >>"$d/sql_run$r.txt"
    done
  done
}

do_l2_sweep() {
  local r d
  # L2c: stepped ramp of VUs on one hot event at a time.
  local retry name
  for retry in 0 1; do
    name=$([[ $retry == 1 ]] && echo retry || echo noretry)
    d="$RESULTS_DIR/l2/sweep_$name"
    meta "$d" "L2c concurrency sweep, constant-vus stages $SWEEP_LEVELS, ${SWEEP_STAGE_S}s each, closed loop, 1000 users, all VUs of a stage on the same event. retry=$retry (if 1: busy-409 retried after 5-25 ms jitter, max 200 attempts). Per-stage rates are count / ${SWEEP_STAGE_S}s." "$SWEEP_STAGE_S"
    for r in $(seq 1 "$RUNS"); do
      reset_db
      bulk_events $((7 * SWEEP_EVENTS_PER_STAGE)) 1000 10
      RESULTS_DIR="$d" run_k6 "run$r" l2_sweep.js -e N=1000 -e LEVELS="$SWEEP_LEVELS" -e STAGE_S="$SWEEP_STAGE_S" \
        -e EVENTS_PER_STAGE="$SWEEP_EVENTS_PER_STAGE" -e RETRY="$retry"
      psql_f "$LT_DIR/sql/booking_invariants.sql" -v expected_bookings="$(st201 "$d/run$r.json")" >"$d/sql_run$r.txt"
    done
  done
}

do_l3() {
  warmup l3
  local n r d ev retry name
  for n in $N_LIST; do
    for retry in 1 0; do
      name=$([[ $retry == 1 ]] && echo retry || echo noretry)
      d="$RESULTS_DIR/l3/dup5_${name}_n$n"
      meta "$d" "L3 duplicates, N=$n users, each sends 5 identical POST /book in parallel (http.batch), VUs=min(N,200). retry=$retry (busy-409 retried after 5-25 ms jitter until a non-busy answer, cap 100000 attempts). Capacity 5N."
      for r in $(seq 1 "$RUNS"); do
        reset_db; ev=$(create_event $((n * 5)) 10 "l3")
        RESULTS_DIR="$d" run_k6 "run$r" l2_hot_event.js -e MODE=duplicates -e DUP=5 -e N="$n" \
          -e VUS=$((n < 200 ? n : 200)) -e EVENT_ID="$ev" -e RETRY="$retry" -e MAX_ATTEMPTS=100000
        psql_f "$LT_DIR/sql/l3_invariants.sql" -v event_id="$ev" -v n_users="$n" -v require_exactly_one="$retry" >"$d/sql_run$r.txt"
        psql_f "$LT_DIR/sql/booking_invariants.sql" -v expected_bookings="$(st201 "$d/run$r.json")" >>"$d/sql_run$r.txt"
      done
    done
  done
}

do_l4() {
  local r d ev
  d="$RESULTS_DIR/l4/draw20_n1000"
  meta "$d" "L4 concurrent draws: event with 1000 bookings, capacity 1000, winner_count 100, closed; 20 simultaneous POST /draw (per-vu-iterations, 20 VUs x 1)."
  for r in $(seq 1 "$RUNS"); do
    reset_db; ev=$(create_event 1000 100 "l4")
    RESULTS_DIR="$d/prep" run_k6 "prep_run$r" l2_hot_event.js -e N=1000 -e VUS=50 -e EVENT_ID="$ev" -e RETRY=1
    close_event "$ev"
    RESULTS_DIR="$d" run_k6 "run$r" l4_draws.js -e EVENT_ID="$ev" -e CONCURRENCY=20
    psql_f "$LT_DIR/sql/l4_invariants.sql" -v event_id="$ev" >"$d/sql_run$r.txt"
    # Three views of the same draw, normalised to "rank user W|L" lines.
    psql_q "SELECT draw_rank || ' ' || winner_user_id || ' ' ||
              CASE WHEN draw_rank <= (SELECT winner_count FROM events WHERE id = $ev) THEN 'W' ELSE 'L' END
            FROM lottery_draws WHERE event_id = $ev ORDER BY draw_rank" >"$d/draw_db_run$r.txt"
    curl -sS "$BASE_URL/events/$ev/results" |
      jq -r '(.data.winners[] | "\(.draw_rank) \(.winner_user_id) W"), (.data.waitlist[] | "\(.draw_rank) \(.winner_user_id) L")' \
      >"$d/draw_api_results_run$r.txt"
    { grep -o 'DRAW_200_BODY .*' "$d/run$r.txt" || true; } | head -1 | sed 's/^DRAW_200_BODY //' |
      jq -r '(.data.winners[] | "\(.draw_rank) \(.winner_user_id) W"), (.data.waitlist[] | "\(.draw_rank) \(.winner_user_id) L")' \
      >"$d/draw_post_response_run$r.txt" || true
    {
      echo "lines: db=$(wc -l <"$d/draw_db_run$r.txt") api_results=$(wc -l <"$d/draw_api_results_run$r.txt") post_response=$(wc -l <"$d/draw_post_response_run$r.txt")"
      if diff -q "$d/draw_db_run$r.txt" "$d/draw_api_results_run$r.txt" >/dev/null; then echo "db vs GET /results: IDENTICAL"; else echo "db vs GET /results: DIFFERENT"; fi
      if diff -q "$d/draw_db_run$r.txt" "$d/draw_post_response_run$r.txt" >/dev/null; then echo "db vs POST /draw 200 body: IDENTICAL"; else echo "db vs POST /draw 200 body: DIFFERENT"; fi
      echo "winners in GET /results: $(grep -c ' W$' "$d/draw_api_results_run$r.txt")  waitlist: $(grep -c ' L$' "$d/draw_api_results_run$r.txt")"
    } >"$d/compare_run$r.txt"
  done
}

do_l5() {
  local d="$RESULTS_DIR/l5" r
  mkdir -p "$d"
  go build -o "$STATE_DIR/fairness" ./loadtest/cmd/fairness
  for r in $(seq 1 "$RUNS"); do
    "$STATE_DIR/fairness" -k 10 -w 5 -draws 5000 >"$d/algorithm_5000_run$r.txt"
  done
  "$STATE_DIR/fairness" -k 10 -w 5 -draws 200000 >"$d/algorithm_200000.txt"
  "$STATE_DIR/fairness" -k 10 -w 5 -draws 5000 -repeat 2000 >"$d/algorithm_calibration_2000x5000.txt"
  for r in $(seq 1 "$RUNS"); do
    reset_db
    go run ./loadtest/cmd/fairness-api -base "$BASE_URL" -tokens "$STATE_DIR/tokens.json" -k 10 -w 5 -draws 1000 -c 8 \
      >"$d/api_1000_run$r.txt" 2>"$d/api_1000_run$r.stderr.txt"
  done
}

do_l6() {
  # Needs a drawn event: build one exactly like L4 (without the k6 draw).
  local d="$RESULTS_DIR/l6" ev
  mkdir -p "$d"
  reset_db; ev=$(create_event 1000 100 "l6")
  RESULTS_DIR="$d/prep" run_k6 prep l2_hot_event.js -e N=1000 -e VUS=50 -e EVENT_ID="$ev" -e RETRY=1
  close_event "$ev"
  api POST "/events/$ev/draw" | jq -c '{success, winners: (.data.winners | length), waitlist: (.data.waitlist | length)}' >"$d/draw.txt"
  psql_f "$LT_DIR/sql/l6_tamper.sql" >"$d/tamper.txt" 2>&1 || true
}

do_l7() {
  local d ev rate r
  reset_db; ev=$(create_event 1000 100 "l7 drawn")
  RESULTS_DIR="$RESULTS_DIR/l7/prep" run_k6 prep l2_hot_event.js -e N=1000 -e VUS=50 -e EVENT_ID="$ev" -e RETRY=1
  close_event "$ev"; api POST "/events/$ev/draw" >/dev/null
  for i in $(seq 2 50); do create_event 1000 10 "l7 open $i" >/dev/null; done
  {
    curl -sS -o /dev/null -w 'GET /events bytes=%{size_download}\n' "$BASE_URL/events"
    curl -sS -o /dev/null -w 'GET /events/{id} bytes=%{size_download}\n' "$BASE_URL/events/$ev"
    curl -sS -o /dev/null -w 'GET /events/{id}/results bytes=%{size_download}\n' "$BASE_URL/events/$ev/results"
    psql_q "SELECT 'events=' || count(*) FROM events"; psql_q "SELECT 'bookings=' || count(*) FROM bookings"
  } >"$RESULTS_DIR/l7/dataset.txt"
  RESULTS_DIR="$RESULTS_DIR/warmup" run_k6 warmup_l7 l7_reads.js -e RATE=300 -e DURATION=10s -e EVENT_ID="$ev"
  for rate in $L7_RATES; do
    d="$RESULTS_DIR/l7/rate$rate"
    meta "$d" "L7 read load, constant-arrival-rate $rate req/s total (1/3 each: GET /events, GET /events/{id}, GET /events/{id}/results), 60 s. Event $ev is drawn with 1000 entrants."
    for r in $(seq 1 "$RUNS"); do
      RESULTS_DIR="$d" run_k6 "run$r" l7_reads.js -e RATE="$rate" -e DURATION=60s -e EVENT_ID="$ev"
      sleep 5
    done
  done
}

now_ms() { perl -MTime::HiRes=time -e 'printf "%d\n", time * 1000'; }

restart_app_fresh() {
  # Restart resets httprate's in-memory counters. Readiness: wait for the
  # "HTTP server listening" log line, then poll GET /health until it returns
  # 200. Attempts that get no HTTP response (Docker Desktop's port forward
  # lags the app by a moment) never reach the app and are not counted; the
  # single successful probe IS counted by the general limiter, so the API
  # test below expects 99 more 200s, not 100. /health is not an /auth route,
  # so the auth budget is untouched.
  local since code
  since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  docker restart "$APP_CONTAINER" >/dev/null
  for _ in $(seq 1 60); do
    docker logs --since "$since" "$APP_CONTAINER" 2>&1 | grep -q 'HTTP server listening' && break
    sleep 0.5
  done
  for _ in $(seq 1 100); do
    code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/health")
    [[ "$code" == "200" ]] && { echo "readiness: 1 successful GET /health probe (counts against the general limit)"; return 0; }
    [[ "$code" == "000" ]] || die "unexpected /health status $code after restart"
    sleep 0.1
  done
  die "app not reachable after restart"
}

do_l8() {
  local d="$RESULTS_DIR/l8" r codes
  mkdir -p "$d"
  log "L8: restarting app with the code-default rate limits"
  stack_up LT_API_RATE_LIMIT_REQUESTS=100 LT_API_RATE_LIMIT_WINDOW=1m \
    LT_AUTH_RATE_LIMIT_REQUESTS=10 LT_AUTH_RATE_LIMIT_WINDOW=1m LT_LOG_LEVEL=info
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$APP_CONTAINER" | grep RATE_LIMIT | sort >"$d/limits_in_effect.txt"
  for r in $(seq 1 "$RUNS"); do
    {
      restart_app_fresh
      echo "## A. general API limit (100/min): 120 sequential GET /events from one IP; 1 request already used by the probe"
      codes=$(for _ in $(seq 1 120); do curl -s -o /dev/null -w '%{http_code}\n' "$BASE_URL/events"; done)
      echo "$codes" | uniq -c
      echo "first_429_at_request=$(echo "$codes" | grep -n 429 | head -1 | cut -d: -f1)"
      echo "## A2. one more GET /events, headers"
      curl -s -D - -o /dev/null "$BASE_URL/events" | grep -iE '^(HTTP|x-ratelimit|retry-after)'
      echo "## A3. same request with a spoofed X-Forwarded-For (RealIP middleware runs before the limiter)"
      for i in 1 2 3; do curl -s -o /dev/null -w '%{http_code}\n' -H "X-Forwarded-For: 203.0.113.$i" "$BASE_URL/events"; done | uniq -c

      restart_app_fresh
      echo "## B. auth limit: 15 sequential POST /auth/login with body {} (401 without running bcrypt)"
      codes=$(for _ in $(seq 1 15); do curl -s -o /dev/null -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' -d '{}' "$BASE_URL/auth/login"; done)
      echo "$codes" | uniq -c
      echo "first_429_at_request=$(echo "$codes" | grep -n 429 | head -1 | cut -d: -f1)"
      echo "## B2. same with a spoofed X-Real-IP"
      for i in 1 2 3; do curl -s -o /dev/null -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' -H "X-Real-IP: 198.51.100.$i" -d '{}' "$BASE_URL/auth/login"; done | uniq -c
    } >"$d/run$r.txt" 2>&1
  done
  log "L8: restoring raised limits"
  stack_up
}

do_l9() {
  local d="$RESULTS_DIR/l9" r ts pid
  mkdir -p "$d"
  meta "$d" "L9 graceful shutdown: 50 VUs closed-loop booking across 50 events for 20 s; SIGTERM sent to the app at ~6 s."
  stack_up LT_LOG_LEVEL=info
  for r in $(seq 1 "$RUNS"); do
    reset_db
    bulk_events 50 1000 10
    RESULTS_DIR="$d" run_k6 "run$r" l9_shutdown.js -e N=1000 -e EVENTS=50 -e VUS=50 -e DURATION=20s &
    pid=$!
    sleep 6
    local restarts_before kb ka
    restarts_before=$(docker inspect -f '{{.RestartCount}}' "$APP_CONTAINER")
    ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    kb=$(now_ms)
    docker kill -s TERM "$APP_CONTAINER" >/dev/null
    ka=$(now_ms)
    wait "$pid" || true
    {
      echo "sigterm: docker kill started_ms=$kb returned_ms=$ka (host clock)"
      echo "restart_count_before=$restarts_before after=$(docker inspect -f '{{.RestartCount}}' "$APP_CONTAINER") state_after_run=$(docker inspect -f '{{.State.Status}}' "$APP_CONTAINER")"
      echo "## app log lines after SIGTERM (http_request lines omitted)"
      docker logs --since "$ts" "$APP_CONTAINER" 2>&1 | { grep -v '"http_request"' || true; } | head -40
      echo "## per-request outcome by phase (from the R lines in run$r.txt)"
      echo "## in_flight = started before docker kill began and finished after it"
      awk -v kb="$kb" '$1 == "R" {
          phase = ($2 < kb && $3 >= kb) ? "in_flight_at_sigterm" : ($2 >= kb ? "started_after_sigterm" : "finished_before_sigterm")
          key = phase " " $4 " status=" $5 " k6_error_code=" $6; n[key]++
        } END { for (k in n) print n[k], k }' "$d/run$r.txt" | sort -k2,2 -k1,1nr
    } >"$d/timeline_run$r.txt"
    # The app may or may not have been restarted by Docker; make sure it is up.
    stack_up LT_LOG_LEVEL=info
    psql_f "$LT_DIR/sql/booking_invariants.sql" -v expected_bookings="$(st201 "$d/run$r.json")" >"$d/sql_run$r.txt"
  done
  stack_up
}

do_aggregate() {
  go run ./loadtest/cmd/aggregate -results "$RESULTS_DIR" >"$RESULTS_DIR/AGGREGATE.md"
  log "wrote $RESULTS_DIR/AGGREGATE.md"
}

steps=("$@")
[[ ${#steps[@]} -eq 0 ]] && steps=(init l2 l2u l3 l4 l6 l5 l7 l8 l9 aggregate)
for s in "${steps[@]}"; do
  log "=== $s ==="
  "do_$s"
done
log "done"
