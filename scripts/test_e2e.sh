#!/usr/bin/env bash
# ============================================================
# LOTTERY SYSTEM — END-TO-END TEST SCRIPT
# Usage: bash scripts/test_e2e.sh
# ============================================================

set -euo pipefail

BASE_URL="http://localhost:8080"
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS=0
FAIL=0

log()  { echo -e "\n${BLUE}[TEST]${NC} $1"; }
ok()   { echo -e "${GREEN}[PASS]${NC} $1"; PASS=$((PASS+1)); }
fail() { echo -e "${RED}[FAIL]${NC} $1 — response: $2"; FAIL=$((FAIL+1)); }
info() { echo -e "${YELLOW}[INFO]${NC} $1"; }

request() {
  local METHOD="$1"
  local ENDPOINT="$2"
  local BODY="${3:-}"
  local TOKEN="${4:-}"
  local ARGS=(-s -X "$METHOD" "${BASE_URL}${ENDPOINT}" -H "Content-Type: application/json")
  [ -n "$TOKEN" ] && ARGS+=(-H "Authorization: Bearer $TOKEN")
  [ -n "$BODY" ]  && ARGS+=(-d "$BODY")
  curl "${ARGS[@]}"
}

extract() {
  echo "$1" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d$2)" 2>/dev/null || echo ""
}

pretty() { echo "$1" | python3 -m json.tool 2>/dev/null || echo "$1"; }

is_success() { echo "$1" | python3 -c "import sys,json; d=json.load(sys.stdin); exit(0 if d.get('success') else 1)" 2>/dev/null; }
is_failure()  { echo "$1" | python3 -c "import sys,json; d=json.load(sys.stdin); exit(0 if not d.get('success') else 1)" 2>/dev/null; }

echo ""
echo "=================================================="
echo "  LOTTERY SYSTEM — END-TO-END TESTS"
echo "=================================================="

# ── 1. Health ─────────────────────────────────────────────────────────────────
log "1. Health check"
RESP=$(request GET /health)
pretty "$RESP"
if echo "$RESP" | grep -q '"ok"'; then
  ok "Server is healthy"
else
  fail "Server not responding" "$RESP"; exit 1
fi

# ── 2. Register admin ─────────────────────────────────────────────────────────
log "2. Register admin user"
ADMIN_EMAIL="admin_$(date +%s)@lottery.com"
RESP=$(request POST /auth/register "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"adminpass123\",\"full_name\":\"System Admin\"}")
pretty "$RESP"
ADMIN_ID=$(extract "$RESP" "['data']['id']")
if is_success "$RESP" && [ -n "$ADMIN_ID" ]; then
  ok "Admin registered (id: $ADMIN_ID)"
else
  fail "Admin registration" "$RESP"
fi

# ── 3. Register user ──────────────────────────────────────────────────────────
log "3. Register regular user"
USER_EMAIL="user_$(date +%s)@lottery.com"
RESP=$(request POST /auth/register "{\"email\":\"$USER_EMAIL\",\"password\":\"userpass123\",\"full_name\":\"Alice Wonderland\"}")
pretty "$RESP"
USER_ID=$(extract "$RESP" "['data']['id']")
if is_success "$RESP" && [ -n "$USER_ID" ]; then
  ok "User registered (id: $USER_ID)"
else
  fail "User registration" "$RESP"
fi

# ── 4. Promote admin ──────────────────────────────────────────────────────────
log "4. Promoting admin in database"
docker exec lottery_postgres psql -U lottery -d lottery_db \
  -c "UPDATE users SET role = 'admin' WHERE email = '$ADMIN_EMAIL';" 2>/dev/null
ok "Admin promoted to role=admin"

# ── 5. Login admin ────────────────────────────────────────────────────────────
log "5. Login as admin"
RESP=$(request POST /auth/login "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"adminpass123\"}")
pretty "$RESP"
ADMIN_TOKEN=$(extract "$RESP" "['data']['token']")
if is_success "$RESP" && [ -n "$ADMIN_TOKEN" ]; then
  ok "Admin token obtained (role: admin)"
else
  fail "Admin login" "$RESP"; exit 1
fi

# ── 6. Login user ─────────────────────────────────────────────────────────────
log "6. Login as regular user"
RESP=$(request POST /auth/login "{\"email\":\"$USER_EMAIL\",\"password\":\"userpass123\"}")
USER_TOKEN=$(extract "$RESP" "['data']['token']")
if is_success "$RESP" && [ -n "$USER_TOKEN" ]; then
  ok "User token obtained"
else
  fail "User login" "$RESP"; exit 1
fi

# ── 7. Create event ───────────────────────────────────────────────────────────
log "7. Create lottery event (admin only)"
DRAW_AT=$(python3 -c "from datetime import datetime,timedelta,timezone; print((datetime.now(timezone.utc)+timedelta(hours=1)).strftime('%Y-%m-%dT%H:%M:%SZ'))")
RESP=$(request POST /events \
  "{\"title\":\"Bloomberg Internship Lottery\",\"description\":\"Win a spot\",\"capacity\":500,\"draw_at\":\"$DRAW_AT\",\"winner_count\":3}" \
  "$ADMIN_TOKEN")
pretty "$RESP"
EVENT_ID=$(extract "$RESP" "['data']['id']")
if is_success "$RESP" && [ -n "$EVENT_ID" ]; then
  ok "Event created (id: $EVENT_ID, winner_count: 3)"
else
  fail "Event creation" "$RESP"
fi

# ── 8. List events ────────────────────────────────────────────────────────────
log "8. List events (public)"
RESP=$(request GET /events)
pretty "$RESP"
COUNT=$(extract "$RESP" "['meta']['total']")
if is_success "$RESP"; then
  ok "Events listed (total: $COUNT)"
else
  fail "List events" "$RESP"
fi

# ── 9. Book event ─────────────────────────────────────────────────────────────
log "9. Book event as regular user"
RESP=$(request POST "/events/${EVENT_ID}/book" "" "$USER_TOKEN")
pretty "$RESP"
if is_success "$RESP"; then
  ok "Booking confirmed — user is now in lottery pool"
else
  fail "Booking" "$RESP"
fi

# ── 10. Duplicate booking ─────────────────────────────────────────────────────
log "10. Duplicate booking (must be rejected)"
RESP=$(request POST "/events/${EVENT_ID}/book" "" "$USER_TOKEN")
pretty "$RESP"
if is_failure "$RESP"; then
  ERROR=$(extract "$RESP" "['error']")
  ok "Duplicate correctly rejected: \"$ERROR\""
else
  fail "Duplicate should have been rejected" "$RESP"
fi

# ── 11. My bookings ───────────────────────────────────────────────────────────
log "11. Fetch my bookings"
RESP=$(request GET /me/bookings "" "$USER_TOKEN")
pretty "$RESP"
if is_success "$RESP"; then
  ok "My bookings fetched"
else
  fail "My bookings" "$RESP"
fi

# ── 12. Run lottery draw ──────────────────────────────────────────────────────
log "12. Run lottery draw (admin only)"
RESP=$(request POST "/events/${EVENT_ID}/draw" "" "$ADMIN_TOKEN")
pretty "$RESP"
if is_success "$RESP"; then
  WINNERS=$(extract "$RESP" "['data']['winners']")
  ENTROPY=$(extract "$RESP" "['data']['winners'][0]['entropy_source']")
  TOTAL=$(extract "$RESP" "['data']['total_entrants']")
  ok "Draw completed — $TOTAL entrant(s), entropy: $ENTROPY"
else
  fail "Lottery draw" "$RESP"
fi

# ── 13. Fetch results ─────────────────────────────────────────────────────────
log "13. Fetch draw results (public endpoint)"
RESP=$(request GET "/events/${EVENT_ID}/results")
pretty "$RESP"
if is_success "$RESP"; then
  WINNER_ID=$(extract "$RESP" "['data']['winners'][0]['winner_user_id']")
  ok "Results verified — winner user_id: $WINNER_ID"
else
  fail "Fetch results" "$RESP"
fi

# ── 14. Try drawing again (must fail — idempotency) ───────────────────────────
log "14. Re-run draw on same event (must be rejected)"
RESP=$(request POST "/events/${EVENT_ID}/draw" "" "$ADMIN_TOKEN")
pretty "$RESP"
if is_failure "$RESP"; then
  ok "Re-draw correctly rejected (idempotency works)"
else
  fail "Re-draw should have been rejected" "$RESP"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "=================================================="
if [ "$FAIL" -eq 0 ]; then
  echo -e "  ${GREEN}ALL $PASS TESTS PASSED ✓${NC}"
else
  echo -e "  ${GREEN}$PASS passed${NC} | ${RED}$FAIL failed${NC}"
fi
echo "=================================================="
echo ""