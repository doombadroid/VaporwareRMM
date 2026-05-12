package handlers

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
)

var (
	// AppName is used as a shell variable + systemd service name + filesystem path.
	// Strict charset prevents template / shell / path injection.
	brandAppNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	// PrimaryColor must be a CSS hex color so the dashboard CSS injection is bounded.
	brandColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}){1,2}$`)

	// agentBinaryPaths is the allowlist of supported (os-arch) →
	// on-disk binary path served by GET /download/agent-:os-:arch.
	// Templating the request params into a path would be a directory-
	// traversal primitive; an allowlist makes the lookup explicit
	// and lets a future Codex review trivially confirm the surface.
	//
	// Adding a new platform requires TWO changes:
	//   1. Add an entry here keyed by "<os>-<arch>".
	//   2. Add a matching build stage in packages/server/Dockerfile
	//      that produces the file at the path used here.
	// Forgetting (2) means the new entry serves a 500/404 to clients.
	agentBinaryPaths = map[string]string{
		"linux-amd64": "/opt/agents/linux-amd64",
	}
)

func RegisterBrandingRoutes(app *fiber.App, api fiber.Router) {
	// Public branding config — picks the tenant from the request's subdomain
	// (resolved by ResolveTenantFromHost) and falls back to MSP default.
	app.Get("/api/branding/", func(c *fiber.Ctx) error {
		hostTenant, _ := c.Locals("host_tenant_id").(string)
		var brandingConfig models.BrandingConfig
		if hostTenant != "" {
			if err := db.DB.QueryRow(
				`SELECT app_name, icon_url, company_name, primary_color FROM branding WHERE id = ?`, hostTenant,
			).Scan(&brandingConfig.AppName, &brandingConfig.IconURL, &brandingConfig.CompanyName, &brandingConfig.PrimaryColor); err == nil && brandingConfig.AppName != "" {
				return c.JSON(brandingConfig)
			}
		}
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

	// Serve pre-built agent binary. The (os, arch) lookup goes through
	// the agentBinaryPaths allowlist (declared above) so a request
	// like /download/agent-..-..-passwd lands on a map miss and 404s.
	// Adding new platforms means adding a map entry AND ensuring the
	// Dockerfile produces the matching binary at that path — see the
	// note on agentBinaryPaths.
	app.Get("/download/agent-:os-:arch", func(c *fiber.Ctx) error {
		key := c.Params("os") + "-" + c.Params("arch")
		path, ok := agentBinaryPaths[key]
		if !ok {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "agent binary not available for this platform",
			})
		}
		c.Set("Content-Type", "application/octet-stream")
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=vaporrmm-agent-%s", key))
		return c.SendFile(path)
	})

	// Also register on v1 for consistency
	api.Get("/branding/install-links", func(c *fiber.Ctx) error {
		return serveInstallLinks(c)
	})
	api.Get("/branding/agent-install", func(c *fiber.Ctx) error {
		return serveAgentInstall(c)
	})

	// Protected branding (per-tenant)
	branding := api.Group("/branding")

	// Authenticated GET returns the caller's tenant branding
	branding.Get("/", func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var bc models.BrandingConfig
		err := db.DB.QueryRow(`SELECT app_name, icon_url, company_name, primary_color FROM branding WHERE id = ?`, tenantID).Scan(
			&bc.AppName, &bc.IconURL, &bc.CompanyName, &bc.PrimaryColor)
		if err != nil {
			// Fallback to MSP default if tenant has no row yet
			err = db.DB.QueryRow(`SELECT app_name, icon_url, company_name, primary_color FROM branding WHERE id = 'default'`).Scan(
				&bc.AppName, &bc.IconURL, &bc.CompanyName, &bc.PrimaryColor)
			if err != nil {
				return c.JSON(models.BrandingConfig{AppName: "vaporRMM", IconURL: "", CompanyName: "Vaporware RMM", PrimaryColor: "#3b82f6"})
			}
		}
		return c.JSON(bc)
	})

	branding.Put("/", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req models.BrandingConfig
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		// AppName becomes a systemd unit name AND a /etc/<app_name> path
		// component AND a downloaded-file name. The restricted charset
		// here is enforcing those positional constraints, NOT shell-
		// injection paranoia (the install script also routes app_name
		// through shellSafeOrFallback as defense-in-depth). Display
		// strings with spaces, punctuation, etc. go in CompanyName.
		if req.AppName != "" && !brandAppNameRe.MatchString(req.AppName) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "app_name must be 1-64 chars, ASCII letters/digits/dash/underscore only"})
		}
		// CompanyName is a display field. The only rejection is line
		// breaks — a one-line display field has no business carrying
		// newlines, and stripping them keeps the install-script
		// comment header valid. Every other character (including &
		// ' " $ ` \ ; |) is accepted because real customer names
		// contain them ("Smith & Jones IT", "T&C IT Systems",
		// "O'Reilly Media"). Safety against shell injection in the
		// generated install script is the responsibility of
		// generateInstallScript, which embeds the value inside a
		// shell comment line — comments do not interpret
		// metacharacters. See threatmodel.md §3: reject at output
		// boundary, not at input.
		if req.CompanyName != "" {
			if len(req.CompanyName) > 128 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "company_name must be 128 chars or fewer"})
			}
			if strings.ContainsAny(req.CompanyName, "\r\n") {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "company_name must not contain line breaks"})
			}
		}
		// PrimaryColor must look like a CSS hex color (#RGB or #RRGGBB).
		if req.PrimaryColor != "" && !brandColorRe.MatchString(req.PrimaryColor) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "primary_color must be a hex color, e.g. #3b82f6"})
		}
		if req.IconURL != "" {
			parsed, err := url.Parse(req.IconURL)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "icon_url must be a valid https:// URL"})
			}
		}
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var upsert string
		if db.DB.Dialect == "postgres" {
			upsert = `INSERT INTO branding (id, app_name, icon_url, company_name, primary_color, tenant_id) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET app_name = EXCLUDED.app_name, icon_url = EXCLUDED.icon_url, company_name = EXCLUDED.company_name, primary_color = EXCLUDED.primary_color`
		} else {
			upsert = `INSERT OR REPLACE INTO branding (id, app_name, icon_url, company_name, primary_color, tenant_id) VALUES (?, ?, ?, ?, ?, ?)`
		}
		_, err := db.DB.Exec(upsert, tenantID, req.AppName, req.IconURL, req.CompanyName, req.PrimaryColor, tenantID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update branding"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "branding.update", "branding", tenantID, "updated branding", c.IP())
		return c.JSON(fiber.Map{"message": "Branding updated successfully", "branding": req})
	})
}

func getBrandingAndServerURL(c *fiber.Ctx) (models.BrandingConfig, string) {
	var bc models.BrandingConfig
	_ = db.DB.QueryRow(`SELECT app_name, icon_url, company_name, primary_color FROM branding WHERE id = 'default'`).Scan(
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

// shellSafeOrFallback returns s if it matches brandAppNameRe, otherwise "vaporrmm".
// Defense in depth: even if a row in the branding table has somehow been
// populated with shell metacharacters, the rendered install script stays safe.
func shellSafeOrFallback(s string) string {
	if brandAppNameRe.MatchString(s) {
		return s
	}
	return "vaporrmm"
}

// scrubForComment strips bytes that could break out of a `#` shell comment
// line. Newlines are the only real escape vector — bash comments run to
// end-of-line and do not interpret metacharacters (no backtick exec, no
// $-substitution, no quote pairing). Earlier passes also stripped ` $ \
// "as defense in depth" but that mangled legitimate display names like
// "Tesla, Inc. ($TSLA)" into "Tesla, Inc. ( TSLA)". The threat-model
// principle is: reject at the output boundary where the character matters,
// not at the input. Here the boundary is the comment line and the only
// boundary-violating character is a newline.
func scrubForComment(s string) string {
	out := strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	if len(out) > 128 {
		out = out[:128]
	}
	return out
}

func generateInstallScript(appName, companyName, iconURL, serverURL string) string {
	appName = shellSafeOrFallback(appName)
	companyName = scrubForComment(companyName)
	// iconURL is validated as https:// at write time; scrub anyway.
	iconURL = strings.NewReplacer("\r", "", "\n", "", "\"", "", "'", "", "`", "", "$", "", "\\", "").Replace(iconURL)
	return fmt.Sprintf(`#!/bin/bash
# %s Agent Installation Script
# Generated by %s
# Server: %s
#
# One-line install:
#   curl -fsSL %s/api/branding/agent-install?format=script | sudo bash -s -- --server %s --tailscale-auth-key tskey-auth-XXXX
#
# Tailscale is installed by default. The agent + Sunshine remote-desktop
# traffic moves over a private tailnet so Sunshine's auth-less listener
# never sits on the public internet. To opt out (e.g., your endpoints
# are already on a private network), pass --no-tailscale. Generate an
# auth key at https://login.tailscale.com/admin/settings/keys.
#
# With Sunshine and an explicit auth key:
#   curl -fsSL %s/api/branding/agent-install?format=script | sudo bash -s -- --server %s --install-sunshine --tailscale-auth-key tskey-auth-XXXX
#
# Opting out of Tailscale (NOT RECOMMENDED for fleets that use Sunshine):
#   curl -fsSL %s/api/branding/agent-install?format=script | sudo bash -s -- --server %s --no-tailscale
#
set -euo pipefail

APP_NAME="%s"
SERVER_URL="%s"
ICON_URL="%s"
INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="${APP_NAME,,}-agent"
CONFIG_DIR="/etc/${APP_NAME,,}"
ENV_FILE="${CONFIG_DIR}/agent.env"

# REGISTRATION_SECRET may be passed in env (preferred) or via --registration-secret
: "${REGISTRATION_SECRET:=}"

# TAILSCALE_AUTH_KEY may be passed in env (preferred for CI / fleet
# provisioning where the key is a secret) or via --tailscale-auth-key.
: "${TAILSCALE_AUTH_KEY:=}"

# Optional extras
INSTALL_SUNSHINE=""
# Tailscale is default-on. --no-tailscale flips this to "" before
# the install path runs. --install-tailscale remains accepted but
# is a no-op (the explicit name was the prior surface; preserve it
# so existing operator scripts don't break).
INSTALL_TAILSCALE="1"

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
      # Already default; flag preserved for backwards compatibility.
      INSTALL_TAILSCALE="1"
      shift
      ;;
    --no-tailscale)
      INSTALL_TAILSCALE=""
      shift
      ;;
    --tailscale-auth-key)
      TAILSCALE_AUTH_KEY="$2"
      shift 2
      ;;
    --registration-secret)
      REGISTRATION_SECRET="$2"
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

# ============================================================
# Install Tailscale (default on; opt out with --no-tailscale)
# ============================================================
# Runs BEFORE the agent install so a missing/invalid auth key
# fails the script before any state is written. Tailscale gives the
# agent + Sunshine traffic a private tailnet so Sunshine's auth-less
# listener never sits on the public internet.
if [ -n "$INSTALL_TAILSCALE" ]; then
  echo ""
  echo "--- Installing Tailscale ---"
  if [ -z "$TAILSCALE_AUTH_KEY" ]; then
    echo ""
    echo "ERROR: Tailscale is enabled by default but no auth key was provided."
    echo "Get one from https://login.tailscale.com/admin/settings/keys and pass it via:"
    echo "  --tailscale-auth-key=tskey-auth-XXXXX"
    echo "or set the TAILSCALE_AUTH_KEY environment variable before running this script."
    echo ""
    echo "To skip Tailscale entirely (NOT RECOMMENDED if you plan to use Sunshine"
    echo "remote desktop — the listener has no built-in auth), rerun with --no-tailscale."
    exit 1
  fi
  if command -v tailscale &> /dev/null; then
    echo "Tailscale already installed; skipping package install."
  else
    echo "Running Tailscale install script..."
    if ! curl -fsSL https://tailscale.com/install.sh | sh; then
      echo ""
      echo "ERROR: Tailscale install script failed."
      echo "Install manually from https://tailscale.com/download then re-run this script with --no-tailscale,"
      echo "or re-run after the manual install completes."
      exit 1
    fi
  fi
  TAILSCALE_HOSTNAME="${APP_NAME,,}-$(hostname)"
  echo "Bringing tailnet up as ${TAILSCALE_HOSTNAME} (auth-key length: ${#TAILSCALE_AUTH_KEY})..."
  if ! tailscale up --authkey="$TAILSCALE_AUTH_KEY" --hostname="$TAILSCALE_HOSTNAME" --accept-routes --accept-dns=false; then
    echo ""
    echo "ERROR: tailscale up failed."
    echo "Check the daemon: sudo tailscale status"
    echo "Common causes: expired/used auth-key, ACL refused the tag, restrictive firewall."
    exit 1
  fi
  if ! tailscale status >/dev/null 2>&1; then
    echo ""
    echo "ERROR: tailscale status exited non-zero after 'tailscale up'."
    echo "The tailnet is not in a usable state. Inspect manually before re-running."
    exit 1
  fi
  echo "Tailscale connected."
fi

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

# Helper: try to download binary from server. Captures the HTTP
# status into DOWNLOAD_STATUS so the error path can surface it.
DOWNLOAD_STATUS=""
download_binary() {
  echo "Downloading pre-built agent binary..."
  if command -v curl &> /dev/null; then
    DOWNLOAD_STATUS=$(curl -fsSL -w "%%{http_code}" -o "$BINARY_PATH" "$DOWNLOAD_URL" 2>/dev/null) && return 0
    return 1
  fi
  if command -v wget &> /dev/null; then
    # wget doesn't have an easy status-code capture. Probe with a HEAD-style
    # request to learn the status, then fall through to the real fetch on 200.
    DOWNLOAD_STATUS=$(wget --server-response --spider "$DOWNLOAD_URL" 2>&1 | awk '/HTTP\// {code=$2} END {print code}')
    if [ "$DOWNLOAD_STATUS" = "200" ]; then
      wget -q "$DOWNLOAD_URL" -O "$BINARY_PATH" 2>/dev/null && return 0
    fi
    return 1
  fi
  DOWNLOAD_STATUS="no_http_client"
  return 1
}

# Attempt install: download from server. No local-build fallback in
# production — the dev-only path that searched ~/Documents/vaporRMM
# was confusing for end users hitting an install failure on a real
# host. Operators who need a non-standard arch should add it to the
# server's allowlist (see agentBinaryPaths in branding.go).
if download_binary; then
  chmod +x "$BINARY_PATH"
  echo "Agent binary downloaded successfully."
else
  echo ""
  echo "ERROR: Could not download agent binary."
  echo "  URL:    $DOWNLOAD_URL"
  echo "  Status: HTTP ${DOWNLOAD_STATUS:-unknown}"
  echo ""
  echo "Likely causes:"
  echo "  - This OS/arch (${OS}/${ARCH}) is not built into the server image."
  echo "    Confirm by listing the server's supported platforms with the"
  echo "    operator; only platforms in agentBinaryPaths are served."
  echo "  - The server is unreachable from this host. Verify connectivity:"
  echo "      curl -fsSL ${SERVER_URL}/health"
  echo "    Expect a JSON \"status\":\"ok\" response."
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

# Tailscale install moved to the top of the script so a missing
# auth key fails before any state is written. See the
# # Install Tailscale section above the OS-detect block.

# Create config directory
mkdir -p "$CONFIG_DIR"
echo "$SERVER_URL" > "${CONFIG_DIR}/server_url"
if [ -n "$ICON_URL" ]; then
  echo "$ICON_URL" > "${CONFIG_DIR}/icon_url"
fi

# Persist a stable VAPOR_AGENT_TOKEN so the agent uses the same bearer across restarts.
# Generated once on install; reused thereafter. File is mode 0600.
if [ ! -f "${CONFIG_DIR}/agent_token" ]; then
  if command -v openssl &> /dev/null; then
    openssl rand -hex 32 > "${CONFIG_DIR}/agent_token"
  elif command -v xxd &> /dev/null; then
    head -c 32 /dev/urandom | xxd -p -c 64 > "${CONFIG_DIR}/agent_token"
  elif command -v od &> /dev/null; then
    # od is part of coreutils — present on every Linux + BSD + macOS we care about.
    head -c 32 /dev/urandom | od -An -vtx1 | tr -d ' \n' > "${CONFIG_DIR}/agent_token"
  else
    # Last resort: base64. Not hex, but cryptographically equivalent.
    head -c 32 /dev/urandom | base64 | tr -d '/+=\n' | head -c 64 > "${CONFIG_DIR}/agent_token"
  fi
  chmod 600 "${CONFIG_DIR}/agent_token"
fi
AGENT_TOKEN="$(cat "${CONFIG_DIR}/agent_token")"

# Write env file consumed by the systemd / OpenRC service.
# REGISTRATION_SECRET only used on first run; kept in the file so re-registration
# after a wipe of the server-side agent_tokens row works without re-running install.
{
  echo "VAPOR_SERVER_URL=${SERVER_URL}"
  echo "VAPOR_AGENT_TOKEN=${AGENT_TOKEN}"
  if [ -n "${REGISTRATION_SECRET}" ]; then
    echo "REGISTRATION_SECRET=${REGISTRATION_SECRET}"
  fi
} > "${ENV_FILE}"
chmod 600 "${ENV_FILE}"

# Detect init system and install service accordingly
INIT_SYSTEM=""

if command -v systemctl &> /dev/null && [ -d /etc/systemd/system ]; then
  INIT_SYSTEM="systemd"
  echo "Installing systemd service..."
  # ExecStart deliberately carries no --server-url flag — the agent
  # reads VAPOR_SERVER_URL from the EnvironmentFile. One source of
  # truth (the env file) avoids the mixed-mode bug where the flag
  # disagreed with the env value.
  cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=$APP_NAME Agent
After=network.target

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=${BINARY_PATH}
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
    echo ""
    echo "ERROR: Service did not start successfully."
    echo "Check the logs:"
    echo "  Linux (OpenRC):  sudo tail -100 /var/log/${SERVICE_NAME}.log"
    echo "  Linux (systemd): sudo journalctl -u ${SERVICE_NAME} -n 100 --no-pager"
    echo ""
    echo "Once you've diagnosed the issue, restart with:"
    echo "  Linux (OpenRC):  sudo rc-service ${SERVICE_NAME} restart"
    echo "  Linux (systemd): sudo systemctl restart ${SERVICE_NAME}"
    exit 1
  }
elif command -v rc-update &> /dev/null && [ -d /etc/init.d ]; then
  INIT_SYSTEM="openrc"
  echo "Installing OpenRC service..."
  # Unquoted heredoc so install-time vars (BINARY_PATH, APP_NAME,
  # ENV_FILE, SERVICE_NAME) expand here. \${RC_SVCNAME} is escaped
  # to defer expansion until OpenRC reads the service file at
  # runtime — that variable is owned by OpenRC, not this script.
  #
  # start_pre sources the env file inside set -a / set +a so every
  # KEY=value line becomes an exported variable for the daemon
  # process. This replaces the prior /etc/conf.d/<SERVICE>
  # indirection that wasn't loading agent.env correctly.
  cat > "/etc/init.d/${SERVICE_NAME}" <<EOF
#!/sbin/openrc-run
description="${APP_NAME} Agent"
command="${BINARY_PATH}"
command_args=""
command_background=true
pidfile="/run/\${RC_SVCNAME}.pid"
output_log="/var/log/${SERVICE_NAME}.log"
error_log="/var/log/${SERVICE_NAME}.log"

depend() {
    need net
}

start_pre() {
    if [ -f ${ENV_FILE} ]; then
        set -a
        . ${ENV_FILE}
        set +a
    fi
}
EOF
  chmod +x "/etc/init.d/${SERVICE_NAME}"
  rc-update add "$SERVICE_NAME" default 2>/dev/null || true
  rc-service "$SERVICE_NAME" restart 2>/dev/null || rc-service "$SERVICE_NAME" start 2>/dev/null || {
    echo ""
    echo "ERROR: Service did not start successfully."
    echo "Check the logs:"
    echo "  Linux (OpenRC):  sudo tail -100 /var/log/${SERVICE_NAME}.log"
    echo "  Linux (systemd): sudo journalctl -u ${SERVICE_NAME} -n 100 --no-pager"
    echo ""
    echo "Once you've diagnosed the issue, restart with:"
    echo "  Linux (OpenRC):  sudo rc-service ${SERVICE_NAME} restart"
    echo "  Linux (systemd): sudo systemctl restart ${SERVICE_NAME}"
    exit 1
  }
else
  echo ""
  echo "ERROR: Neither systemd nor OpenRC detected on this host."
  echo "The vaporRMM agent requires one of:"
  echo "  - systemd (Ubuntu/Debian/RHEL/Fedora/Arch)"
  echo "  - OpenRC  (Alpine/Gentoo)"
  echo ""
  echo "Install an init system, then re-run this install script."
  exit 1
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
  echo "View logs:     tail -f /var/log/${SERVICE_NAME}.log"
fi
`, companyName, companyName, serverURL, serverURL, serverURL, serverURL, serverURL, serverURL, serverURL, appName, serverURL, iconURL)
}
