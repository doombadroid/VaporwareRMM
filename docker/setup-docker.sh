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

# Check if .env exists, create from example if not
if [ ! -f .env ]; then
  if [ -f .env.example ]; then
    log "Creating .env from example..."
    cp .env.example .env
    # Generate a random JWT_SECRET
    if command -v openssl &> /dev/null; then
      SECRET=$(openssl rand -base64 32)
    else
      SECRET=$(dd if=/dev/urandom bs=32 count=1 2>/dev/null | base64 | tr -d '=+/')
    fi
    sed -i "s|JWT_SECRET=.*|JWT_SECRET=$SECRET|" .env
    warn "Generated random JWT_SECRET in .env"
    warn "Please review and customize .env before running again if needed"
  fi
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
