package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/utils"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

var tsAuthKeyRe = regexp.MustCompile(`^[a-zA-Z0-9_:-]+$`)

// tailscaleTagRe matches Tailscale's accepted tag format ("tag:name") with
// a strict alnum+hyphen body. Anything outside this set goes near
// `tailscale auth-key create --tag=<value>` and could escape into the
// argv parser if Tailscale ever changed how it tokenises (e.g. "=" as
// inner separator). Cap at 64 chars to bound the exec arglist size when
// many tags are passed.
var tailscaleTagRe = regexp.MustCompile(`^tag:[a-z0-9-]{1,64}$`)

// tenantFilter returns a SQL fragment and its args to enforce tenant scoping.
// Returns "", nil for super_admin (cross-tenant access).
// Returns " AND tenant_id = ?", [tid] for everyone else.
func tenantFilter(c *fiber.Ctx) (string, []interface{}) {
	role, _ := c.Locals("user_role").(string)
	if role == "super_admin" {
		return "", nil
	}
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		tenantID = "default"
	}
	return " AND tenant_id = ?", []interface{}{tenantID}
}

// callerTenantID returns the tenant_id from the request locals (default fallback).
func callerTenantID(c *fiber.Ctx) string {
	tenantID, _ := c.Locals("tenant_id").(string)
	if tenantID == "" {
		return "default"
	}
	return tenantID
}

// csvSafe prefixes values that start with formula-triggering characters so
// spreadsheet applications (Excel, LibreOffice) do not execute them as formulas.
func csvSafe(s string) string {
	if len(s) > 0 {
		switch s[0] {
		case '=', '+', '-', '@', '\t', '\r':
			return "'" + s
		}
	}
	return s
}

var allowedCommandTypes = map[string]bool{
	"shell":  true,
	"script": true,
}

func RegisterDeviceRoutes(api, devices fiber.Router, cfg Config) {
	// Get device by ID
	devices.Get("/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		row := db.DB.QueryRow(`SELECT id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version,
			status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model,
			cpu, memory, disk_size, timezone, agent_port, agent_ip, tags FROM devices WHERE id = ?`+tf, args...)
		d, err := utils.ScanDevice(row)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		return c.JSON(d)
	})

	// Create device
	devices.Post("/", func(c *fiber.Ctx) error {
		var device models.NewDeviceInput
		if err := c.BodyParser(&device); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		deviceID := uuid.New().String()
		now := time.Now().Unix()
		userID, _ := c.Locals("user_id").(string)
		_, err := db.DB.Exec(`INSERT INTO devices (id, name, hostname, ip_address, mac_address, os_name, os_version, status, last_seen, created_at, user_id, tenant_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			deviceID, device.Name, device.Hostname, device.IPAddress, device.MacAddress,
			device.OSName, device.OSVersion, "offline", now, now, userID, callerTenantID(c))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to insert device"})
		}
		events.AuditLogTenant(callerTenantID(c), userID, "device.create", "device", deviceID, fmt.Sprintf("created device %s", device.Hostname), c.IP())
		return c.Status(fiber.StatusCreated).JSON(models.ServerDevice{
			ID: deviceID, Name: device.Name, Hostname: device.Hostname, IPAddress: device.IPAddress,
			OSName: device.OSName, OSVersion: device.OSVersion, Status: "offline", CreatedAt: now, LastSeen: now,
		})
	})

	// Update device
	devices.Put("/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		var update models.UpdateDeviceInput
		if err := c.BodyParser(&update); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}

		fields := []string{"last_seen = ?"}
		args := []interface{}{time.Now().Unix()}
		if update.Name != nil {
			fields = append(fields, "name = ?")
			args = append(args, *update.Name)
		}
		if update.Hostname != nil {
			fields = append(fields, "hostname = ?")
			args = append(args, *update.Hostname)
		}
		if update.Status != nil {
			fields = append(fields, "status = ?")
			args = append(args, *update.Status)
		}
		if update.Tags != nil {
			fields = append(fields, "tags = ?")
			args = append(args, strings.Join(*update.Tags, ","))
		}
		args = append(args, id)
		tf, tArgs := tenantFilter(c)
		args = append(args, tArgs...)

		query := fmt.Sprintf("UPDATE devices SET %s WHERE id = ?"+tf, strings.Join(fields, ", "))
		result, err := db.DB.Exec(query, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update device"})
		}
		if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		readArgs := append([]interface{}{id}, tArgs...)
		row := db.DB.QueryRow(`SELECT id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version,
			status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model,
			cpu, memory, disk_size, timezone, agent_port, agent_ip, tags FROM devices WHERE id = ?`+tf, readArgs...)
		d, err := utils.ScanDevice(row)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found after update"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "device.update", "device", id, "updated device", c.IP())
		return c.JSON(d)
	})

	// Delete device
	devices.Delete("/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		result, err := db.DB.Exec("DELETE FROM devices WHERE id = ?"+tf, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete device"})
		}
		if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		callerTenant := callerTenantID(c)
		role, _ := c.Locals("user_role").(string)
		isSuper := auth.IsSuperAdmin(role)
		auth.TokenMu.Lock()
		for token, at := range auth.RegisteredTokens {
			if at.DeviceID != id {
				continue
			}
			if !isSuper && at.TenantID != callerTenant {
				continue
			}
			delete(auth.RegisteredTokens, token)
			if isSuper {
				if _, err := db.DB.Exec(`DELETE FROM agent_tokens WHERE token_hash = ?`, token); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			} else {
				if _, err := db.DB.Exec(`DELETE FROM agent_tokens WHERE token_hash = ? AND tenant_id = ?`, token, callerTenant); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			}
			break
		}
		auth.TokenMu.Unlock()
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenant, userID, "device.delete", "device", id, "deleted device", c.IP())
		return c.JSON(fiber.Map{"message": "Device deleted successfully"})
	})

	// Heartbeat
	devices.Post("/:id/heartbeat", func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		updArgs := append([]interface{}{time.Now().Unix(), "online", id}, tArgs...)
		result, err := db.DB.Exec("UPDATE devices SET last_seen = ?, status = ? WHERE id = ?"+tf, updArgs...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update heartbeat"})
		}
		if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		readArgs := append([]interface{}{id}, tArgs...)
		row := db.DB.QueryRow(`SELECT id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version,
			status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model,
			cpu, memory, disk_size, timezone, agent_port, agent_ip, tags FROM devices WHERE id = ?`+tf, readArgs...)
		d, err := utils.ScanDevice(row)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		return c.JSON(d)
	})

	// Send command
	devices.Post("/:id/command", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		var cmdReq struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		}
		if err := c.BodyParser(&cmdReq); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		if cmdReq.Type == "" || cmdReq.Command == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "type and command are required"})
		}
		if !allowedCommandTypes[cmdReq.Type] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid command type"})
		}

		tf, tArgs := tenantFilter(c)
		lookupArgs := append([]interface{}{id}, tArgs...)
		row := db.DB.QueryRow(`SELECT id, hostname, agent_ip, agent_port FROM devices WHERE id = ?`+tf, lookupArgs...)
		d, err := utils.ScanDevice(row)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		cmdID := uuid.New().String()
		cmdData := models.CommandRequest{
			ID: cmdID, Type: cmdReq.Type, Payload: map[string]interface{}{"command": cmdReq.Command}, CreatedAt: time.Now(),
		}
		payloadJSON, _ := json.Marshal(cmdData)
		_, err = db.DB.Exec(`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			cmdID, id, cmdReq.Type, string(payloadJSON), "pending", time.Now().Unix(), callerTenantID(c))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create command"})
		}

		agentPort := cfg.DefaultAgentWSPort
		if d.AgentPort != nil {
			agentPort = *d.AgentPort
		}
		agentIP := ""
		if d.AgentIP != nil {
			agentIP = *d.AgentIP
		}

		auth.TokenMu.RLock()
		var deviceToken string
		for t, at := range auth.RegisteredTokens {
			if at.DeviceID == id {
				deviceToken = t
				break
			}
		}
		auth.TokenMu.RUnlock()

		go func() {
			if sendErr := utils.SendCommandToDevice(agentIP, agentPort, deviceToken, payloadJSON); sendErr != nil {
				slog.Error("failed to send command", "command_id", cmdID, "device_id", id, "error", sendErr)
				if _, err := db.DB.Exec(`UPDATE device_commands SET status = ?, output = ?, finished_at = ? WHERE id = ?`, "failed", sendErr.Error(), time.Now().Unix(), cmdID); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			} else {
				if _, err := db.DB.Exec(`UPDATE device_commands SET status = ?, finished_at = ? WHERE id = ?`, "completed", time.Now().Unix(), cmdID); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			}
		}()

		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "device.command", "device", id, fmt.Sprintf("sent %s command", cmdReq.Type), c.IP())
		return c.JSON(fiber.Map{"message": "Command sent", "command_id": cmdID})
	})

	// Sunshine endpoints
	devices.Get("/:id/sunshine", func(c *fiber.Ctx) error {
		id := c.Params("id")
		var hostname, ipAddress string
		var agentIP *string
		var sunshineInstalled, sunshineRunning int
		var sunshinePort int
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{cfg.DefaultSunshinePort, id}, tArgs...)
		err := db.DB.QueryRow(`SELECT hostname, ip_address, agent_ip, COALESCE(sunshine_installed,0), COALESCE(sunshine_running,0), COALESCE(sunshine_port,?) FROM devices WHERE id = ?`+tf, args...).Scan(
			&hostname, &ipAddress, &agentIP, &sunshineInstalled, &sunshineRunning, &sunshinePort)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		deviceIP := ipAddress
		if agentIP != nil && *agentIP != "" {
			deviceIP = *agentIP
		}
		sunshineInfo := &models.SunshineStatus{Installed: sunshineInstalled == 1, Running: sunshineRunning == 1, Port: sunshinePort}
		resp := fiber.Map{"device_id": id, "hostname": hostname, "device_ip": deviceIP, "sunshine": sunshineInfo, "moonlight_url": fmt.Sprintf("moonlight://%s", deviceIP), "web_url": fmt.Sprintf("http://%s:%d", deviceIP, sunshinePort)}
		if cfg.MoonlightWebURL != "" {
			resp["moonlight_web_url"] = cfg.MoonlightWebURL
		}
		return c.JSON(resp)
	})

	// Install Sunshine on device
	devices.Post("/:id/sunshine/install", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		row := db.DB.QueryRow(`SELECT id, hostname, agent_ip, agent_port FROM devices WHERE id = ?`+tf, append([]interface{}{id}, tArgs...)...)
		d, err := utils.ScanDevice(row)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		var installCmd string
		switch d.OSName {
		case "windows":
			installCmd = fmt.Sprintf(`powershell -Command "Invoke-WebRequest -Uri 'https://github.com/LizardByte/Sunshine/releases/download/%s/sunshine-windows-installer.exe' -OutFile '$env:TEMP\sunshine.exe'; Start-Process -Wait -FilePath '$env:TEMP\sunshine.exe' -ArgumentList '/S'"`, cfg.SunshineVersion)
		case "darwin":
			installCmd = `brew install sunshine 2>/dev/null || echo "Install Homebrew first: https://brew.sh"`
		default:
			installCmd = fmt.Sprintf(`curl -fsSL https://github.com/LizardByte/Sunshine/releases/download/%s/sunshine-ubuntu-24.04-amd64.deb -o /tmp/sunshine.deb && dpkg -i /tmp/sunshine.deb || apt-get install -f -y`, cfg.SunshineVersion)
		}

		cmdID := uuid.New().String()
		cmdData := models.CommandRequest{
			ID: cmdID, Type: "shell", Payload: map[string]interface{}{"command": installCmd}, CreatedAt: time.Now(),
		}
		payloadJSON, _ := json.Marshal(cmdData)
		_, err = db.DB.Exec(`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			cmdID, id, "shell", string(payloadJSON), "pending", time.Now().Unix(), callerTenantID(c))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create command"})
		}

		agentPort := cfg.DefaultAgentWSPort
		if d.AgentPort != nil {
			agentPort = *d.AgentPort
		}
		agentIP := ""
		if d.AgentIP != nil {
			agentIP = *d.AgentIP
		}

		auth.TokenMu.RLock()
		var deviceToken string
		for t, at := range auth.RegisteredTokens {
			if at.DeviceID == id {
				deviceToken = t
				break
			}
		}
		auth.TokenMu.RUnlock()

		go func() {
			if sendErr := utils.SendCommandToDevice(agentIP, agentPort, deviceToken, payloadJSON); sendErr != nil {
				slog.Error("failed to send sunshine install command", "command_id", cmdID, "device_id", id, "error", sendErr)
				if _, err := db.DB.Exec(`UPDATE device_commands SET status = ?, output = ?, finished_at = ? WHERE id = ?`, "failed", sendErr.Error(), time.Now().Unix(), cmdID); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			} else {
				if _, err := db.DB.Exec(`UPDATE device_commands SET status = ?, finished_at = ? WHERE id = ?`, "completed", time.Now().Unix(), cmdID); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			}
		}()

		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "device.sunshine.install", "device", id, "sent sunshine install command", c.IP())
		return c.JSON(fiber.Map{"message": "Sunshine install command sent", "command_id": cmdID})
	})

	// Fetch Sunshine pairing PIN from device
	devices.Get("/:id/sunshine/pin", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		row := db.DB.QueryRow(`SELECT id, hostname, agent_ip, agent_port FROM devices WHERE id = ?`+tf, append([]interface{}{id}, tArgs...)...)
		d, err := utils.ScanDevice(row)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		agentPort := cfg.DefaultAgentWSPort
		if d.AgentPort != nil {
			agentPort = *d.AgentPort
		}
		agentIP := ""
		if d.AgentIP != nil {
			agentIP = *d.AgentIP
		}

		auth.TokenMu.RLock()
		var deviceToken string
		for t, at := range auth.RegisteredTokens {
			if at.DeviceID == id {
				deviceToken = t
				break
			}
		}
		auth.TokenMu.RUnlock()

		pin, fetchErr := utils.FetchSunshinePIN(agentIP, agentPort, deviceToken)
		if fetchErr != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Failed to fetch PIN", "message": fetchErr.Error()})
		}

		return c.JSON(fiber.Map{"pin": pin, "device_id": id})
	})

	// Submit a Moonlight-shown PIN to the device's local Sunshine API.
	// This is the correct pairing model — Moonlight (client) generates
	// the PIN, user enters it here, agent forwards to Sunshine. Replaces
	// the old "fetch PIN from logs" flow which depended on the user
	// already typing the PIN into Sunshine's own UI.
	devices.Post("/:id/sunshine/pair", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		var req struct {
			PIN  string `json:"pin"`
			Name string `json:"name,omitempty"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		// Format check at the edge so we don't make a useless agent
		// round trip when the input is obviously bad.
		req.PIN = strings.TrimSpace(req.PIN)
		if len(req.PIN) < 4 || len(req.PIN) > 8 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "PIN must be 4-8 digits"})
		}
		for _, r := range req.PIN {
			if r < '0' || r > '9' {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "PIN must be digits only"})
			}
		}
		if len(req.Name) > 64 {
			req.Name = req.Name[:64]
		}

		tf, tArgs := tenantFilter(c)
		row := db.DB.QueryRow(`SELECT id, hostname, agent_ip, agent_port FROM devices WHERE id = ?`+tf, append([]interface{}{id}, tArgs...)...)
		d, err := utils.ScanDevice(row)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		agentPort := cfg.DefaultAgentWSPort
		if d.AgentPort != nil {
			agentPort = *d.AgentPort
		}
		agentIP := ""
		if d.AgentIP != nil {
			agentIP = *d.AgentIP
		}

		auth.TokenMu.RLock()
		var deviceToken string
		for t, at := range auth.RegisteredTokens {
			if at.DeviceID == id {
				deviceToken = t
				break
			}
		}
		auth.TokenMu.RUnlock()

		if err := utils.SubmitSunshinePIN(agentIP, agentPort, deviceToken, req.PIN, req.Name); err != nil {
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "agent rejected pair", "message": err.Error()})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "sunshine.pair", "device", id, "submitted Moonlight PIN", c.IP())
		return c.JSON(fiber.Map{"message": "paired"})
	})

	// Install Tailscale on device
	devices.Post("/:id/tailscale/install", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		row := db.DB.QueryRow(`SELECT id, hostname, agent_ip, agent_port FROM devices WHERE id = ?`+tf, append([]interface{}{id}, tArgs...)...)
		d, err := utils.ScanDevice(row)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		var req struct {
			AuthKey string `json:"auth_key,omitempty"`
		}
		_ = c.BodyParser(&req)

		var installCmd string
		switch d.OSName {
		case "windows":
			installCmd = `powershell -Command "Invoke-WebRequest -Uri 'https://pkgs.tailscale.com/stable/tailscale-setup-latest.exe' -OutFile '$env:TEMP\tailscale.exe'; Start-Process -Wait -FilePath '$env:TEMP\tailscale.exe' -ArgumentList '/S'"`
		case "darwin":
			installCmd = `brew install tailscale 2>/dev/null || echo "Install Homebrew first: https://brew.sh"`
		default:
			installCmd = `curl -fsSL https://tailscale.com/install.sh | sh`
		}

		if req.AuthKey != "" {
			if len(req.AuthKey) > 256 || !tsAuthKeyRe.MatchString(req.AuthKey) {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid auth key format"})
			}
			installCmd += fmt.Sprintf(` && tailscale up --authkey %s --accept-routes`, req.AuthKey)
		}

		cmdID := uuid.New().String()
		cmdData := models.CommandRequest{
			ID: cmdID, Type: "shell", Payload: map[string]interface{}{"command": installCmd}, CreatedAt: time.Now(),
		}
		payloadJSON, _ := json.Marshal(cmdData)
		_, err = db.DB.Exec(`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			cmdID, id, "shell", string(payloadJSON), "pending", time.Now().Unix(), callerTenantID(c))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create command"})
		}

		agentPort := cfg.DefaultAgentWSPort
		if d.AgentPort != nil {
			agentPort = *d.AgentPort
		}
		agentIP := ""
		if d.AgentIP != nil {
			agentIP = *d.AgentIP
		}

		auth.TokenMu.RLock()
		var deviceToken string
		for t, at := range auth.RegisteredTokens {
			if at.DeviceID == id {
				deviceToken = t
				break
			}
		}
		auth.TokenMu.RUnlock()

		go func() {
			if sendErr := utils.SendCommandToDevice(agentIP, agentPort, deviceToken, payloadJSON); sendErr != nil {
				slog.Error("failed to send tailscale install command", "command_id", cmdID, "device_id", id, "error", sendErr)
				if _, err := db.DB.Exec(`UPDATE device_commands SET status = ?, output = ?, finished_at = ? WHERE id = ?`, "failed", sendErr.Error(), time.Now().Unix(), cmdID); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			} else {
				if _, err := db.DB.Exec(`UPDATE device_commands SET status = ?, finished_at = ? WHERE id = ?`, "completed", time.Now().Unix(), cmdID); err != nil {
					slog.Warn("db exec failed", "error", err)
				}
			}
		}()

		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "device.tailscale.install", "device", id, "sent tailscale install command", c.IP())
		return c.JSON(fiber.Map{"message": "Tailscale install command sent", "command_id": cmdID})
	})

	// Tailscale endpoints
	devices.Get("/:id/tailscale", func(c *fiber.Ctx) error {
		id := c.Params("id")
		var hostname, ipAddress string
		var agentIP, tailscaleIP, tailscaleHostname, tailscaleBackendState *string
		var tailscaleInstalled, tailscaleConnected, tailscalePeers int
		tf, tArgs := tenantFilter(c)
		err := db.DB.QueryRow(`SELECT hostname, ip_address, agent_ip, COALESCE(tailscale_installed,0), COALESCE(tailscale_connected,0), tailscale_ip, tailscale_hostname, COALESCE(tailscale_peers,0), tailscale_backend_state FROM devices WHERE id = ?`+tf, append([]interface{}{id}, tArgs...)...).Scan(
			&hostname, &ipAddress, &agentIP, &tailscaleInstalled, &tailscaleConnected, &tailscaleIP, &tailscaleHostname, &tailscalePeers, &tailscaleBackendState)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		tsInfo := &models.TailscaleStatus{Installed: tailscaleInstalled == 1, Connected: tailscaleConnected == 1, Peers: tailscalePeers}
		if tailscaleIP != nil {
			tsInfo.IP = *tailscaleIP
		}
		if tailscaleHostname != nil {
			tsInfo.Hostname = *tailscaleHostname
		}
		if tailscaleBackendState != nil {
			tsInfo.BackendState = *tailscaleBackendState
		}
		deviceIP := ipAddress
		if tsInfo.IP != "" {
			deviceIP = tsInfo.IP
		} else if agentIP != nil && *agentIP != "" {
			deviceIP = *agentIP
		}
		return c.JSON(fiber.Map{"device_id": id, "hostname": hostname, "device_ip": deviceIP, "tailscale": tsInfo})
	})

	devices.Post("/:id/tailscale/auth-key", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		_, err := utils.ScanDevice(db.DB.QueryRow(`SELECT id, hostname FROM devices WHERE id = ?`+tf, append([]interface{}{id}, tArgs...)...))
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		var req struct {
			Reusable  bool     `json:"reusable,omitempty"`
			Ephemeral bool     `json:"ephemeral,omitempty"`
			Tags      []string `json:"tags,omitempty"`
		}
		_ = c.BodyParser(&req)
		args := []string{"tailscale", "auth-key", "create"}
		if req.Reusable {
			args = append(args, "--reusable")
		}
		if req.Ephemeral {
			args = append(args, "--ephemeral")
		}
		for _, tag := range req.Tags {
			// Tailscale tags are formatted "tag:name". Restrict to the
			// charset Tailscale itself accepts so a value like
			// "name=--ephemeral" or "name `evil`" can't subvert the
			// CLI argument parser. Length cap defends against memory
			// exhaustion on the exec arglist.
			if !tailscaleTagRe.MatchString(tag) {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid tag format (allowed: tag:[a-z0-9-]+, max 64 chars)"})
			}
			args = append(args, "--tag="+tag)
		}
		cmd := exec.Command(args[0], args[1:]...)
		output, err := cmd.Output()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate auth key", "message": "Ensure Tailscale CLI is installed and authenticated on the server"})
		}
		return c.JSON(fiber.Map{"auth_key": strings.TrimSpace(string(output)), "message": "Auth key generated successfully"})
	})

	// ============================================================
	// Device advanced endpoints
	// ============================================================
	// List with filtering, sorting, pagination
	devices.Get("/", func(c *fiber.Ctx) error {
		limit, _ := strconv.Atoi(c.Query("limit", "50"))
		offset, _ := strconv.Atoi(c.Query("offset", "0"))
		if limit < 1 || limit > cfg.MaxDevicesLimit {
			limit = 50
		}
		if offset < 0 {
			offset = 0
		}
		statusFilter := c.Query("status", "")
		osFilter := c.Query("os", "")
		search := c.Query("search", "")
		sortBy := c.Query("sort_by", "last_seen")
		sortOrder := c.Query("sort_order", "desc")
		allowedSort := map[string]bool{"last_seen": true, "hostname": true, "status": true, "created_at": true}
		if !allowedSort[sortBy] {
			sortBy = "last_seen"
		}
		if sortOrder != "asc" && sortOrder != "desc" {
			sortOrder = "desc"
		}
		whereParts := []string{"1=1"}
		args := []interface{}{}
		if statusFilter != "" {
			whereParts = append(whereParts, "status = ?")
			args = append(args, statusFilter)
		}
		if osFilter != "" {
			whereParts = append(whereParts, "os_name = ?")
			args = append(args, osFilter)
		}
		if search != "" {
			whereParts = append(whereParts, "(hostname LIKE ? OR name LIKE ?)")
			args = append(args, "%"+search+"%", "%"+search+"%")
		}
		role, _ := c.Locals("user_role").(string)
		if !auth.IsSuperAdmin(role) {
			whereParts = append(whereParts, "tenant_id = ?")
			args = append(args, callerTenantID(c))
		}
		// Non-admin users (within a tenant) only see devices they own
		if role != "admin" && !auth.IsSuperAdmin(role) {
			userID, ok := c.Locals("user_id").(string)
			if ok && userID != "" {
				whereParts = append(whereParts, "(user_id = ? OR user_id IS NULL)")
				args = append(args, userID)
			}
		}
		whereClause := strings.Join(whereParts, " AND ")
		orderClause := fmt.Sprintf("ORDER BY %s %s", sortBy, sortOrder)
		query := fmt.Sprintf(`SELECT id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version,
			status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model,
			cpu, memory, disk_size, timezone, agent_port, agent_ip, tags FROM devices WHERE %s %s LIMIT ? OFFSET ?`, whereClause, orderClause)
		args = append(args, limit, offset)
		rows, err := db.DB.Query(query, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query devices"})
		}
		defer rows.Close()
		deviceList := []models.ServerDevice{}
		for rows.Next() {
			d, err := utils.ScanDevice(rows)
			if err != nil {
				slog.Error("error scanning device", "error", err)
				continue
			}
			deviceList = append(deviceList, *d)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM devices WHERE %s", whereClause)
		var total int
		if err := db.DB.QueryRow(countQuery, args[:len(args)-2]...).Scan(&total); err != nil {
			slog.Warn("db query row scan failed", "error", err)
		}
		return c.JSON(fiber.Map{"data": deviceList, "total": total, "limit": limit, "offset": offset, "has_more": offset+len(deviceList) < total})
	})

	// Export devices CSV
	devices.Get("/export", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		format := c.Query("format", "csv")
		tf, tArgs := tenantFilter(c)
		exportQ := `SELECT id, hostname, ip_address, mac_address, os_name, os_version, agent_version, status, last_seen, created_at, cpu, memory, disk_size FROM devices`
		if tf != "" {
			exportQ += " WHERE 1=1" + tf
		}
		exportQ += " ORDER BY hostname"
		rows, err := db.DB.Query(exportQ, tArgs...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query devices"})
		}
		defer rows.Close()
		if format == "csv" {
			var buf strings.Builder
			buf.WriteString("id,hostname,ip_address,mac_address,os_name,os_version,agent_version,status,last_seen,created_at,cpu,memory,disk_size\n")
			for rows.Next() {
				var d models.ServerDevice
				var lastSeen, createdAt int64
				if err := rows.Scan(&d.ID, &d.Hostname, &d.IPAddress, &d.MacAddress, &d.OSName, &d.OSVersion, &d.AgentVersion, &d.Status, &lastSeen, &createdAt, &d.CPU, &d.Memory, &d.DiskSize); err != nil {
					slog.Warn("rows scan failed", "error", err)
				}
				cpu := ""
				if d.CPU != nil {
					cpu = *d.CPU
				}
				mem := int64(0)
				if d.Memory != nil {
					mem = *d.Memory
				}
				disk := int64(0)
				if d.DiskSize != nil {
					disk = *d.DiskSize
				}
				buf.WriteString(fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%d,%d\n",
					d.ID, csvSafe(d.Hostname), csvSafe(d.IPAddress), csvSafe(d.MacAddress),
					csvSafe(d.OSName), csvSafe(d.OSVersion), csvSafe(d.AgentVersion),
					d.Status, time.Unix(lastSeen, 0).Format(time.RFC3339), time.Unix(createdAt, 0).Format(time.RFC3339),
					csvSafe(cpu), mem, disk))
			}
			if err := rows.Err(); err != nil {
				slog.Warn("rows iteration error", "error", err)
			}
			c.Set("Content-Type", "text/csv")
			c.Set("Content-Disposition", `attachment; filename="devices.csv"`)
			return c.SendString(buf.String())
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Unsupported format. Use ?format=csv"})
	})

	// Bulk delete
	devices.Post("/bulk-delete", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		if len(req.IDs) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "No device IDs provided"})
		}
		placeholders := make([]string, len(req.IDs))
		args := make([]interface{}, 0, len(req.IDs)+1)
		for i, id := range req.IDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		tf, tArgs := tenantFilter(c)
		args = append(args, tArgs...)
		query := "DELETE FROM devices WHERE id IN (" + strings.Join(placeholders, ",") + ")" + tf
		result, err := db.DB.Exec(query, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete devices"})
		}
		rowsAffected, _ := result.RowsAffected()
		callerTenant := callerTenantID(c)
		role, _ := c.Locals("user_role").(string)
		isSuper := auth.IsSuperAdmin(role)
		auth.TokenMu.Lock()
		for token, at := range auth.RegisteredTokens {
			// Tenant gate: never delete tokens belonging to another tenant unless caller is super_admin.
			if !isSuper && at.TenantID != callerTenant {
				continue
			}
			for _, id := range req.IDs {
				if at.DeviceID == id {
					delete(auth.RegisteredTokens, token)
					if isSuper {
						if _, err := db.DB.Exec(`DELETE FROM agent_tokens WHERE token_hash = ?`, token); err != nil {
							slog.Warn("db exec failed", "error", err)
						}
					} else {
						if _, err := db.DB.Exec(`DELETE FROM agent_tokens WHERE token_hash = ? AND tenant_id = ?`, token, callerTenant); err != nil {
							slog.Warn("db exec failed", "error", err)
						}
					}
					break
				}
			}
		}
		auth.TokenMu.Unlock()
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenant, userID, "device.bulk_delete", "device", "", fmt.Sprintf("bulk deleted %d devices", rowsAffected), c.IP())
		return c.JSON(fiber.Map{"message": "Devices deleted successfully", "deleted": rowsAffected})
	})

	// Device metrics history
	devices.Get("/:id/metrics", func(c *fiber.Ctx) error {
		id := c.Params("id")
		var exists int
		tf, tArgs := tenantFilter(c)
		if err := db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE id = ?`+tf, append([]interface{}{id}, tArgs...)...).Scan(&exists); err != nil || exists == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		since := time.Now().Unix() - 3600
		if s := c.Query("since"); s != "" {
			if parsed, err := strconv.ParseInt(s, 10, 64); err == nil {
				since = parsed
			}
		}
		mtf, mtArgs := tenantFilter(c)
		mArgs := append([]interface{}{id, since}, mtArgs...)
		rows, err := db.DB.Query(`SELECT recorded_at, cpu_usage, memory_usage, disk_usage FROM metrics_history WHERE device_id = ? AND recorded_at > ?`+mtf+` ORDER BY recorded_at ASC`, mArgs...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query metrics"})
		}
		defer rows.Close()
		type metricPoint struct {
			Time   int64   `json:"time"`
			CPU    float64 `json:"cpu"`
			Memory float64 `json:"memory"`
			Disk   float64 `json:"disk"`
		}
		points := []metricPoint{}
		for rows.Next() {
			var p metricPoint
			if err := rows.Scan(&p.Time, &p.CPU, &p.Memory, &p.Disk); err != nil {
				continue
			}
			points = append(points, p)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		if points == nil {
			points = []metricPoint{}
		}
		return c.JSON(points)
	})

	// Command history
	devices.Get("/:id/commands", func(c *fiber.Ctx) error {
		id := c.Params("id")
		limit := 50
		if l := c.Query("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		if limit > cfg.MaxCommandLimit {
			limit = 200
		}
		ctf, ctArgs := tenantFilter(c)
		cArgs := append([]interface{}{id}, ctArgs...)
		cArgs = append(cArgs, limit)
		rows, err := db.DB.Query(`SELECT id, type, payload, status, output, created_at, finished_at FROM device_commands WHERE device_id = ?`+ctf+` ORDER BY created_at DESC LIMIT ?`, cArgs...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query commands"})
		}
		defer rows.Close()
		type command struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			Payload    string `json:"payload"`
			Status     string `json:"status"`
			Output     string `json:"output,omitempty"`
			CreatedAt  int64  `json:"created_at"`
			FinishedAt *int64 `json:"finished_at,omitempty"`
		}
		commands := []command{}
		for rows.Next() {
			var cmd command
			var finishedAt sql.NullInt64
			if err := rows.Scan(&cmd.ID, &cmd.Type, &cmd.Payload, &cmd.Status, &cmd.Output, &cmd.CreatedAt, &finishedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			if finishedAt.Valid {
				cmd.FinishedAt = &finishedAt.Int64
			}
			commands = append(commands, cmd)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		return c.JSON(fiber.Map{"commands": commands})
	})

	// ============================================================
	// File transfers
	// ============================================================
	devices.Get("/:id/file-transfers", func(c *fiber.Ctx) error {
		deviceID := c.Params("id")
		ftf, ftArgs := tenantFilter(c)
		ftQArgs := append([]interface{}{deviceID}, ftArgs...)
		rows, err := db.DB.Query(`SELECT id, device_id, type, file_name, file_path, status, progress, created_at, completed_at FROM file_transfers WHERE device_id = ?`+ftf+` ORDER BY created_at DESC`, ftQArgs...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query file transfers"})
		}
		defer rows.Close()
		type fileTransfer struct {
			ID          string `json:"id"`
			DeviceID    string `json:"device_id"`
			Type        string `json:"type"`
			FileName    string `json:"file_name"`
			FilePath    string `json:"file_path"`
			Status      string `json:"status"`
			Progress    int    `json:"progress"`
			CreatedAt   int64  `json:"created_at"`
			CompletedAt *int64 `json:"completed_at,omitempty"`
		}
		transfers := []fileTransfer{}
		for rows.Next() {
			var ft fileTransfer
			var completedAt sql.NullInt64
			if err := rows.Scan(&ft.ID, &ft.DeviceID, &ft.Type, &ft.FileName, &ft.FilePath, &ft.Status, &ft.Progress, &ft.CreatedAt, &completedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			if completedAt.Valid {
				ft.CompletedAt = &completedAt.Int64
			}
			transfers = append(transfers, ft)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		if transfers == nil {
			transfers = []fileTransfer{}
		}
		return c.JSON(fiber.Map{"file_transfers": transfers})
	})

	devices.Post("/:id/file-transfers", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		deviceID := c.Params("id")
		var req struct {
			Type     string `json:"type"`
			FileName string `json:"file_name"`
			FilePath string `json:"file_path"`
		}
		if err := c.BodyParser(&req); err != nil || req.Type == "" || req.FileName == "" || req.FilePath == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "type, file_name, and file_path are required"})
		}
		if req.Type != "upload" && req.Type != "download" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "type must be upload or download"})
		}
		// Verify the parent device belongs to caller's tenant before creating a child row.
		var deviceTenant string
		dtf, dtArgs := tenantFilter(c)
		dArgs := append([]interface{}{deviceID}, dtArgs...)
		if err := db.DB.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM devices WHERE id = ?`+dtf, dArgs...).Scan(&deviceTenant); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		transferID := uuid.New().String()
		_, err := db.DB.Exec(`INSERT INTO file_transfers (id, device_id, type, file_name, file_path, status, progress, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			transferID, deviceID, req.Type, req.FileName, req.FilePath, "pending", 0, time.Now().Unix(), deviceTenant)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create file transfer"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(deviceTenant, userID, "file_transfer.create", "file_transfer", transferID, fmt.Sprintf("created %s transfer for %s", req.Type, req.FileName), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": transferID, "device_id": deviceID, "type": req.Type, "file_name": req.FileName, "file_path": req.FilePath, "status": "pending", "message": "File transfer created"})
	})

	// ============================================================
	// Patch management
	// ============================================================
	api.Get("/devices/:id/patches", func(c *fiber.Ctx) error {
		deviceID := c.Params("id")
		ptf, ptArgs := tenantFilter(c)
		pArgs := append([]interface{}{deviceID}, ptArgs...)
		rows, err := db.DB.Query(`SELECT id, device_id, title, description, severity, status, installed_at, created_at FROM patches WHERE device_id = ?`+ptf+` ORDER BY created_at DESC`, pArgs...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query patches"})
		}
		defer rows.Close()
		type patch struct {
			ID          string `json:"id"`
			DeviceID    string `json:"device_id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Severity    string `json:"severity"`
			Status      string `json:"status"`
			InstalledAt *int64 `json:"installed_at,omitempty"`
			CreatedAt   int64  `json:"created_at"`
		}
		patches := []patch{}
		for rows.Next() {
			var p patch
			var installedAt sql.NullInt64
			if err := rows.Scan(&p.ID, &p.DeviceID, &p.Title, &p.Description, &p.Severity, &p.Status, &installedAt, &p.CreatedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			if installedAt.Valid {
				p.InstalledAt = &installedAt.Int64
			}
			patches = append(patches, p)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		return c.JSON(fiber.Map{"patches": patches})
	})

	api.Post("/devices/:id/patches", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		deviceID := c.Params("id")
		var req struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Severity    string `json:"severity"`
		}
		if err := c.BodyParser(&req); err != nil || req.Title == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Title is required"})
		}
		if req.Severity == "" {
			req.Severity = "medium"
		}
		// Verify the parent device belongs to caller's tenant first
		var deviceTenant string
		ptf, ptArgs := tenantFilter(c)
		dArgs := append([]interface{}{deviceID}, ptArgs...)
		if err := db.DB.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM devices WHERE id = ?`+ptf, dArgs...).Scan(&deviceTenant); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		patchID := uuid.New().String()
		_, err := db.DB.Exec(`INSERT INTO patches (id, device_id, title, description, severity, status, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			patchID, deviceID, req.Title, req.Description, req.Severity, "pending", time.Now().Unix(), deviceTenant)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create patch"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(deviceTenant, userID, "patch.create", "patch", patchID, fmt.Sprintf("created patch %s for device %s", req.Title, deviceID), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": patchID, "message": "Patch created successfully"})
	})

	api.Put("/patches/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		patchID := c.Params("id")
		var req struct {
			Status string `json:"status"`
		}
		if err := c.BodyParser(&req); err != nil || req.Status == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Status is required"})
		}
		if !writeablePatchStatuses[req.Status] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid status"})
		}
		var installedAt interface{}
		if req.Status == "installed" {
			installedAt = time.Now().Unix()
		}
		ptf, ptArgs := tenantFilter(c)
		updArgs := []interface{}{req.Status, installedAt, patchID}
		updArgs = append(updArgs, ptArgs...)
		result, err := db.DB.Exec(`UPDATE patches SET status = ?, installed_at = ? WHERE id = ?`+ptf, updArgs...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update patch"})
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Patch not found"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "patch.update", "patch", patchID, fmt.Sprintf("updated patch status to %s", req.Status), c.IP())
		return c.JSON(fiber.Map{"message": "Patch updated successfully"})
	})
}
