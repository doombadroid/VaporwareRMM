package handlers

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
)

func RegisterBrandingRoutes(app *fiber.App, api fiber.Router) {
	// Public branding config
	app.Get("/api/branding/", func(c *fiber.Ctx) error {
		var brandingConfig models.BrandingConfig
		err := db.DB.QueryRow(`SELECT app_name, icon_url, company_name, primary_color FROM branding WHERE id = 'default'`).Scan(
			&brandingConfig.AppName, &brandingConfig.IconURL, &brandingConfig.CompanyName, &brandingConfig.PrimaryColor,
		)
		if err != nil {
			return c.JSON(models.BrandingConfig{AppName: "vaporRMM", IconURL: "", CompanyName: "Vaporware RMM", PrimaryColor: "#3b82f6"})
		}
		return c.JSON(brandingConfig)
	})

	// Public install links (no auth required — used for client onboarding)
	app.Get("/api/branding/install-links", func(c *fiber.Ctx) error {
		return serveInstallLinks(c)
	})

	// Public agent install script (no auth required)
	app.Get("/api/branding/agent-install", func(c *fiber.Ctx) error {
		return serveAgentInstall(c)
	})

	// Serve pre-built agent binary
	app.Get("/download/agent-:os-:arch", func(c *fiber.Ctx) error {
		// For now, serve the linux-amd64 binary regardless of requested platform
		// In production, you'd have separate binaries per platform
		c.Set("Content-Type", "application/octet-stream")
		c.Set("Content-Disposition", "attachment; filename=vaporrmm-agent")
		return c.SendFile("/tmp/vaporrmm-agent")
	})

	// Also register on v1 for consistency
	api.Get("/branding/install-links", func(c *fiber.Ctx) error {
		return serveInstallLinks(c)
	})
	api.Get("/branding/agent-install", func(c *fiber.Ctx) error {
		return serveAgentInstall(c)
	})

	// Protected branding update
	branding := api.Group("/branding")
	branding.Put("/", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req models.BrandingConfig
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		_, err := db.DB.Exec(`UPDATE branding SET app_name = ?, icon_url = ?, company_name = ?, primary_color = ? WHERE id = 'default'`,
			req.AppName, req.IconURL, req.CompanyName, req.PrimaryColor)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update branding"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLog(userID, "branding.update", "branding", "default", "updated branding", c.IP())
		return c.JSON(fiber.Map{"message": "Branding updated successfully", "branding": req})
	})
}

func getBrandingAndServerURL(c *fiber.Ctx) (models.BrandingConfig, string) {
	var bc models.BrandingConfig
	db.DB.QueryRow(`SELECT app_name, icon_url, company_name, primary_color FROM branding WHERE id = 'default'`).Scan(
		&bc.AppName, &bc.IconURL, &bc.CompanyName, &bc.PrimaryColor,
	)
	if bc.AppName == "" {
		bc = models.BrandingConfig{AppName: "vaporRMM", IconURL: "", CompanyName: "Vaporware RMM", PrimaryColor: "#3b82f6"}
	}

	host := c.Hostname()
	// Only append port if Hostname doesn't already contain one and Port is valid server port
	if port := c.Port(); port != "" && !strings.Contains(host, ":") {
		host = host + ":" + port
	}
	serverURL := fmt.Sprintf("%s://%s", c.Protocol(), host)
	return bc, serverURL
}

func serveInstallLinks(c *fiber.Ctx) error {
	bc, serverURL := getBrandingAndServerURL(c)

	return c.JSON(fiber.Map{
		"app_name":      bc.AppName,
		"company_name":  bc.CompanyName,
		"icon_url":      bc.IconURL,
		"primary_color": bc.PrimaryColor,
		"server_url":    serverURL,
		"install_options": []fiber.Map{
			{
				"name":     "Linux (curl)",
				"command":  fmt.Sprintf("curl -fsSL %s/api/branding/agent-install?format=script | sudo bash -s -- --server %s", serverURL, serverURL),
				"platform": "linux",
			},
			{
				"name":     "Windows (PowerShell)",
				"command":  fmt.Sprintf("Invoke-WebRequest -Uri '%s/api/branding/agent-install?format=script' -UseBasicParsing | Invoke-Expression", serverURL),
				"platform": "windows",
			},
			{
				"name":     "macOS (curl)",
				"command":  fmt.Sprintf("curl -fsSL %s/api/branding/agent-install?format=script | sudo bash -s -- --server %s", serverURL, serverURL),
				"platform": "macos",
			},
			{
				"name":     "Download Script",
				"url":      fmt.Sprintf("%s/api/branding/agent-install?format=script", serverURL),
				"platform": "all",
			},
		},
	})
}

func serveAgentInstall(c *fiber.Ctx) error {
	bc, serverURL := getBrandingAndServerURL(c)

	format := c.Query("format", "script")
	if format == "json" {
		return c.JSON(fiber.Map{
			"app_name":        bc.AppName,
			"company_name":    bc.CompanyName,
			"icon_url":        bc.IconURL,
			"server_url":      serverURL,
			"install_command": fmt.Sprintf("curl -fsSL %s/api/branding/agent-install?format=script | bash", serverURL),
		})
	}

	script := generateInstallScript(bc.AppName, bc.CompanyName, bc.IconURL, serverURL)
	c.Set("Content-Type", "text/x-shellscript")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-agent-install.sh", strings.ToLower(bc.AppName)))
	return c.SendString(script)
}

func generateInstallScript(appName, companyName, iconURL, serverURL string) string {
	return fmt.Sprintf(`#!/bin/bash
# %s Agent Installation Script
# Generated by %s
# Server: %s
#
# One-line install:
#   curl -fsSL %s/api/branding/agent-install?format=script | sudo bash -s -- --server %s
#
# With Sunshine + Tailscale:
#   curl -fsSL %s/api/branding/agent-install?format=script | sudo bash -s -- --server %s --install-sunshine --install-tailscale --tailscale-auth-key YOUR_KEY
#
set -euo pipefail

APP_NAME="%s"
SERVER_URL="%s"
ICON_URL="%s"
INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="${APP_NAME,,}-agent"
CONFIG_DIR="/etc/${APP_NAME,,}"

# Optional extras
INSTALL_SUNSHINE=""
INSTALL_TAILSCALE=""
TAILSCALE_AUTH_KEY=""

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --server)
      SERVER_URL="$2"
      shift 2
      ;;
    --install-sunshine)
      INSTALL_SUNSHINE="1"
      shift
      ;;
    --install-tailscale)
      INSTALL_TAILSCALE="1"
      shift
      ;;
    --tailscale-auth-key)
      TAILSCALE_AUTH_KEY="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

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

# Helper: try to download binary from server
download_binary() {
  echo "Downloading pre-built agent binary..."
  if command -v curl &> /dev/null; then
    curl -fsSL "$DOWNLOAD_URL" -o "$BINARY_PATH" 2>/dev/null && return 0
  fi
  if command -v wget &> /dev/null; then
    wget -q "$DOWNLOAD_URL" -O "$BINARY_PATH" 2>/dev/null && return 0
  fi
  return 1
}

# Helper: try local dev build (only for localhost/127.0.0.1)
build_local() {
  if ! command -v go &> /dev/null; then
    return 1
  fi
  # Only attempt local build if server is localhost (dev environment)
  case "$SERVER_URL" in
    *localhost*|*127.0.0.1*)
      # Search common local source paths
      for SRC in "$HOME/Documents/vaporRMM/packages/agent" "$HOME/vaporRMM/packages/agent" "$HOME/workspace/vaporRMM/packages/agent" "$(pwd)/../vaporRMM/packages/agent"; do
        if [ -d "$SRC" ] && [ -f "$SRC/main.go" ]; then
          echo "Building agent from local source: $SRC"
          cd "$SRC"
          go build -o "$BINARY_PATH" .
          return 0
        fi
      done
      ;;
  esac
  return 1
}

# Attempt install: download first, then local build, then fail
if download_binary; then
  chmod +x "$BINARY_PATH"
  echo "Agent binary downloaded successfully."
elif build_local; then
  echo "Agent built from local source."
else
  echo ""
  echo "ERROR: Could not install agent automatically."
  echo ""
  echo "Options:"
  echo "  1. Ensure the server has a pre-built binary at: $DOWNLOAD_URL"
  echo "  2. Install Go and place the vaporRMM source tree at ~/Documents/vaporRMM/"
  echo "  3. Build the agent manually and copy to: $BINARY_PATH"
  echo ""
  exit 1
fi

# ============================
# Install Sunshine (optional)
# ============================
if [ -n "$INSTALL_SUNSHINE" ]; then
  echo ""
  echo "--- Installing Sunshine ---"
  if command -v sunshine &> /dev/null || [ -f /usr/bin/sunshine ] || [ -f /opt/sunshine/Sunshine.AppImage ]; then
    echo "Sunshine already installed, skipping."
  else
    # Try distribution package managers first
    if command -v apt-get &> /dev/null; then
      echo "Attempting install via apt..."
      apt-get update -qq && apt-get install -y -qq sunshine 2>/dev/null || {
        echo "apt install failed, trying manual download..."
        SUNSHINE_DEB="https://github.com/LizardByte/Sunshine/releases/download/v2025.628.4510/sunshine-ubuntu-24.04-amd64.deb"
        curl -fsSL "$SUNSHINE_DEB" -o /tmp/sunshine.deb 2>/dev/null && dpkg -i /tmp/sunshine.deb 2>/dev/null || apt-get install -f -y 2>/dev/null || true
      }
    elif command -v dnf &> /dev/null; then
      echo "Attempting install via dnf..."
      dnf install -y sunshine 2>/dev/null || true
    elif command -v pacman &> /dev/null; then
      echo "Attempting install via pacman..."
      pacman -S --noconfirm sunshine 2>/dev/null || true
    elif command -v apk &> /dev/null; then
      echo "Attempting install via apk..."
      apk add sunshine 2>/dev/null || true
    fi

    # Fallback: check if installed now
    if command -v sunshine &> /dev/null || [ -f /usr/bin/sunshine ]; then
      echo "Sunshine installed successfully."
    else
      echo "WARNING: Could not install Sunshine automatically. Install manually from https://github.com/LizardByte/Sunshine"
    fi
  fi
fi

# ============================
# Install Tailscale (optional)
# ============================
if [ -n "$INSTALL_TAILSCALE" ]; then
  echo ""
  echo "--- Installing Tailscale ---"
  if command -v tailscale &> /dev/null; then
    echo "Tailscale already installed, skipping."
  else
    echo "Running Tailscale install script..."
    curl -fsSL https://tailscale.com/install.sh | sh 2>/dev/null || {
      echo "WARNING: Tailscale install script failed. Install manually from https://tailscale.com/download"
    }
  fi

  # Auto-connect if auth key provided
  if [ -n "$TAILSCALE_AUTH_KEY" ] && command -v tailscale &> /dev/null; then
    echo "Connecting Tailscale with provided auth key..."
    tailscale up --authkey "$TAILSCALE_AUTH_KEY" --accept-routes 2>/dev/null || {
      echo "WARNING: Tailscale up failed. You may need to run: sudo tailscale up --authkey $TAILSCALE_AUTH_KEY"
    }
  fi
fi

# Create config directory
mkdir -p "$CONFIG_DIR"
echo "$SERVER_URL" > "${CONFIG_DIR}/server_url"
if [ -n "$ICON_URL" ]; then
  echo "$ICON_URL" > "${CONFIG_DIR}/icon_url"
fi
# Write moonlight-web URL if server exposes it
if [ -n "$SERVER_URL" ]; then
  echo "${SERVER_URL}" > "${CONFIG_DIR}/server_url"
fi

# Detect init system and install service accordingly
INIT_SYSTEM=""

if command -v systemctl &> /dev/null && [ -d /etc/systemd/system ]; then
  INIT_SYSTEM="systemd"
  echo "Installing systemd service..."
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
    echo "WARNING: Could not start systemd service. Starting manually..."
    nohup "$BINARY_PATH" --server-url="$SERVER_URL" > /dev/null 2>&1 &
    INIT_SYSTEM=""
  }
elif command -v rc-update &> /dev/null && [ -d /etc/init.d ]; then
  INIT_SYSTEM="openrc"
  echo "Installing OpenRC service..."
  cat > "/etc/init.d/${SERVICE_NAME}" <<'EOF'
#!/sbin/openrc-run

description="VaporRMM Agent"
command="/usr/local/bin/vaporrmm-agent"
command_args="--server-url=http://localhost:8080"
command_background=true
pidfile="/run/${RC_SVCNAME}.pid"

depend() {
  need net
}
EOF
  sed -i "s|command_args=.*|command_args=\"--server-url=${SERVER_URL}\"|" "/etc/init.d/${SERVICE_NAME}"
  chmod +x "/etc/init.d/${SERVICE_NAME}"
  rc-update add "$SERVICE_NAME" default 2>/dev/null || true
  rc-service "$SERVICE_NAME" restart 2>/dev/null || rc-service "$SERVICE_NAME" start 2>/dev/null || {
    echo "WARNING: Could not start OpenRC service. Starting manually..."
    nohup "$BINARY_PATH" --server-url="$SERVER_URL" > /dev/null 2>&1 &
    INIT_SYSTEM=""
  }
else
  echo "No systemd or OpenRC detected. Starting agent directly..."
  nohup "$BINARY_PATH" --server-url="$SERVER_URL" > /dev/null 2>&1 &
fi

echo ""
echo "========================================"
echo "  $APP_NAME agent is running!"
echo "========================================"
echo "Binary : $BINARY_PATH"
echo "Config : ${CONFIG_DIR}/"
echo "Server : $SERVER_URL"
if [ -n "$INSTALL_SUNSHINE" ]; then
  echo "Sunshine: installed (if available)"
fi
if [ -n "$INSTALL_TAILSCALE" ]; then
  echo "Tailscale: installed (if available)"
fi
echo ""
if [ "$INIT_SYSTEM" = "systemd" ]; then
  echo "Check status:  systemctl status $SERVICE_NAME"
  echo "View logs:     journalctl -u $SERVICE_NAME -f"
elif [ "$INIT_SYSTEM" = "openrc" ]; then
  echo "Check status:  rc-service $SERVICE_NAME status"
  echo "View logs:     tail -f /var/log/vaporrmm-agent.log  (or check syslog)"
else
  echo "Check status:  ps aux | grep vaporrmm-agent"
  echo "View logs:     No centralized logging (running via nohup)"
fi
`, companyName, companyName, serverURL, serverURL, serverURL, serverURL, serverURL, appName, serverURL, iconURL)
}
