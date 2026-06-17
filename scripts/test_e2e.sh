#!/usr/bin/env bash
# ============================================================
# Lottery System — full E2E test (no docker exec required)
# Drives the public HTTP API only. Relies on a bootstrap admin
# created by setting BOOTSTRAP_ADMIN_EMAIL / BOOTSTRAP_ADMIN_PASSWORD
# in the server's environment before startup.
# ============================================================

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_EMAIL="${BOOTSTRAP_ADMIN_EMAIL:-admin@lottery.local}"
ADMIN_PASSWORD="${BOOTSTRAP_ADMIN_PASSWORD:-Admin1234!ChangeMe}"

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS=0; FAIL=0

log()  { echo -e "\n${BLUE}[TEST]${NC} $1"; }
ok()   { echo -e "${GREEN}[PASS]${NC} $1"; PASS=$((PASS+1)); }
fail() { echo -e "${RED}[FAIL]${NC} $1\n${YELLOW}response:${NC} $2"; FAIL=$((FAIL+1)); }

call() {
  local method="$1" path="$2" token="${3:-}" body="${4:-}"
  local args=(-s -X "$method" "${BASE_URL}${path}" -H "Content-Type: application/json")
  [[ -n "$token" ]] && args+=(-H "Authorization: Bearer $token")
  [[ -n "$body"  ]] && args+=(-d "$body")
  curl "${args[@]}"
}

j() { jq -r "$2" <<<"$1"; }

# ── 1. Health ───────────────────────────────────────────────
log "1. /health"
RESP=$(call GET /health)
[[ "$(j "$RESP" '.status')" == "ok" ]] && ok "alive" || { fail "alive" "$RESP"; exit 1; }

# ── 2. Readiness ────────────────────────────────────────────
log "2. /ready"
RESP=$(call GET /ready)
[[ "$(j "$RESP" '.status')" == "ok" ]] && ok "ready" || fail "ready" "$RESP"

# ── 3. Admin login ──────────────────────────────────────────
log "3. login admin"
RESP=$(call POST /auth/login "" "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}")
ADMIN_TOKEN=$(j "$RESP" '.data.token')
if [[ -n "$ADMIN_TOKEN" && "$ADMIN_TOKEN" != "null" ]]; then ok "admin token"
else fail "admin login (is BOOTSTRAP_ADMIN_EMAIL/PASSWORD set in the server env?)" "$RESP"; exit 1; fi

# ── 4. Register user ────────────────────────────────────────
TS=$(date +%s)
USER_EMAIL="user_${TS}@lottery.test"
log "4. register user $USER_EMAIL"
RESP=$(call POST /auth/register "" "{\"email\":\"$USER_EMAIL\",\"password\":\"userpass123\",\"full_name\":\"Alice $TS\"}")
USER_ID=$(j "$RESP" '.data.id')
[[ -n "$USER_ID" && "$USER_ID" != "null" ]] && ok "user $USER_ID" || fail "register" "$RESP"

# ── 5. Login user ───────────────────────────────────────────
log "5. login user"
RESP=$(call POST /auth/login "" "{\"email\":\"$USER_EMAIL\",\"password\":\"userpass123\"}")
USER_TOKEN=$(j "$RESP" '.data.token')
[[ -n "$USER_TOKEN" && "$USER_TOKEN" != "null" ]] && ok "user token" || { fail "user login" "$RESP"; exit 1; }

# ── 6. /auth/me ─────────────────────────────────────────────
log "6. /auth/me"
RESP=$(call GET /auth/me "$USER_TOKEN")
[[ "$(j "$RESP" '.data.email')" == "$USER_EMAIL" ]] && ok "me ok" || fail "me" "$RESP"

# ── 7. Create event ─────────────────────────────────────────
log "7. create event"
DRAW_AT=$(python3 -c "from datetime import datetime,timedelta,timezone; print((datetime.now(timezone.utc)+timedelta(hours=1)).strftime('%Y-%m-%dT%H:%M:%SZ'))")
RESP=$(call POST /events "$ADMIN_TOKEN" "{\"title\":\"E2E Event $TS\",\"description\":\"win a spot\",\"capacity\":5,\"draw_at\":\"$DRAW_AT\",\"winner_count\":2}")
EVENT_ID=$(j "$RESP" '.data.id')
[[ -n "$EVENT_ID" && "$EVENT_ID" != "null" ]] && ok "event $EVENT_ID" || { fail "create event" "$RESP"; exit 1; }

# ── 8. Non-admin cannot create event ────────────────────────
log "8. non-admin POST /events must be 403"
RESP=$(call POST /events "$USER_TOKEN" "{\"title\":\"Nope\",\"capacity\":1,\"draw_at\":\"$DRAW_AT\"}")
[[ "$(j "$RESP" '.success')" == "false" ]] && ok "forbidden enforced" || fail "RBAC" "$RESP"

# ── 9. List events ──────────────────────────────────────────
log "9. list events"
RESP=$(call GET /events)
[[ "$(j "$RESP" '.success')" == "true" ]] && ok "listed" || fail "list events" "$RESP"

# ── 10. Book ────────────────────────────────────────────────
log "10. book event"
RESP=$(call POST "/events/${EVENT_ID}/book" "$USER_TOKEN")
[[ "$(j "$RESP" '.success')" == "true" ]] && ok "booked" || fail "book" "$RESP"

# ── 11. Duplicate booking ───────────────────────────────────
log "11. duplicate booking rejected"
RESP=$(call POST "/events/${EVENT_ID}/book" "$USER_TOKEN")
[[ "$(j "$RESP" '.success')" == "false" ]] && ok "rejected" || fail "duplicate" "$RESP"

# ── 12. Drawing before close is rejected ────────────────────
log "12. premature draw rejected"
RESP=$(call POST "/events/${EVENT_ID}/draw" "$ADMIN_TOKEN")
[[ "$(j "$RESP" '.success')" == "false" ]] && ok "premature draw rejected" || fail "premature draw" "$RESP"

# ── 13. Close event ─────────────────────────────────────────
log "13. close event"
RESP=$(call PUT "/events/${EVENT_ID}/close" "$ADMIN_TOKEN")
[[ "$(j "$RESP" '.data.status')" == "closed" ]] && ok "closed" || fail "close" "$RESP"

# ── 14. Booking after close rejected ────────────────────────
log "14. booking after close rejected"
RESP=$(call POST "/events/${EVENT_ID}/book" "$USER_TOKEN")
[[ "$(j "$RESP" '.success')" == "false" ]] && ok "rejected" || fail "post-close booking" "$RESP"

# ── 15. Draw ────────────────────────────────────────────────
log "15. draw"
RESP=$(call POST "/events/${EVENT_ID}/draw" "$ADMIN_TOKEN")
ENTROPY=$(j "$RESP" '.data.winners[0].entropy_source')
TOTAL=$(j "$RESP" '.data.total_entrants')
WINNERS=$(j "$RESP" '.data.winners | length')
WAITLIST=$(j "$RESP" '.data.waitlist | length')
if [[ "$(j "$RESP" '.success')" == "true" ]]; then
  ok "drew $WINNERS winner(s), $WAITLIST waitlist, total $TOTAL, entropy=$ENTROPY"
else
  fail "draw" "$RESP"
fi

# ── 16. Idempotent re-draw rejected ─────────────────────────
log "16. re-draw rejected"
RESP=$(call POST "/events/${EVENT_ID}/draw" "$ADMIN_TOKEN")
[[ "$(j "$RESP" '.success')" == "false" ]] && ok "rejected" || fail "re-draw" "$RESP"

# ── 17. Results endpoint ────────────────────────────────────
log "17. results"
RESP=$(call GET "/events/${EVENT_ID}/results")
RW=$(j "$RESP" '.data.winners | length')
RWL=$(j "$RESP" '.data.waitlist | length')
[[ "$RW" == "$WINNERS" && "$RWL" == "$WAITLIST" ]] && ok "winners/waitlist match draw" || fail "results split" "$RESP"

# ── 18. Promote user via admin endpoint ─────────────────────
log "18. promote user $USER_ID to admin"
RESP=$(call POST "/admin/users/${USER_ID}/promote" "$ADMIN_TOKEN")
[[ "$(j "$RESP" '.data.role')" == "admin" ]] && ok "promoted" || fail "promote" "$RESP"

# ── Summary ─────────────────────────────────────────────────
echo
echo "=================================================="
if [[ "$FAIL" -eq 0 ]]; then
  echo -e "  ${GREEN}ALL $PASS TESTS PASSED${NC}"
else
  echo -e "  ${GREEN}$PASS passed${NC} | ${RED}$FAIL failed${NC}"
  exit 1
fi
echo "=================================================="
