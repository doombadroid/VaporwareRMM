#!/bin/bash
# vaporRMM Docker Setup & Test Script
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.full.yml}"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log() { echo -e "${GREEN}[vaporRMM]${NC} $1"; }
warn() { echo -e "${YELLOW}[vaporRMM]${NC} $1"; }
error() { echo -e "${RED}[vaporRMM]${NC} $1"; }

cd "$SCRIPT_DIR"

log "========================================"
log "  vaporRMM Docker Setup"
log "========================================"

# Codex #3: generate cryptographically random ADMIN_PASSWORD,
# JWT_SECRET, and SECRETS_ENCRYPTION_KEY into .env before bringing
# the stack up. Idempotent: any field still set to __GENERATE_ME__
# (the sentinel value in .env.example) gets replaced; fields already
# customised are left alone. Operators rotating credentials on an
# existing install can `sed -i 's|.*=.*|FOO=__GENERATE_ME__|' .env`
# the relevant lines and re-run this script to regenerate.
#
# The admin password is printed to stdout once. CreateDefaultAdmin
# hashes it on first server start and the plaintext is unrecoverable
# afterwards. Operators MUST record it from the script output; we do
# not write it to a file the operator might forget about.

# Pick a random-bytes generator that exists on the host. base64 -w0
# is GNU-only; tr is portable; openssl is the preferred path.
gen_secret() {
  local nbytes="${1:-32}"
  if command -v openssl &> /dev/null; then
    openssl rand -base64 "$nbytes" | tr -d '\n'
  else
    dd if=/dev/urandom bs="$nbytes" count=1 2>/dev/null | base64 | tr -d '=+/\n'
  fi
}

if [ ! -f .env ]; then
  if [ -f .env.example ]; then
    log "Creating .env from example..."
    cp .env.example .env
  else
    error ".env.example not found; cannot bootstrap .env"
    exit 1
  fi
fi

SENTINEL="__GENERATE_ME__"
# Map of (var name, byte count) — 48 bytes for JWT_SECRET so the
# base64 output comfortably exceeds the 32-char minimum the server
# enforces; 32 bytes for the rest.
declare -a SECRETS=(
  "ADMIN_PASSWORD:32"
  "JWT_SECRET:48"
  "SECRETS_ENCRYPTION_KEY:32"
)
ADMIN_PW_PRINTED=""
for entry in "${SECRETS[@]}"; do
  name="${entry%%:*}"
  nbytes="${entry##*:}"
  current=$(grep -E "^${name}=" .env | head -1 | cut -d= -f2-)
  if [ -z "$current" ] || [ "$current" = "$SENTINEL" ]; then
    value=$(gen_secret "$nbytes")
    # sed delimiter is | because base64 output contains / and +; the
    # generator strips =+/ in the dd fallback but openssl output can
    # contain them and we keep them for entropy.
    if grep -qE "^${name}=" .env; then
      sed -i "s|^${name}=.*|${name}=${value}|" .env
    else
      printf '%s=%s\n' "$name" "$value" >> .env
    fi
    warn "Generated random $name in .env"
    if [ "$name" = "ADMIN_PASSWORD" ]; then
      ADMIN_PW_PRINTED="$value"
    fi
  else
    log "$name already set in .env; leaving unchanged"
  fi
done

if [ -n "$ADMIN_PW_PRINTED" ]; then
  echo ""
  log "================================================================"
  log "  ADMIN CREDENTIALS — RECORD NOW (one-time output)"
  log "================================================================"
  log "  Email:    admin@vaporrmm.local"
  log "  Password: $ADMIN_PW_PRINTED"
  log "================================================================"
  echo ""
fi

# Source .env for the test script
if [ -f .env ]; then
  export $(grep -v '^#' .env | grep '=' | xargs 2>/dev/null || true)
fi

# Initialize submodules (Sunshine, Moonlight Web, Moonlight Qt)
cd "$SCRIPT_DIR/.."
if [ ! -f "third_party/sunshine/CMakeLists.txt" ]; then
  log "Initializing third-party submodules..."
  git submodule update --init --recursive --depth 1
fi
cd "$SCRIPT_DIR"

# Check Docker
if ! command -v docker &> /dev/null; then
  error "Docker is not installed. Please install Docker first."
  error "  Ubuntu/Debian:  https://docs.docker.com/engine/install/ubuntu/"
  error "  Alpine:         apk add docker docker-cli-compose"
  error "  Fedora:         dnf install docker-ce docker-compose-plugin"
  exit 1
fi

# Check docker compose
if docker compose version &> /dev/null; then
  COMPOSE="docker compose"
elif docker-compose --version &> /dev/null; then
  COMPOSE="docker-compose"
else
  error "Docker Compose not found. Please install it:"
  error "  https://docs.docker.com/compose/install/"
  exit 1
fi

# Stop any existing containers
if $COMPOSE -f "$COMPOSE_FILE" ps | grep -q "vaporrmm"; then
  warn "Existing vaporRMM containers found. Stopping..."
  $COMPOSE -f "$COMPOSE_FILE" down
fi

# Build third-party components (security-hardened)
log "Building security-hardened third-party components..."
if [ -f "../scripts/build-third-party.sh" ]; then
  ../scripts/build-third-party.sh || warn "Third-party build had issues, continuing with upstream images..."
fi

# Build and start
log "Building vaporRMM Docker images..."
$COMPOSE -f "$COMPOSE_FILE" build --no-cache server dashboard

log "Starting services..."
$COMPOSE -f "$COMPOSE_FILE" up -d

# Wait for server
log "Waiting for server to start..."
for i in {1..60}; do
  if curl -s http://localhost:8080/health > /dev/null 2>&1; then
    log "Server is up!"
    break
  fi
  if [ "$i" -eq 60 ]; then
    error "Server failed to start within 60 seconds"
    error "Check logs: $COMPOSE -f $COMPOSE_FILE logs -f server"
    exit 1
  fi
  sleep 1
done

# Get IP
IP=$(hostname -I | awk '{print $1}' 2>/dev/null || ip route get 1 2>/dev/null | awk '{print $7; exit}' || echo "localhost")

log ""
log "========================================"
log "  vaporRMM is running in Docker!"
log "========================================"
log "Dashboard: http://$IP:3000"
log "API:       http://$IP:8080"
log "Health:    http://$IP:8080/health"
log ""
log "Default login:"
log "  Email:    admin@vaporrmm.local"
log "  Password: (from .env ADMIN_PASSWORD)"
log ""
log "Services:"
$COMPOSE -f "$COMPOSE_FILE" ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}"
log ""

# Run test suite if available
if [ -f test.sh ]; then
  log "Running test suite..."
  sleep 5  # Give agents time to register
  chmod +x test.sh
  ./test.sh || true
fi

log ""
log "Commands:"
log "  View logs:     $COMPOSE -f $COMPOSE_FILE logs -f"
log "  Stop:          $COMPOSE -f $COMPOSE_FILE down"
log "  Restart:       $COMPOSE -f $COMPOSE_FILE restart"
log "  Agent logs:    $COMPOSE -f $COMPOSE_FILE logs -f agent-ubuntu"
log "  Server shell:  $COMPOSE -f $COMPOSE_FILE exec server bash"
log ""
log "Install agent on remote machines:"
log "  curl -fsSL http://$IP:8080/api/branding/agent-install?format=script | sudo bash -s -- --server http://$IP:8080"
log ""
log "Tailscale (optional):"
log "  1. Get auth key: https://login.tailscale.com/admin/settings/keys"
log "  2. Add to .env:  TAILSCALE_AUTH_KEY=tskey-auth-..."
log "  3. Restart:      $COMPOSE -f $COMPOSE_FILE up -d"
log ""
