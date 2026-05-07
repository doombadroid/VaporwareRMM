package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/utils"
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

		// Optional registration secret for production deployments
		if regSecret := os.Getenv("REGISTRATION_SECRET"); regSecret != "" {
			if c.Get("X-Registration-Secret") != regSecret {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid registration secret"})
			}
		}

		authHeader := c.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Bearer token required"})
		}

		deviceID := uuid.New().String()
		now := time.Now().Unix()

		osName, _ := regInfo["os"].(string)
		osVersion, _ := regInfo["os_version"].(string)
		localIP, _ := regInfo["local_ip"].(string)
		macAddr, _ := regInfo["mac_address"].(string)
		cpuModel, _ := regInfo["cpu"].(string)
		agentVersion, _ := regInfo["agent_version"].(string)

		_, err := db.DB.Exec(
			`INSERT INTO devices (id, name, hostname, ip_address, mac_address, os_name, os_version, agent_version, status, last_seen, created_at, cpu, agent_ip)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			deviceID, hostname, hostname, localIP, macAddr, osName, osVersion, agentVersion, "online", now, now, cpuModel, localIP,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to register device", "message": err.Error()})
		}

		auth.RegisterAgentToken(token, deviceID, hostname)
		events.TriggerWebhooks("device.registered", map[string]interface{}{"device_id": deviceID, "hostname": hostname, "timestamp": now})

		return c.JSON(fiber.Map{"device_id": deviceID, "hostname": hostname, "status": "registered", "message": "Device registered successfully"})
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
			var hostname string
			if err := db.DB.QueryRow(`SELECT hostname FROM devices WHERE id = ?`, deviceID).Scan(&hostname); err != nil {
				slog.Warn("db query row scan failed", "error", err)
			}
			events.TriggerWebhooks("device.online", map[string]interface{}{"device_id": deviceID, "hostname": hostname, "timestamp": now})
			events.WSBroadcastMessage(map[string]interface{}{"type": "device.online", "device_id": deviceID, "hostname": hostname, "timestamp": now})
		}

		cpuUsage, _ := heartbeatData["cpu_usage"].(float64)
		memUsage, _ := heartbeatData["memory_usage"].(float64)
		diskUsage, _ := heartbeatData["disk_usage"].(float64)
		if cpuUsage > 0 || memUsage > 0 || diskUsage > 0 {
			if _, err := db.DB.Exec(
				`INSERT INTO metrics_history (device_id, cpu_usage, memory_usage, disk_usage, recorded_at) VALUES (?, ?, ?, ?, ?)`,
				deviceID, cpuUsage, memUsage, diskUsage, now,
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

	// Agent help request
	app.Post("/agent/help-request", auth.AgentAuthMiddleware(), func(c *fiber.Ctx) error {
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

		commandID := uuid.New().String()
		_, err := db.DB.Exec(
			`INSERT INTO device_commands (id, device_id, type, payload, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			commandID, deviceID, "help_request", string(helpJSON), "pending", now,
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

		for _, result := range req.Results {
			output := result.Output
			status := "completed"
			if !result.Success {
				status = "failed"
				output = result.Error
			}
			if _, err := db.DB.Exec(
				`UPDATE device_commands SET status = ?, output = ?, finished_at = ? WHERE id = ?`,
				status, output, time.Now().Unix(), result.CommandID,
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
		_, err := db.DB.Exec(`UPDATE file_transfers SET status = ?, progress = ?, completed_at = ? WHERE id = ? AND device_id = ?`, req.Status, req.Progress, completedAt, transferID, deviceID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update file transfer"})
		}
		if req.Status == "completed" {
			events.TriggerWebhooks("file_transfer.completed", map[string]interface{}{"transfer_id": transferID, "device_id": deviceID, "timestamp": time.Now().Unix()})
		} else if req.Status == "failed" {
			events.TriggerWebhooks("file_transfer.failed", map[string]interface{}{"transfer_id": transferID, "device_id": deviceID, "timestamp": time.Now().Unix()})
		}
		return c.JSON(fiber.Map{"message": "File transfer status updated"})
	})
}
