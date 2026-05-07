#!/bin/bash
# vaporRMM Remote Agent Install Script
# This script is served by the server at /api/branding/agent-install
# It downloads and installs the agent pointing at the correct server

set -euo pipefail

APP_NAME="vaporRMM"
SERVER_URL="${1:-}"
INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="${APP_NAME,,}-agent"
CONFIG_DIR="/etc/${APP_NAME,,}"

# Parse --server argument
while [[ $# -gt 0 ]]; do
  case $1 in
    --server)
      SERVER_URL="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [ -z "$SERVER_URL" ]; then
  echo "ERROR: --server URL is required"
  echo "Usage: $0 --server http://your-server:8080"
  exit 1
fi

echo "========================================"
echo "  Installing $APP_NAME agent"
echo "  Server: $SERVER_URL"
echo "========================================"

# Detect OS/ARCH
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case $ARCH in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) ARCH="amd64" ;;
esac

BINARY_PATH="${INSTALL_DIR}/${APP_NAME,,}-agent"
DOWNLOAD_URL="${SERVER_URL}/download/agent-${OS}-${ARCH}"

# Download binary
download_binary() {
  echo "Downloading agent binary..."
  if command -v curl &> /dev/null; then
    curl -fsSL "$DOWNLOAD_URL" -o "$BINARY_PATH" 2>/dev/null && return 0
  fi
  if command -v wget &> /dev/null; then
    wget -q "$DOWNLOAD_URL" -O "$BINARY_PATH" 2>/dev/null && return 0
  fi
  return 1
}

# Fallback: build from local source (dev only)
build_local() {
  if ! command -v go &> /dev/null; then
    return 1
  fi
  case "$SERVER_URL" in
    *localhost*|*127.0.0.1*)
      for SRC in "$HOME/Documents/vaporRMM/packages/agent" "$HOME/vaporRMM/packages/agent" "$HOME/workspace/vaporRMM/packages/agent"; do
        if [ -d "$SRC" ] && [ -f "$SRC/main.go" ]; then
          echo "Building from local source: $SRC"
          cd "$SRC"
          go build -o "$BINARY_PATH" .
          return 0
        fi
      done
      ;;
  esac
  return 1
}

if download_binary; then
  chmod +x "$BINARY_PATH"
  echo "Binary downloaded."
elif build_local; then
  echo "Built from local source."
else
  echo "ERROR: Could not install agent."
  echo "Options:"
  echo "  1. Ensure the server has a binary at: $DOWNLOAD_URL"
  echo "  2. Install Go and clone the vaporRMM source tree"
  echo "  3. Build manually: go build -o $BINARY_PATH"
  exit 1
fi

# Config
mkdir -p "$CONFIG_DIR"
echo "$SERVER_URL" > "${CONFIG_DIR}/server_url"

# Service installation
INIT_SYSTEM=""

if command -v systemctl &> /dev/null && [ -d /etc/systemd/system ]; then
  INIT_SYSTEM="systemd"
  cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=$APP_NAME Agent
After=network.target

[Service]
Type=simple
ExecStart=${BINARY_PATH} --server-url=${SERVER_URL}
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME" 2>/dev/null || true
  systemctl restart "$SERVICE_NAME" 2>/dev/null || systemctl start "$SERVICE_NAME" 2>/dev/null || {
    INIT_SYSTEM=""
    nohup "$BINARY_PATH" --server-url="$SERVER_URL" > /dev/null 2>&1 &
  }

elif command -v rc-update &> /dev/null && [ -d /etc/init.d ]; then
  INIT_SYSTEM="openrc"
  cat > "/etc/init.d/${SERVICE_NAME}" <<'EOF'
#!/sbin/openrc-run

description="vaporRMM Agent"
command="/usr/local/bin/vaporrmm-agent"
command_background=true
pidfile="/run/${RC_SVCNAME}.pid"

depend() {
  need net
}
EOF
  sed -i "s|command=.*|command=\"$BINARY_PATH\"|" "/etc/init.d/${SERVICE_NAME}"
  sed -i "/depend/a command_args=\"--server-url=$SERVER_URL\"" "/etc/init.d/${SERVICE_NAME}"
  chmod +x "/etc/init.d/${SERVICE_NAME}"
  rc-update add "$SERVICE_NAME" default 2>/dev/null || true
  rc-service "$SERVICE_NAME" restart 2>/dev/null || rc-service "$SERVICE_NAME" start 2>/dev/null || {
    INIT_SYSTEM=""
    nohup "$BINARY_PATH" --server-url="$SERVER_URL" > /dev/null 2>&1 &
  }

else
  echo "No init system detected. Starting manually..."
  nohup "$BINARY_PATH" --server-url="$SERVER_URL" > /dev/null 2>&1 &
fi

echo ""
echo "========================================"
echo "  $APP_NAME agent installed!"
echo "========================================"
echo "Server : $SERVER_URL"
echo "Binary : $BINARY_PATH"
echo "Config : ${CONFIG_DIR}/"
echo ""
if [ "$INIT_SYSTEM" = "systemd" ]; then
  echo "Status : systemctl status $SERVICE_NAME"
  echo "Logs   : journalctl -u $SERVICE_NAME -f"
elif [ "$INIT_SYSTEM" = "openrc" ]; then
  echo "Status : rc-service $SERVICE_NAME status"
  echo "Logs   : Check syslog or: tail -f /var/log/messages"
else
  echo "Status : ps aux | grep vaporrmm-agent"
fi
