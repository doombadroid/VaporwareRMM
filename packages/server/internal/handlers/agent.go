package handlers

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"vaporrmm/models"
	"vaporrmm/server/internal/ai/sysfeatures"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/utils"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func RegisterAgentRoutes(app *fiber.App, cfg Config) {
	// Agent registration
	app.Post("/agent/register", auth.RateLimiter(10, time.Minute), func(c *fiber.Ctx) error {
		var regInfo map[string]interface{}
		if err := c.BodyParser(&regInfo); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body", "message": "Failed to parse registration data"})
		}

		hostname, _ := regInfo["hostname"].(string)
		if hostname == "" || len(hostname) > 253 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "hostname is required and must be <= 253 chars"})
		}
		if strings.ContainsAny(hostname, "/\\$;&|<>'\"") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "hostname contains invalid characters"})
		}

		// Determine tenant binding from registration secret.
		// Per-tenant secret in tenants.registration_secret takes precedence.
		// Fallback: REGISTRATION_SECRET env var → 'default' tenant (backward compat).
		regSecret := c.Get("X-Registration-Secret")
		tenantID := ""
		if regSecret != "" {
			// registration_secret is stored as SHA-256 hex hash, never plaintext
			secretHash := fmt.Sprintf("%x", sha256.Sum256([]byte(regSecret)))
			if err := db.DB.QueryRow(`SELECT id FROM tenants WHERE registration_secret = ? AND status = 'active'`, secretHash).Scan(&tenantID); err != nil {
				tenantID = ""
			}
		}
		if tenantID == "" {
			envSecret := os.Getenv("REGISTRATION_SECRET")
			if envSecret != "" {
				if regSecret != envSecret {
					return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid registration secret"})
				}
				tenantID = "default"
			} else if regSecret == "" && os.Getenv("ALLOW_OPEN_REGISTRATION") == "1" {
				// Open registration explicitly opted in by operator → default tenant.
				// Use only for dev/CI; never in production.
				tenantID = "default"
			} else {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid or missing registration secret"})
			}
		}

		authHeader := c.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Bearer token required"})
		}
		// Enforce minimum entropy: a legitimate agent generates a 256-bit token
		// (43+ chars when base64-url encoded). Rejecting short tokens server-side
		// stops a misconfigured or malicious caller from registering with a guess
		// like "Bearer test" and ending up with a permanent device row.
		if len(token) < 32 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "agent token too short (need ≥32 chars of entropy)"})
		}

		// Enforce tenant device cap (0 = unlimited)
		var maxDevices int
		if err := db.DB.QueryRow(`SELECT COALESCE(max_devices,0) FROM tenants WHERE id = ?`, tenantID).Scan(&maxDevices); err != nil {
			slog.Warn("could not read tenant device cap", "tenant_id", tenantID, "error", err)
		}
		if maxDevices > 0 {
			var count int
			if err := db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE tenant_id = ?`, tenantID).Scan(&count); err == nil && count >= maxDevices {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error":   "Device limit reached",
					"message": fmt.Sprintf("Tenant %s has reached its device cap (%d). Contact your administrator.", tenantID, maxDevices),
				})
			}
		}

		now := time.Now().Unix()

		osName, _ := regInfo["os"].(string)
		osVersion, _ := regInfo["os_version"].(string)
		localIP, _ := regInfo["local_ip"].(string)
		macAddr, _ := regInfo["mac_address"].(string)
		cpuModel, _ := regInfo["cpu"].(string)
		agentVersion, _ := regInfo["agent_version"].(string)
		osClass := sysfeatures.ClassifyOS(osName)

		// Codex #6: on the re-registration branch we require the caller
		// to prove possession of the device's currently-bound agent
		// token (or the previous one inside the 60s rotation grace
		// window). Without that gate, anyone holding the tenant's
		// REGISTRATION_SECRET could take over any device by guessing
		// its hostname+MAC. The header is plaintext; the comparison
		// hashes before comparing so a timing oracle on the row's
		// previous_token_hash can't leak the prior secret. Header on
		// FIRST registration is ignored — the no-prior-token path is
		// the legitimate first-INSERT case (and, on a hostname-match
		// where the device row has lost every token, the
		// legacy-bypass case wired up in a later commit).
		existingAgentToken := strings.TrimPrefix(c.Get("X-Existing-Agent-Token"), "Bearer ")

		// Re-registration check + INSERT runs under db.DedupMu so a
		// concurrent dedup pass can't race with this critical section.
		// Without the lock, the dedup migration could be midway
		// through collapsing a duplicate set when a fresh register
		// lands inside the set, leaving a new duplicate that the
		// CREATE UNIQUE INDEX at the end of dedup would reject.
		// SQLite serialises writes natively; the mutex matters on
		// Postgres.
		db.DedupMu.Lock()
		var existingID string
		err := db.DB.QueryRow(
			`SELECT id FROM devices WHERE tenant_id = ? AND hostname = ? AND COALESCE(mac_address, '') = ?`,
			tenantID, hostname, macAddr,
		).Scan(&existingID)
		var deviceID string
		var registered bool
		var popVerdict auth.PoPVerdict = auth.PoPNoPriorToken
		switch {
		case err == nil && existingID != "":
			deviceID = existingID
			popVerdict = auth.VerifyAgentPoP(tenantID, deviceID, hostname, existingAgentToken)
			// One-time legacy bypass: an agent registered before this
			// commit has no persisted token to present. The check fires
			// when (a) the device has not consumed its bypass yet AND
			// (b) VAPOR_REFUSE_LEGACY_BYPASS_AFTER (if set) has not
			// passed AND one of:
			//   - PoPNoPriorToken (no active token row at all), or
			//   - PoPReject + active token row is a pre-Codex-#6 row
			//     (previous_token_hash IS NULL). This is the realistic
			//     upgrade path: legacy agent has an active row but the
			//     freshly-installed binary either generated a new
			//     bearer or never persisted the old one, so the PoP it
			//     presents doesn't match. Without this branch every
			//     restarted legacy endpoint would 409 forever.
			// PoPReject against a Codex-#6-era row (previous_token_hash
			// IS NOT NULL) is NOT eligible — that's the take-over
			// attempt the PoP gate was built to stop.
			legacyBypass := false
			if auth.IsLegacyAgentEligibleForBypass(deviceID) {
				switch {
				case popVerdict == auth.PoPNoPriorToken:
					legacyBypass = true
				case popVerdict == auth.PoPReject && auth.ActiveTokenIsLegacy(tenantID, deviceID, hostname):
					legacyBypass = true
				}
			}
			switch {
			case popVerdict == auth.PoPAcceptCurrent || popVerdict == auth.PoPAcceptGraceRotationAck || popVerdict == auth.PoPAcceptGraceCrashRecovery || legacyBypass:
				// PoP satisfied (current/grace) OR one-time legacy
				// bypass. UPDATE the device row and proceed to token
				// rotation. The legacy-bypass branch additionally
				// flips devices.legacy_pop_bypass_used so subsequent
				// re-registers for this device are on the standard
				// PoP track.
				if _, err := db.DB.Exec(
					`UPDATE devices SET ip_address = ?, os_name = ?, os_version = ?, agent_version = ?, status = 'online', last_seen = ?, cpu = ?, agent_ip = ?, os_class = ? WHERE id = ?`,
					localIP, osName, osVersion, agentVersion, now, cpuModel, localIP, osClass, deviceID,
				); err != nil {
					db.DedupMu.Unlock()
					return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to refresh device", "message": err.Error()})
				}
				if legacyBypass {
					if err := auth.MarkLegacyBypassConsumed(deviceID); err != nil {
						slog.Warn("could not mark legacy bypass consumed", "device_id", deviceID, "error", err)
					}
				}
				if popVerdict == auth.PoPAcceptGraceRotationAck || popVerdict == auth.PoPAcceptGraceCrashRecovery {
					events.AuditLogTenant(tenantID, "system", "device.register.pop_grace", "device", deviceID,
						fmt.Sprintf("grace_window_pop_accept verdict=%s hostname=%s ip=%s", popVerdict.String(), hostname, c.IP()),
						c.IP())
				}
				if legacyBypass {
					// WARN level intentionally — the bypass is a known
					// trust-on-first-use carve-out for pre-Codex-#6
					// agents and operators should be able to see it
					// happen in the log without grepping at debug
					// verbosity.
					slog.Warn("legacy agent pre-Codex-#6 pop bypass consumed", "device_id", deviceID, "hostname", hostname, "ip", c.IP())
					events.AuditLogTenant(tenantID, "system", "legacy_agent_pop_bypass", "device", deviceID,
						fmt.Sprintf("legacy_agent_pop_bypass hostname=%s ip=%s", hostname, c.IP()),
						c.IP())
				}
			default:
				// PoP rejected. The dedup lock is released before the
				// audit/webhook hooks fire — they don't touch the
				// devices/agent_tokens critical section and we want
				// concurrent legitimate re-registers to proceed.
				db.DedupMu.Unlock()
				events.AuditLogTenant(tenantID, "system", "device.register.pop_rejected", "device", deviceID,
					fmt.Sprintf("pop_rejected verdict=%s claimed_hostname=%s claimed_mac=%s ip=%s",
						popVerdict.String(), hostname, macAddr, c.IP()),
					c.IP())
				// Webhook fires at most once per device per hour; the
				// audit row above is unconditional. Bucket count
				// accumulates between fires so the next fire's
				// payload reflects the full burst.
				events.TriggerRegistrationConflictWebhook(tenantID, deviceID, hostname, macAddr, c.IP())
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{
					"error":   "Device exists; proof-of-possession required",
					"message": "This hostname is already registered. Re-registration requires presenting the current agent token in the X-Existing-Agent-Token header. If the agent has lost its token, an administrator must remove the device first.",
					"code":    409,
				})
			}
		default:
			deviceID = uuid.New().String()
			if _, err := db.DB.Exec(
				`INSERT INTO devices (id, name, hostname, ip_address, mac_address, os_name, os_version, agent_version, status, last_seen, created_at, cpu, agent_ip, tenant_id, os_class)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				deviceID, hostname, hostname, localIP, macAddr, osName, osVersion, agentVersion, "online", now, now, cpuModel, localIP, tenantID, osClass,
			); err != nil {
				db.DedupMu.Unlock()
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to register device", "message": err.Error()})
			}
			registered = true
		}
		db.DedupMu.Unlock()

		auth.RegisterAgentToken(token, deviceID, hostname, tenantID)
		if registered {
			events.TriggerWebhooks(tenantID, "device.registered", map[string]interface{}{"device_id": deviceID, "hostname": hostname, "timestamp": now})
			// Audit-log every NEW registration. Re-registers (token
			// rotation, agent reinstall) hit the UPDATE branch and don't
			// emit a new audit row; the heartbeat keeps the device
			// liveness signal flowing and the audit log doesn't need a
			// row per re-register flap.
			events.AuditLogTenant(tenantID, "system", "device.register", "device", deviceID, fmt.Sprintf("registered hostname=%s ip=%s", hostname, c.IP()), c.IP())
		}

		statusMsg := "registered"
		if !registered {
			statusMsg = "refreshed"
		}
		return c.JSON(fiber.Map{"device_id": deviceID, "hostname": hostname, "status": statusMsg, "message": "Device " + statusMsg + " successfully"})
	})

	// Agent heartbeat
	app.Post("/agent/heartbeat", auth.RateLimiter(agentHeartbeatRateLimit, time.Minute), auth.AgentAuthMiddleware(), func(c *fiber.Ctx) error {
		var heartbeatData map[string]interface{}
		if err := c.BodyParser(&heartbeatData); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}

		deviceID := c.Locals("device_id").(string)
		now := time.Now().Unix()

		status, _ := heartbeatData["status"].(string)
		if status == "" {
			status = "online"
		}

		var prevStatus string
		if err := db.DB.QueryRow(`SELECT status FROM devices WHERE id = ?`, deviceID).Scan(&prevStatus); err != nil {
			slog.Warn("db query row scan failed", "error", err)
		}

		result, err := db.DB.Exec("UPDATE devices SET last_seen = ?, status = ? WHERE id = ?", now, status, deviceID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update heartbeat"})
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		if prevStatus == "offline" && status == "online" {
			var hostname, ownerID, devTenant string
			if err := db.DB.QueryRow(`SELECT hostname, COALESCE(user_id,''), COALESCE(tenant_id,'default') FROM devices WHERE id = ?`, deviceID).Scan(&hostname, &ownerID, &devTenant); err != nil {
				slog.Warn("db query row scan failed", "error", err)
			}
			payload := map[string]interface{}{"device_id": deviceID, "hostname": hostname, "timestamp": now}
			events.TriggerWebhooks(devTenant, "device.online", payload)
			events.WSBroadcastFiltered(devTenant, ownerID, map[string]interface{}{"type": "device.online", "device_id": deviceID, "hostname": hostname, "timestamp": now})
		}

		cpuUsage, _ := heartbeatData["cpu_usage"].(float64)
		memUsage, _ := heartbeatData["memory_usage"].(float64)
		diskUsage, _ := heartbeatData["disk_usage"].(float64)
		// Clamp to [0,100]. A malicious or buggy agent can otherwise stuff
		// NaN, +Inf, or huge values into metrics_history and break aggregation.
		cpuUsage = clampPercent(cpuUsage)
		memUsage = clampPercent(memUsage)
		diskUsage = clampPercent(diskUsage)

		// Round-trip latency measured by the agent on its prior heartbeat.
		// Clamp at 5000ms so a misconfigured or malicious agent can't
		// poison the fleet average with absurd values. Stored on the
		// device row; dashboard /overview averages it across online devices.
		if rttRaw, ok := heartbeatData["network_latency_ms"].(float64); ok {
			rtt := int(rttRaw)
			if rtt < 0 {
				rtt = 0
			}
			if rtt > 5000 {
				rtt = 5000
			}
			if _, err := db.DB.Exec(`UPDATE devices SET network_latency_ms = ? WHERE id = ?`, rtt, deviceID); err != nil {
				slog.Warn("network_latency update failed", "error", err)
			}
		}
		if cpuUsage > 0 || memUsage > 0 || diskUsage > 0 {
			agentTenant, _ := c.Locals("tenant_id").(string)
			if agentTenant == "" {
				agentTenant = "default"
			}
			if _, err := db.DB.Exec(
				`INSERT INTO metrics_history (device_id, cpu_usage, memory_usage, disk_usage, recorded_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?)`,
				deviceID, cpuUsage, memUsage, diskUsage, now, agentTenant,
			); err != nil {
				slog.Warn("db exec failed", "error", err)
			}
		}

		// Process Sunshine status
		if sunshineRaw, ok := heartbeatData["sunshine"]; ok {
			if sunshineMap, ok := sunshineRaw.(map[string]interface{}); ok {
				installed, _ := sunshineMap["installed"].(bool)
				running, _ := sunshineMap["running"].(bool)
				portFloat, _ := sunshineMap["port"].(float64)
				port := int(portFloat)
				if port == 0 {
					port = cfg.DefaultSunshinePort
				}
				if _, err := db.DB.Exec(
					`UPDATE devices SET sunshine_installed=?, sunshine_running=?, sunshine_port=? WHERE id=?`,
					utils.BoolToInt(installed), utils.BoolToInt(running), port, deviceID,
				); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			}
		}

		// Process Tailscale status
		if tailscaleRaw, ok := heartbeatData["tailscale"]; ok {
			if tailscaleMap, ok := tailscaleRaw.(map[string]interface{}); ok {
				installed, _ := tailscaleMap["installed"].(bool)
				connected, _ := tailscaleMap["connected"].(bool)
				ip, _ := tailscaleMap["ip"].(string)
				tsHostname, _ := tailscaleMap["hostname"].(string)
				peersFloat, _ := tailscaleMap["peers"].(float64)
				backendState, _ := tailscaleMap["backend_state"].(string)
				peers := int(peersFloat)
				// Cap string fields. The agent is authenticated but a compromised
				// agent should not be able to push megabyte hostnames into the DB.
				ip = capLen(ip, 64)
				tsHostname = capLen(tsHostname, 256)
				backendState = capLen(backendState, 64)
				if peers < 0 || peers > 100000 {
					peers = 0
				}

				updateSQL := `UPDATE devices SET tailscale_installed=?, tailscale_connected=?, tailscale_ip=?, tailscale_hostname=?, tailscale_peers=?, tailscale_backend_state=?`
				args := []interface{}{utils.BoolToInt(installed), utils.BoolToInt(connected), ip, tsHostname, peers, backendState}
				if ip != "" {
					updateSQL += `, agent_ip=?`
					args = append(args, ip)
				}
				updateSQL += ` WHERE id=?`
				args = append(args, deviceID)
				if _, err := db.DB.Exec(updateSQL, args...); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			}
		}

		agentVersion, _ := heartbeatData["agent_version"].(string)
		updateAvailable := false
		if agentVersion != "" && agentVersion != "1.1.0" {
			updateAvailable = true
		}

		return c.JSON(fiber.Map{"status": "ok", "message": "Heartbeat received", "update_available": updateAvailable, "latest_version": "1.1.0"})
	})

	// Agent help request. Rate-limited so a single compromised agent can't
	// spam the device_commands table or page on-call by triggering thousands
	// of help-request rows in a tight loop.
	app.Post("/agent/help-request", auth.RateLimiter(5, time.Minute), auth.AgentAuthMiddleware(), func(c *fiber.Ctx) error {
		deviceID := c.Locals("device_id").(string)
		hostname := c.Locals("hostname").(string)

		var helpData map[string]interface{}
		if err := c.BodyParser(&helpData); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}

		now := time.Now().Unix()
		helpJSON, _ := json.Marshal(map[string]interface{}{
			"type": "help_request", "device_id": deviceID, "hostname": hostname, "timestamp": now, "details": helpData,
		})
		// Cap the payload landing in device_commands.payload. Without this
		// a compromised agent at the 5/min rate could write 5 * 4MB =
		// ~20MB/min into the table, with the data also showing up in
		// every audit / tenant-export read path. 64KB is more than enough
		// for the diagnostic context the help-request flow needs.
		if len(helpJSON) > 64*1024 {
			return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "help request payload too large (max 64 KiB)"})
		}

		commandID := uuid.New().String()
		agentTenant, _ := c.Locals("tenant_id").(string)
		if agentTenant == "" {
			agentTenant = "default"
		}
		_, err := db.DB.Exec(
			`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			commandID, deviceID, "help_request", string(helpJSON), "pending", now, agentTenant,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to store help request"})
		}

		slog.Info("Help request received", "device_id", deviceID, "hostname", hostname)
		return c.JSON(fiber.Map{"status": "ok", "message": "Help request sent to IT support", "request_id": commandID})
	})

	// Get pending commands for agent
	app.Get("/agent/:hostname/commands", auth.AgentAuthMiddleware(), func(c *fiber.Ctx) error {
		hostname := c.Params("hostname")
		deviceID := c.Locals("device_id").(string)

		var dbHostname string
		err := db.DB.QueryRow("SELECT hostname FROM devices WHERE id = ?", deviceID).Scan(&dbHostname)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		if dbHostname != hostname {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Hostname mismatch"})
		}

		rows, err := db.DB.Query(
			`SELECT payload FROM device_commands WHERE device_id = ? AND status = 'pending' ORDER BY created_at ASC`, deviceID,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query commands"})
		}
		defer rows.Close()

		commands := []models.CommandRequest{}
		for rows.Next() {
			var payload string
			if err := rows.Scan(&payload); err != nil {
				continue
			}
			var cmd models.CommandRequest
			if err := json.Unmarshal([]byte(payload), &cmd); err == nil {
				commands = append(commands, cmd)
			}
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		return c.JSON(commands)
	})

	// Agent submit command results
	app.Post("/agent/:hostname/results", auth.AgentAuthMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Results []models.CommandResult `json:"results"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		// Hard-cap result batch size. Prevents a compromised agent from filling
		// the device_commands table or burning a goroutine on a 100k-row update.
		if len(req.Results) > 1000 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "too many results in one batch (max 1000)"})
		}

		// Constrain the UPDATE to commands belonging to the authenticated
		// device. Without this constraint a compromised agent A could submit
		// fake "completed" rows for device B's pending commands by guessing
		// their UUIDs, marking them succeeded with attacker-supplied output.
		// device_id from the agent middleware is the trust root; the URL
		// :hostname param is informational only.
		deviceID, _ := c.Locals("device_id").(string)
		if deviceID == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "agent device unknown"})
		}
		for _, result := range req.Results {
			output := result.Output
			status := "completed"
			if !result.Success {
				status = "failed"
				output = result.Error
			}
			// Cap per-result output. Without this a compromised agent can
			// fire a 1000-row batch with 10MB outputs each (~10GB) every
			// time it polls; the rows then echo into command-history GETs
			// and tenant exports. The agent already truncates at 64KiB
			// (truncateOutput), so anything larger here is anomalous.
			const maxOutputBytes = 256 * 1024
			if len(output) > maxOutputBytes {
				output = output[:maxOutputBytes] + "...[truncated by server]"
			}
			if _, err := db.DB.Exec(
				`UPDATE device_commands SET status = ?, output = ?, finished_at = ? WHERE id = ? AND device_id = ?`,
				status, output, time.Now().Unix(), result.CommandID, deviceID,
			); err != nil {
				slog.Warn("db exec failed", "error", err)
			}
		}
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Agent version / update endpoints
	app.Get("/agent/version", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"version":       "1.1.0",
			"min_version":   "1.0.0",
			"download_url":  fmt.Sprintf("%s://%s/agent/update", c.Protocol(), c.Hostname()),
			"release_notes": "Bug fixes and performance improvements",
			"force_update":  false,
		})
	})

	app.Get("/agent/update", func(c *fiber.Ctx) error {
		updateScript := `#!/bin/bash
# vaporRMM Agent Auto-Update Script
set -euo pipefail

UPDATE_URL="` + fmt.Sprintf("%s://%s", c.Protocol(), c.Hostname()) + `"
INSTALL_DIR="/usr/local/bin"
BACKUP_DIR="/tmp/vaporrmm-backup-$(date +%s)"

echo "Checking for agent updates..."

ARCH=$(uname -m)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    armv7l)  ARCH="arm" ;;
esac

case "$OS" in
    linux)   PLATFORM="linux" ;;
    darwin)  PLATFORM="darwin" ;;
    mingw*|cygwin*) PLATFORM="windows" ;;
esac

echo "Platform: $PLATFORM/$ARCH"

if [ -f "$INSTALL_DIR/vaporrmm-agent" ]; then
    mkdir -p "$BACKUP_DIR"
    cp "$INSTALL_DIR/vaporrmm-agent" "$BACKUP_DIR/"
    echo "Backup created at $BACKUP_DIR"
fi

echo "Downloading latest agent..."
echo "Agent update complete. Restart the agent to apply changes."
echo "  systemctl restart vaporrmm-agent"
`
		c.Set("Content-Type", "text/x-shellscript")
		return c.SendString(updateScript)
	})

	// File transfer update from agent
	app.Put("/agent/file-transfer/:id", auth.AgentAuthMiddleware(), func(c *fiber.Ctx) error {
		transferID := c.Params("id")
		deviceID := c.Locals("device_id").(string)
		var req struct {
			Status   string `json:"status"`
			Progress int    `json:"progress"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		var completedAt interface{}
		if req.Status == "completed" || req.Status == "failed" {
			completedAt = time.Now().Unix()
		}
		// UPDATE constrained by device_id so a compromised agent A can't
		// flip transfers belonging to device B. RowsAffected is the
		// authority on whether the row matched — without checking it,
		// the webhook below would fire on every call (including ones
		// that touched zero rows because the transfer_id was guessed or
		// belongs to another device).
		res, err := db.DB.Exec(`UPDATE file_transfers SET status = ?, progress = ?, completed_at = ? WHERE id = ? AND device_id = ?`, req.Status, req.Progress, completedAt, transferID, deviceID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update file transfer"})
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "transfer not found for this device"})
		}
		ftTenant, _ := c.Locals("tenant_id").(string)
		if req.Status == "completed" {
			events.TriggerWebhooks(ftTenant, "file_transfer.completed", map[string]interface{}{"transfer_id": transferID, "device_id": deviceID, "timestamp": time.Now().Unix()})
		} else if req.Status == "failed" {
			events.TriggerWebhooks(ftTenant, "file_transfer.failed", map[string]interface{}{"transfer_id": transferID, "device_id": deviceID, "timestamp": time.Now().Unix()})
		}
		return c.JSON(fiber.Map{"message": "File transfer status updated"})
	})
}

func clampPercent(v float64) float64 {
	// Reject NaN / Inf and clamp the rest to [0,100].
	if v != v || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func capLen(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
