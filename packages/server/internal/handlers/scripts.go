package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/utils"
)

func RegisterScriptRoutes(api fiber.Router, cfg Config) {
	api.Get("/scripts", func(c *fiber.Ctx) error {
		platform := c.Query("platform", "")
		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var query string
		var args []interface{}
		base := `SELECT id, name, description, content, platform, created_at, updated_at FROM scripts`
		where := []string{}
		if platform != "" {
			where = append(where, "(platform = ? OR platform = 'all')")
			args = append(args, platform)
		}
		if !auth.IsSuperAdmin(role) {
			where = append(where, "tenant_id = ?")
			args = append(args, tenantID)
		}
		if len(where) > 0 {
			query = base + " WHERE " + where[0]
			for i := 1; i < len(where); i++ {
				query += " AND " + where[i]
			}
			query += " ORDER BY name"
		} else {
			query = base + " ORDER BY name"
		}
		rows, err := db.DB.Query(query, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query scripts"})
		}
		defer rows.Close()
		type script struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			Content     string `json:"content"`
			Platform    string `json:"platform"`
			CreatedAt   int64  `json:"created_at"`
			UpdatedAt   int64  `json:"updated_at"`
		}
		scripts := []script{}
		for rows.Next() {
			var s script
			if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Content, &s.Platform, &s.CreatedAt, &s.UpdatedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			scripts = append(scripts, s)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		if scripts == nil {
			scripts = []script{}
		}
		return c.JSON(fiber.Map{"scripts": scripts})
	})

	api.Get("/scripts/:id", func(c *fiber.Ctx) error {
		scriptID := c.Params("id")
		var s struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			Content     string `json:"content"`
			Platform    string `json:"platform"`
			CreatedAt   int64  `json:"created_at"`
			UpdatedAt   int64  `json:"updated_at"`
		}
		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var err error
		if auth.IsSuperAdmin(role) {
			err = db.DB.QueryRow(`SELECT id, name, description, content, platform, created_at, updated_at FROM scripts WHERE id = ?`, scriptID).Scan(&s.ID, &s.Name, &s.Description, &s.Content, &s.Platform, &s.CreatedAt, &s.UpdatedAt)
		} else {
			err = db.DB.QueryRow(`SELECT id, name, description, content, platform, created_at, updated_at FROM scripts WHERE id = ? AND tenant_id = ?`, scriptID, tenantID).Scan(&s.ID, &s.Name, &s.Description, &s.Content, &s.Platform, &s.CreatedAt, &s.UpdatedAt)
		}
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Script not found"})
		}
		return c.JSON(s)
	})

	api.Post("/scripts", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Content     string `json:"content"`
			Platform    string `json:"platform"`
		}
		if err := c.BodyParser(&req); err != nil || req.Name == "" || req.Content == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Name and content are required"})
		}
		if len(req.Content) > 512*1024 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Script content exceeds 512 KB limit"})
		}
		if req.Platform == "" {
			req.Platform = "all"
		}
		scriptID := uuid.New().String()
		now := time.Now().Unix()
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		_, err := db.DB.Exec(`INSERT INTO scripts (id, name, description, content, platform, created_at, updated_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			scriptID, req.Name, req.Description, req.Content, req.Platform, now, now, tenantID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create script"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "script.create", "script", scriptID, fmt.Sprintf("created script %s", req.Name), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": scriptID, "message": "Script created successfully"})
	})

	api.Delete("/scripts/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		scriptID := c.Params("id")
		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var err error
		if auth.IsSuperAdmin(role) {
			_, err = db.DB.Exec(`DELETE FROM scripts WHERE id = ?`, scriptID)
		} else {
			_, err = db.DB.Exec(`DELETE FROM scripts WHERE id = ? AND tenant_id = ?`, scriptID, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete script"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "script.delete", "script", scriptID, "deleted script", c.IP())
		return c.JSON(fiber.Map{"message": "Script deleted successfully"})
	})

	api.Post("/devices/:id/scripts/:script_id/execute", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		deviceID := c.Params("id")
		scriptID := c.Params("script_id")
		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var scriptName, scriptContent string
		var err error
		if auth.IsSuperAdmin(role) {
			err = db.DB.QueryRow(`SELECT name, content FROM scripts WHERE id = ?`, scriptID).Scan(&scriptName, &scriptContent)
		} else {
			err = db.DB.QueryRow(`SELECT name, content FROM scripts WHERE id = ? AND tenant_id = ?`, scriptID, tenantID).Scan(&scriptName, &scriptContent)
		}
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Script not found"})
		}
		var row *sql.Row
		if auth.IsSuperAdmin(role) {
			row = db.DB.QueryRow(`SELECT id, hostname, agent_ip, agent_port FROM devices WHERE id = ?`, deviceID)
		} else {
			row = db.DB.QueryRow(`SELECT id, hostname, agent_ip, agent_port FROM devices WHERE id = ? AND tenant_id = ?`, deviceID, tenantID)
		}
		d, err := utils.ScanDevice(row)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		cmdID := uuid.New().String()
		cmdData := models.CommandRequest{
			ID: cmdID, Type: "script", Payload: map[string]interface{}{"command": scriptContent}, CreatedAt: time.Now(),
		}
		payloadJSON, _ := json.Marshal(cmdData)
		_, err = db.DB.Exec(`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			cmdID, deviceID, "script", string(payloadJSON), "pending", time.Now().Unix(), tenantID)
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
			if at.DeviceID == deviceID {
				deviceToken = t
				break
			}
		}
		auth.TokenMu.RUnlock()
		go func() {
			if sendErr := utils.SendCommandToDevice(agentIP, agentPort, deviceToken, payloadJSON); sendErr != nil {
				slog.Error("failed to send script", "command_id", cmdID, "device_id", deviceID, "error", sendErr)
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
		events.AuditLogTenant(tenantID, userID, "script.execute", "device", deviceID, fmt.Sprintf("executed script %s", scriptName), c.IP())
		return c.JSON(fiber.Map{"message": "Script execution started", "command_id": cmdID, "script": scriptName})
	})
}
