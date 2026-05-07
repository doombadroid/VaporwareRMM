#!/bin/bash
# vaporRMM Server Setup Script
# Run on your VM as root
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="/opt/vaporrmm"
DATA_DIR="$INSTALL_DIR/data"
CONFIG_DIR="/etc/vaporrmm"
USER="vaporrmm"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[vaporRMM]${NC} $1"; }
warn() { echo -e "${YELLOW}[vaporRMM]${NC} $1"; }
error() { echo -e "${RED}[vaporRMM]${NC} $1"; }

# Detect OS
if [ -f /etc/os-release ]; then
  . /etc/os-release
  OS=$ID
else
  error "Cannot detect OS"
  exit 1
fi

log "Starting vaporRMM server setup on $OS..."

# Check if running as root
if [ "$EUID" -ne 0 ]; then
  error "Please run as root (sudo ./setup-server.sh)"
  exit 1
fi

# Install dependencies
log "Installing dependencies..."
case $OS in
  ubuntu|debian)
    apt-get update -qq
    apt-get install -y -qq curl wget sqlite3 nodejs npm golang-go git systemd || true
    ;;
  alpine)
    apk add --no-cache curl wget sqlite nodejs npm go git openrc bash sudo
    ;;
  fedora|centos|rhel)
    dnf install -y curl wget sqlite nodejs npm golang git systemd
    ;;
  *)
    warn "Unknown OS: $OS. Please install Go, Node.js, and SQLite manually."
    ;;
esac

# Create user and directories
log "Creating user and directories..."
if ! id "$USER" &>/dev/null; then
  useradd -r -s /bin/false -d "$INSTALL_DIR" "$USER" 2>/dev/null || adduser -S -D -h "$INSTALL_DIR" "$USER" 2>/dev/null || true
fi

mkdir -p "$INSTALL_DIR"/{server,dashboard,data,backups} "$CONFIG_DIR"
chown -R "$USER:$USER" "$INSTALL_DIR"

# Copy or build server binary
if [ -f "$SCRIPT_DIR/../packages/server/main.go" ]; then
  log "Building server from source..."
  cd "$SCRIPT_DIR/../packages/server"
  go build -o "$INSTALL_DIR/server/vaporrmm-server" ./main.go
elif [ -f "$SCRIPT_DIR/vaporrmm-server" ]; then
  log "Copying pre-built server binary..."
  cp "$SCRIPT_DIR/vaporrmm-server" "$INSTALL_DIR/server/"
  chmod +x "$INSTALL_DIR/server/vaporrmm-server"
else
  error "No server binary or source found. Please build it first:"
  error "  cd packages/server && go build -o vaporrmm-server ."
  exit 1
fi

# Copy dashboard
if [ -d "$SCRIPT_DIR/../apps/dashboard" ]; then
  log "Copying dashboard files..."
  cp -r "$SCRIPT_DIR/../apps/dashboard/"* "$INSTALL_DIR/dashboard/"
  cd "$INSTALL_DIR/dashboard"
  log "Installing dashboard dependencies..."
  npm ci --production 2>/dev/null || npm install --production 2>/dev/null || npm install
  log "Building dashboard..."
  npm run build
else
  error "Dashboard not found. Please ensure apps/dashboard exists."
  exit 1
fi

# Install environment files
if [ ! -f "$CONFIG_DIR/server.env" ]; then
  log "Creating server environment file..."
  cp "$SCRIPT_DIR/server.env.example" "$CONFIG_DIR/server.env"
  JWT_SECRET=$(openssl rand -base64 32 2>/dev/null || dd if=/dev/urandom bs=32 count=1 2>/dev/null | base64)
  sed -i "s|JWT_SECRET=.*|JWT_SECRET=$JWT_SECRET|" "$CONFIG_DIR/server.env"
  warn "Generated JWT secret. Please review $CONFIG_DIR/server.env"
fi

if [ ! -f "$CONFIG_DIR/dashboard.env" ]; then
  log "Creating dashboard environment file..."
  cp "$SCRIPT_DIR/dashboard.env.example" "$CONFIG_DIR/dashboard.env"
fi

# Install systemd services
if [ -d /etc/systemd/system ]; then
  log "Installing systemd services..."
  cp "$SCRIPT_DIR/systemd/vaporrmm-server.service" /etc/systemd/system/
  cp "$SCRIPT_DIR/systemd/vaporrmm-dashboard.service" /etc/systemd/system/
  systemctl daemon-reload
  systemctl enable vaporrmm-server vaporrmm-dashboard
  systemctl start vaporrmm-server
  sleep 2
  systemctl start vaporrmm-dashboard
  log "Services started. Check status with:"
  log "  systemctl status vaporrmm-server"
  log "  systemctl status vaporrmm-dashboard"
elif command -v rc-update &>/dev/null; then
  log "Installing OpenRC services..."
  cat > /etc/init.d/vaporrmm-server <<'EOF'
#!/sbin/openrc-run

description="vaporRMM Server"
command="/opt/vaporrmm/server/vaporrmm-server"
command_background=true
pidfile="/run/${RC_SVCNAME}.pid"
env_file="/etc/vaporrmm/server.env"

depend() {
  need net
}

start_pre() {
  export $(grep -v '^#' "$env_file" | xargs)
}
EOF
  chmod +x /etc/init.d/vaporrmm-server
  rc-update add vaporrmm-server default
  rc-service vaporrmm-server start
  warn "OpenRC service installed. Dashboard must be started manually or via another method."
else
  warn "No systemd or OpenRC detected. Starting server manually..."
  export $(grep -v '^#' "$CONFIG_DIR/server.env" | xargs)
  nohup "$INSTALL_DIR/server/vaporrmm-server" > /var/log/vaporrmm-server.log 2>&1 &
  cd "$INSTALL_DIR/dashboard"
  nohup npm run start -- -p 3000 > /var/log/vaporrmm-dashboard.log 2>&1 &
fi

# Get IP for user
IP=$(hostname -I | awk '{print $1}' 2>/dev/null || ip route get 1 2>/dev/null | awk '{print $7; exit}' || echo "YOUR_VM_IP")

log ""
log "========================================"
log "  vaporRMM server deployed!"
log "========================================"
log "API:      http://$IP:8080"
log "Health:   http://$IP:8080/health"
log "Dashboard:http://$IP:3000"
log "Config:   $CONFIG_DIR/server.env"
log "Data:     $DATA_DIR"
log ""
log "Default login:"
log "  Email:    admin@vaporrmm.local"
log "  Password: (from server.env ADMIN_PASSWORD)"
log ""
log "Next steps:"
log "  1. Edit $CONFIG_DIR/server.env with your settings"
log "  2. Restart: systemctl restart vaporrmm-server"
log "  3. Install agent on remote machines:"
log "     curl -fsSL http://$IP:8080/api/branding/agent-install?format=script | sudo bash -s -- --server http://$IP:8080"
log ""
