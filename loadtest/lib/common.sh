# shellcheck shell=bash
# Shared helpers for loadtest/run.sh. Source, don't execute.
#
# Safety: every destructive helper below operates only on the compose
# project "lottery-loadtest" and its containers lt_postgres / lt_redis /
# lt_app. assert_test_stack refuses to continue if those containers do not
# carry that project's compose label.

LT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_DIR="$(cd "$LT_DIR/.." && pwd)"
STATE_DIR="$LT_DIR/.state"
RESULTS_DIR="${RESULTS_DIR:-$LT_DIR/results}"
COOLDOWN_LOG="$RESULTS_DIR/cooldown.log" # fixed at source time; run_k6 overrides RESULTS_DIR per call
PROJECT="lottery-loadtest"
LT_PORT="${LT_PORT:-18080}"
BASE_URL="http://127.0.0.1:${LT_PORT}"
COMPOSE_ENV="$STATE_DIR/compose.env"

PG_CONTAINER=lt_postgres
REDIS_CONTAINER=lt_redis
APP_CONTAINER=lt_app

log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
die() { log "FATAL: $*"; exit 1; }

dc() {
  docker compose -p "$PROJECT" --env-file "$COMPOSE_ENV" \
    -f "$REPO_DIR/docker-compose.yml" -f "$LT_DIR/docker-compose.loadtest.yml" "$@"
}

# ensure_secrets generates throwaway credentials once per checkout. They live
# in .state/ (gitignored) and are only valid for the local test stack.
ensure_secrets() {
  mkdir -p "$STATE_DIR"
  chmod 700 "$STATE_DIR"
  if [[ ! -f "$COMPOSE_ENV" ]]; then
    umask 077
    {
      echo "JWT_SECRET=$(openssl rand -hex 32)"
      echo "BOOTSTRAP_ADMIN_EMAIL=lt-admin@loadtest.local"
      echo "BOOTSTRAP_ADMIN_PASSWORD=$(openssl rand -hex 16)"
      echo "BOOTSTRAP_ADMIN_NAME=Loadtest-Admin"
      echo "POSTGRES_PASSWORD=$(openssl rand -hex 16)"
    } >"$COMPOSE_ENV"
    openssl rand -hex 16 >"$STATE_DIR/user_password"
    log "generated throwaway secrets in $STATE_DIR"
  fi
  # Password of the least-privilege runtime role (added after the baseline).
  if ! grep -q '^APP_DB_PASSWORD=' "$COMPOSE_ENV"; then
    (umask 077; echo "APP_DB_PASSWORD=$(openssl rand -hex 16)" >>"$COMPOSE_ENV")
  fi
}

admin_email()    { grep '^BOOTSTRAP_ADMIN_EMAIL=' "$COMPOSE_ENV" | cut -d= -f2-; }
admin_password() { grep '^BOOTSTRAP_ADMIN_PASSWORD=' "$COMPOSE_ENV" | cut -d= -f2-; }

assert_test_stack() {
  local c p
  for c in "$PG_CONTAINER" "$REDIS_CONTAINER" "$APP_CONTAINER"; do
    p=$(docker inspect -f '{{ index .Config.Labels "com.docker.compose.project" }}' "$c" 2>/dev/null) ||
      die "container $c not found"
    [[ "$p" == "$PROJECT" ]] || die "container $c belongs to project '$p', not '$PROJECT'; refusing"
  done
}

wait_ready() {
  local deadline=$((SECONDS + ${1:-180}))
  until [[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/ready")" == "200" ]]; do
    ((SECONDS < deadline)) || die "app not ready at $BASE_URL/ready"
    sleep 1
  done
}

# stack_up [extra env assignments...] starts (or recreates) the stack with the
# given LT_* overrides, e.g. stack_up LT_API_RATE_LIMIT_REQUESTS=100.
stack_up() {
  (
    for kv in "$@"; do export "${kv?}"; done
    dc up -d --build >&2
  ) || die "docker compose up failed"
  assert_test_stack
  wait_ready
}

stack_destroy() {
  # Only this project's containers, network and named volume.
  dc down -v --remove-orphans >&2
}

psql_q() { # psql_q "SQL" -> unaligned, tuples-only output
  docker exec -i "$PG_CONTAINER" psql -U lottery -d lottery_db -X -A -t -v ON_ERROR_STOP=1 -c "$1"
}

psql_f() { # psql_f file.sql [-v name=value ...] -> full psql output (echoes queries)
  local f="$1"; shift
  docker exec -i "$PG_CONTAINER" psql -U lottery -d lottery_db -X -e -v ON_ERROR_STOP=1 "$@" <"$f"
}

# reset_db wipes events, bookings and draws but keeps users, so JWTs stay
# valid between scenarios. Also clears any leftover booking locks.
reset_db() {
  assert_test_stack
  psql_f "$LT_DIR/sql/reset.sql" >/dev/null
  docker exec "$REDIS_CONTAINER" sh -c \
    "redis-cli --scan --pattern 'lock:booking:event:*' | xargs -r redis-cli del" >/dev/null
}

admin_token() { jq -r .admin "$STATE_DIR/tokens.json"; }

api() { # api METHOD PATH [BODY] -> body; uses admin token
  local args=(-sS -X "$1" "$BASE_URL$2" -H "Authorization: Bearer $(admin_token)" -H 'Content-Type: application/json')
  [[ -n "${3:-}" ]] && args+=(-d "$3")
  curl "${args[@]}"
}

# create_event CAPACITY WINNERS TITLE [DRAW_IN_SECONDS] -> event id
# draw_at defaults to 7 days out. Draws are refused before draw_at, so
# scenarios that draw pass a few seconds and call wait_until_drawable.
create_event() {
  local epoch draw_at
  epoch=$(($(date +%s) + ${4:-604800}))
  draw_at=$(date -u -r "$epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ)
  api POST /events "{\"title\":\"$3\",\"capacity\":$1,\"winner_count\":$2,\"draw_at\":\"$draw_at\"}" |
    jq -er '.data.id'
}

# wait_until_drawable EVENT_ID: sleep until the event's draw_at has passed (DB clock).
wait_until_drawable() {
  until [[ "$(psql_q "SELECT draw_at <= now() FROM events WHERE id = $1")" == "t" ]]; do sleep 0.5; done
}

close_event() { api PUT "/events/$1/close" | jq -e '.data.status == "closed"' >/dev/null; }

# wait_for_ports: Docker Desktop forwards 127.0.0.1:$LT_PORT into its VM, and
# the forwarder opens a new VM-side connection to the container for every
# client connection. The VM's ephemeral range is only 55000-65535 and closed
# connections sit in TIME_WAIT for 60 s, so back-to-back runs that open
# thousands of connections exhaust it ("connect: cannot assign requested
# address" in Docker's VM log; clients see EOF / empty reply). Before every
# k6 run, wait until the VM's TIME_WAIT count is low. Read-only: it only
# reads /proc/net/tcp from a throwaway container on the VM's network.
wait_for_ports() {
  local tw start=$SECONDS
  while :; do
    tw=$(docker run --rm --net host alpine:3.20 sh -c \
      'cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk "\$4==\"06\"" | wc -l' 2>/dev/null || echo 0)
    ((tw < 2000)) && break
    sleep 5
  done
  echo "$(date -u +%H:%M:%S) ${1#"$LT_DIR"/} time_wait=$tw waited_s=$((SECONDS - start))" >>"$COOLDOWN_LOG"
}

# run_k6 OUTNAME SCRIPT [k6 args...]: raw summary JSON + console text go to results/.
run_k6() {
  local name="$1" script="$2"; shift 2
  mkdir -p "$RESULTS_DIR"
  wait_for_ports "$RESULTS_DIR/$name"
  k6 run --quiet --no-color --log-format=raw \
    -e BASE_URL="$BASE_URL" -e TOKENS="$STATE_DIR/tokens.json" \
    -e SUMMARY_OUT="$RESULTS_DIR/$name.json" \
    "$@" "$LT_DIR/k6/$script" >"$RESULTS_DIR/$name.txt" 2>&1 ||
    log "k6 exited non-zero for $name (thresholds or errors); see $RESULTS_DIR/$name.txt"
}
