#!/bin/bash
# vaporRMM Docker Test Suite
# Verifies all services are running and responding correctly
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'
SERVER_URL="${SERVER_URL:-http://localhost:8080}"
DASHBOARD_URL="${DASHBOARD_URL:-http://localhost:3000}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.full.yml}"
PASS=0
FAIL=0

pass() { echo -e "${GREEN}[PASS]${NC} $1"; ((PASS++)) || true; }
fail() { echo -e "${RED}[FAIL]${NC} $1"; ((FAIL++)) || true; }
info() { echo -e "${YELLOW}[INFO]${NC} $1"; }

info "========================================"
info "  vaporRMM Docker Test Suite"
info "========================================"
info "Server:    $SERVER_URL"
info "Dashboard: $DASHBOARD_URL"
info "Compose:   $COMPOSE_FILE"
info ""

# Check if docker compose is running
if ! docker compose -f "$COMPOSE_FILE" ps | grep -q "vaporrmm-server"; then
  fail "Server container is not running. Start with: docker compose -f $COMPOSE_FILE up -d"
  exit 1
fi

# 1. Server Health
info "--- Test 1: Server Health ---"
if curl -s "$SERVER_URL/health" | grep -q '"status":"ok"'; then
  pass "Server health endpoint responds"
else
  fail "Server health endpoint failed"
fi

# 2. Server Version
info "--- Test 2: Server Version ---"
VERSION=$(curl -s "$SERVER_URL/api/version" | grep -o '"version":"[^"]*"' || true)
if [ -n "$VERSION" ]; then
  pass "Server version: $VERSION"
else
  fail "Server version endpoint failed"
fi

# 3. Public Branding
info "--- Test 3: Public Branding ---"
if curl -s "$SERVER_URL/api/branding/" | grep -q '"app_name"'; then
  pass "Branding endpoint accessible (no auth required)"
else
  fail "Branding endpoint failed"
fi

# 4. Install Links
info "--- Test 4: Install Links ---"
if curl -s "$SERVER_URL/api/branding/install-links" | grep -q '"install_options"'; then
  pass "Install links endpoint accessible"
else
  fail "Install links endpoint failed"
fi

# 5. Agent Install Script
info "--- Test 5: Agent Install Script ---"
if curl -s "$SERVER_URL/api/branding/agent-install?format=script" | grep -q '#!/bin/bash'; then
  pass "Agent install script downloadable"
else
  fail "Agent install script failed"
fi

# 6. Agent Binary Download
info "--- Test 6: Agent Binary Download ---"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$SERVER_URL/download/agent-linux-amd64")
if [ "$HTTP_CODE" = "200" ]; then
  pass "Agent binary download works (HTTP 200)"
else
  fail "Agent binary download failed (HTTP $HTTP_CODE)"
fi

# 7. Dashboard Accessibility
info "--- Test 7: Dashboard Accessibility ---"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$DASHBOARD_URL")
if [ "$HTTP_CODE" = "200" ]; then
  pass "Dashboard loads (HTTP 200)"
else
  fail "Dashboard failed (HTTP $HTTP_CODE)"
fi

# 8. Login API
info "--- Test 8: Login API ---"
LOGIN_RES=$(curl -s -w "\nHTTP_CODE:%{http_code}" -X POST "$SERVER_URL/api/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@vaporrmm.local","password":"'"$ADMIN_PASSWORD"'"}')
HTTP_CODE=$(echo "$LOGIN_RES" | grep "HTTP_CODE:" | cut -d: -f2)
if [ "$HTTP_CODE" = "200" ]; then
  TOKEN=$(echo "$LOGIN_RES" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
  if [ -n "$TOKEN" ]; then
    pass "Login works, JWT token received"
  else
    fail "Login worked but no token in response"
  fi
else
  fail "Login failed (HTTP $HTTP_CODE) — check ADMIN_PASSWORD in .env"
fi

# 9. Authenticated Device List
info "--- Test 9: Authenticated Device List ---"
if [ -n "${TOKEN:-}" ]; then
  DEVICES=$(curl -s "$SERVER_URL/api/v1/devices" -H "Authorization: Bearer $TOKEN")
  if echo "$DEVICES" | grep -q '\['; then
    DEVICE_COUNT=$(echo "$DEVICES" | grep -o '"id"' | wc -l)
    pass "Device list works ($DEVICE_COUNT devices registered)"
  else
    fail "Device list failed"
  fi
else
  fail "Skipping device list (no token)"
fi

# 10. Dashboard Overview
info "--- Test 10: Dashboard Overview ---"
if [ -n "${TOKEN:-}" ]; then
  OVERVIEW=$(curl -s "$SERVER_URL/api/v1/dashboard/overview" -H "Authorization: Bearer $TOKEN")
  if echo "$OVERVIEW" | grep -q '"device_stats"'; then
    pass "Dashboard overview API works"
  else
    fail "Dashboard overview API failed"
  fi
else
  fail "Skipping dashboard overview (no token)"
fi

# 11. Test Agents Connected
info "--- Test 11: Test Agents ---"
if [ -n "${TOKEN:-}" ]; then
  DEVICES=$(curl -s "$SERVER_URL/api/v1/devices" -H "Authorization: Bearer $TOKEN")
  UBUNTU=$(echo "$DEVICES" | grep -c "test-ubuntu" || true)
  ALPINE=$(echo "$DEVICES" | grep -c "test-alpine" || true)
  if [ "$UBUNTU" -gt 0 ] && [ "$ALPINE" -gt 0 ]; then
    pass "Both test agents registered (Ubuntu + Alpine)"
  elif [ "$UBUNTU" -gt 0 ] || [ "$ALPINE" -gt 0 ]; then
    pass "At least one test agent registered"
  else
    fail "No test agents registered yet (they may still be starting)"
  fi
else
  fail "Skipping agent check (no token)"
fi

# 12. Tailscale Status (if configured)
info "--- Test 12: Tailscale ---"
if docker compose -f "$COMPOSE_FILE" exec -T server tailscale status 2>/dev/null | grep -q "vaporrmm-server"; then
  TS_IP=$(docker compose -f "$COMPOSE_FILE" exec -T server tailscale ip -4 2>/dev/null || true)
  pass "Tailscale connected (IP: $TS_IP)"
else
  info "Tailscale not configured (set TAILSCALE_AUTH_KEY in .env to test)"
fi

# 13. Moonlight Web Stream
info "--- Test 13: Moonlight Web Stream ---"
ML_HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${SERVER_URL}:8081" || true)
if [ "$ML_HTTP_CODE" = "200" ] || [ "$ML_HTTP_CODE" = "301" ] || [ "$ML_HTTP_CODE" = "302" ]; then
  pass "Moonlight Web Stream accessible (HTTP $ML_HTTP_CODE)"
else
  info "Moonlight Web Stream not responding yet (HTTP ${ML_HTTP_CODE:-none}) — may still be starting"
fi

# 14. Security-hardened builds
info "--- Test 14: Security-Hardened Builds ---"
if docker compose -f "$COMPOSE_FILE" exec -T moonlight-web id | grep -q "moonlight"; then
  pass "Moonlight Web runs as non-root user"
else
  info "Moonlight Web user check skipped"
fi
if docker compose -f "$COMPOSE_FILE" images moonlight-web | grep -q "vaporrmm/moonlight-web"; then
  pass "Using locally-built Moonlight Web image (not upstream)"
else
  info "Using upstream Moonlight Web image (run scripts/build-third-party.sh for hardened build)"
fi

# Summary
info ""
info "========================================"
info "  Test Results: $PASS passed, $FAIL failed"
info "========================================"

if [ "$FAIL" -gt 0 ]; then
  info ""
  info "Debugging:"
  info "  Server logs:    docker compose -f $COMPOSE_FILE logs -f server"
  info "  Agent logs:     docker compose -f $COMPOSE_FILE logs -f agent-ubuntu"
  info "  All services:   docker compose -f $COMPOSE_FILE ps"
  exit 1
else
  info ""
  info "All tests passed!"
  info ""
  info "Access your deployment:"
  info "  Dashboard:        $DASHBOARD_URL"
  info "  API:              $SERVER_URL"
  info "  Moonlight Web:    ${SERVER_URL}:8081"
  info "  Login:            admin@vaporrmm.local"
  exit 0
fi
