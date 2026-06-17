#!/usr/bin/env bash
# ============================================================
# Lottery System — smoke test
# Prereqs:
#   - server running on $BASE_URL (default http://localhost:8080)
#   - BOOTSTRAP_ADMIN_EMAIL / BOOTSTRAP_ADMIN_PASSWORD set in the
#     server's env so the admin account exists at boot
# Run:  bash scripts/smoke_test.sh
# ============================================================

set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
ADMIN_EMAIL="${BOOTSTRAP_ADMIN_EMAIL:-admin@lottery.local}"
ADMIN_PASSWORD="${BOOTSTRAP_ADMIN_PASSWORD:-Admin1234!ChangeMe}"
TS=$(date +%s)
USER_EMAIL="alice_${TS}@lottery.test"
USER_PASSWORD="AlicePass1234"

GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

step()  { echo -e "\n${BLUE}=== $1 ===${NC}"; }
ok()    { echo -e "${GREEN}✓ $1${NC}"; }
err()   { echo -e "${RED}✗ $1${NC}"; exit 1; }

jget() { jq -r "$2" <<<"$1"; }

call() {
  local method="$1" path="$2" token="${3:-}" body="${4:-}"
  local args=(-s -X "$method" "${BASE}${path}" -H "Content-Type: application/json")
  [[ -n "$token" ]] && args+=(-H "Authorization: Bearer $token")
  [[ -n "$body"  ]] && args+=(-d "$body")
  curl "${args[@]}"
}

step "1. Health"
RESP=$(call GET /health)
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.status')" == "ok" ]] || err "health failed"
ok "server is alive"

step "2. Readiness"
RESP=$(call GET /ready)
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.status')" == "ok" ]] || err "readiness failed"
ok "datastores reachable"

step "3. Login as bootstrapped admin"
RESP=$(call POST /auth/login "" "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}")
ADMIN_TOKEN=$(jget "$RESP" '.data.token')
[[ -n "$ADMIN_TOKEN" && "$ADMIN_TOKEN" != "null" ]] || err "admin login failed: $RESP"
ok "admin token obtained"

step "4. Register a regular user"
RESP=$(call POST /auth/register "" "{\"email\":\"$USER_EMAIL\",\"password\":\"$USER_PASSWORD\",\"full_name\":\"Alice $TS\"}")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.success')" == "true" ]] || err "register failed"
ok "user registered"

step "5. Log the user in"
RESP=$(call POST /auth/login "" "{\"email\":\"$USER_EMAIL\",\"password\":\"$USER_PASSWORD\"}")
USER_TOKEN=$(jget "$RESP" '.data.token')
[[ -n "$USER_TOKEN" && "$USER_TOKEN" != "null" ]] || err "user login failed: $RESP"
ok "user token obtained"

step "6. /auth/me"
RESP=$(call GET /auth/me "$USER_TOKEN")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.data.email')" == "$USER_EMAIL" ]] || err "me mismatch"
ok "me works"

step "7. Create an event (admin)"
DRAW_AT=$(python3 -c "from datetime import datetime,timedelta,timezone; print((datetime.now(timezone.utc)+timedelta(hours=1)).strftime('%Y-%m-%dT%H:%M:%SZ'))")
RESP=$(call POST /events "$ADMIN_TOKEN" "{\"title\":\"Smoke Event $TS\",\"description\":\"For testing\",\"capacity\":10,\"draw_at\":\"$DRAW_AT\",\"winner_count\":1}")
echo "$RESP" | jq .
EVENT_ID=$(jget "$RESP" '.data.id')
[[ -n "$EVENT_ID" && "$EVENT_ID" != "null" ]] || err "create event failed"
ok "event created (id: $EVENT_ID)"

step "8. List events"
RESP=$(call GET /events)
echo "$RESP" | jq '{count: (.data|length), meta: .meta}'

step "9. User books the event"
RESP=$(call POST "/events/${EVENT_ID}/book" "$USER_TOKEN")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.success')" == "true" ]] || err "booking failed"
ok "booked"

step "10. Duplicate booking is rejected"
RESP=$(call POST "/events/${EVENT_ID}/book" "$USER_TOKEN")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.success')" == "false" ]] || err "duplicate booking should fail"
ok "duplicate rejected"

step "11. Admin lists bookings for the event"
RESP=$(call GET "/events/${EVENT_ID}/bookings" "$ADMIN_TOKEN")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.success')" == "true" ]] || err "list bookings failed"

step "12. Drawing before closing is rejected"
RESP=$(call POST "/events/${EVENT_ID}/draw" "$ADMIN_TOKEN")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.success')" == "false" ]] || err "premature draw should fail"
ok "premature draw correctly rejected"

step "13. Close the event"
RESP=$(call PUT "/events/${EVENT_ID}/close" "$ADMIN_TOKEN")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.data.status')" == "closed" ]] || err "close failed"
ok "event closed"

step "14. Booking after close is rejected"
RESP=$(call POST "/events/${EVENT_ID}/book" "$USER_TOKEN")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.success')" == "false" ]] || err "post-close booking should fail"
ok "post-close booking rejected"

step "15. Draw the lottery"
RESP=$(call POST "/events/${EVENT_ID}/draw" "$ADMIN_TOKEN")
echo "$RESP" | jq .
WINNER_COUNT=$(jget "$RESP" '.data.winners | length')
[[ "$WINNER_COUNT" -ge 1 ]] || err "no winners produced"
ok "draw complete with $WINNER_COUNT winner(s)"

step "16. Re-drawing is rejected"
RESP=$(call POST "/events/${EVENT_ID}/draw" "$ADMIN_TOKEN")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.success')" == "false" ]] || err "re-draw should fail"
ok "re-draw rejected (idempotency holds)"

step "17. Public results"
RESP=$(call GET "/events/${EVENT_ID}/results")
echo "$RESP" | jq .
[[ "$(jget "$RESP" '.data.winners | length')" == "$WINNER_COUNT" ]] || err "results winner count mismatch"
ok "results visible"

echo -e "\n${GREEN}All smoke tests passed!${NC}"
