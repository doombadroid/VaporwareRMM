#!/bin/bash
set -e

# Start tailscaled if TAILSCALE_AUTH_KEY is provided
if [ -n "$TAILSCALE_AUTH_KEY" ]; then
  echo "[vaporRMM] Starting tailscaled..."
  tailscaled --tun=userspace-networking --socks5-server=localhost:1055 --outbound-http-proxy-listen=localhost:1055 &
  sleep 2
  echo "[vaporRMM] Connecting to Tailscale..."
  tailscale up --authkey="$TAILSCALE_AUTH_KEY" --hostname="vaporrmm-server" --accept-routes
  echo "[vaporRMM] Tailscale connected. IP: $(tailscale ip -4 2>/dev/null || echo 'unknown')"
fi

# Run the main command (server binary)
exec "$@"
