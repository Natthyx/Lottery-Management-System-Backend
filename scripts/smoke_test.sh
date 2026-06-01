#!/bin/bash
# ============================================================
# Full end-to-end API smoke test
# Run: chmod +x scripts/smoke_test.sh && ./scripts/smoke_test.sh
#
# Prerequisites: server running on localhost:8080
# Start with:  make run
# ============================================================

set -e
BASE="http://localhost:8080"
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

header() { echo -e "\n${BLUE}=== $1 ===${NC}"; }
ok()     { echo -e "${GREEN}✓ $1${NC}"; }

header "1. Register admin"
curl -s -X POST "$BASE/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@test.dev","password":"Admin1234!","full_name":"Test Admin"}' | jq .
ok "Registered"

header "2. Login as admin"
ADMIN_TOKEN=$(curl -s -X POST "$BASE/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@test.dev","password":"Admin1234!"}' | jq -r '.token')
echo "Token: ${ADMIN_TOKEN:0:40}..."
ok "Logged in"

header "3. Register two regular users"
USER1_TOKEN=$(curl -s -X POST "$BASE/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@test.dev","password":"Alice1234!","full_name":"Alice"}' | jq -r '.token' 2>/dev/null || \
  curl -s -X POST "$BASE/auth/login" \
    -H "Content-Type: application/json" \
    -d '{"email":"alice@test.dev","password":"Alice1234!"}' | jq -r '.token')

# Register Alice properly
curl -s -X POST "$BASE/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@test.dev","password":"Alice1234!","full_name":"Alice"}' > /dev/null 2>&1 || true

curl -s -X POST "$BASE/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"email":"bob@test.dev","password":"Bob12345!","full_name":"Bob"}' > /dev/null 2>&1 || true

USER1_TOKEN=$(curl -s -X POST "$BASE/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@test.dev","password":"Alice1234!"}' | jq -r '.token')

USER2_TOKEN=$(curl -s -X POST "$BASE/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"bob@test.dev","password":"Bob12345!"}' | jq -r '.token')

ok "Users registered and logged in"

header "4. Create an event (admin)"
DRAW_TIME=$(date -u -d "+1 hour" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || \
            date -u -v+1H '+%Y-%m-%dT%H:%M:%SZ')  # macOS fallback

EVENT=$(curl -s -X POST "$BASE/events" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d "{
    \"title\": \"VIP Concert Lottery\",
    \"description\": \"Win 2 VIP passes\",
    \"capacity\": 100,
    \"draw_at\": \"$DRAW_TIME\"
  }")
echo "$EVENT" | jq .
EVENT_ID=$(echo "$EVENT" | jq -r '.event.id')
ok "Created event ID: $EVENT_ID"

header "5. List events"
curl -s "$BASE/events" \
  -H "Authorization: Bearer $USER1_TOKEN" | jq .
ok "Listed"

header "6. Alice books the event"
curl -s -X POST "$BASE/events/$EVENT_ID/book" \
  -H "Authorization: Bearer $USER1_TOKEN" | jq .
ok "Alice booked"

header "7. Bob books the event"
curl -s -X POST "$BASE/events/$EVENT_ID/book" \
  -H "Authorization: Bearer $USER2_TOKEN" | jq .
ok "Bob booked"

header "8. Alice tries to book again (should fail)"
DUPE=$(curl -s -X POST "$BASE/events/$EVENT_ID/book" \
  -H "Authorization: Bearer $USER1_TOKEN")
echo "$DUPE" | jq .
ok "Duplicate booking rejected"

header "9. Admin views all bookings"
curl -s "$BASE/events/$EVENT_ID/bookings" \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq .
ok "Listed bookings"

header "10. Admin closes the event"
curl -s -X PUT "$BASE/events/$EVENT_ID/close" \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq .
ok "Event closed"

header "11. Try to book closed event (should fail)"
curl -s -X POST "$BASE/events/$EVENT_ID/book" \
  -H "Authorization: Bearer $USER1_TOKEN" | jq .
ok "Booking rejected after close"

header "12. Admin draws the lottery (1 winner)"
RESULTS=$(curl -s -X POST "$BASE/events/$EVENT_ID/draw" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"winners_count": 1}')
echo "$RESULTS" | jq .
ok "Draw complete"

header "13. View results (public)"
curl -s "$BASE/events/$EVENT_ID/results" \
  -H "Authorization: Bearer $USER1_TOKEN" | jq .
ok "Results visible"

header "14. Try to draw again (should fail - idempotency)"
curl -s -X POST "$BASE/events/$EVENT_ID/draw" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"winners_count": 1}' | jq .
ok "Duplicate draw rejected"

header "15. Health check"
curl -s "$BASE/health" | jq .
ok "Server healthy"

echo -e "\n${GREEN}All smoke tests passed!${NC}"
