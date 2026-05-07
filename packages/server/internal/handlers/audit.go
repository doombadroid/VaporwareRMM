package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"log/slog"
)

func RegisterAuditRoutes(api fiber.Router, cfg Config) {
	api.Get("/audit-logs", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		limit := 100
		if l := c.Query("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		if limit > cfg.MaxAuditLimit {
			limit = cfg.MaxAuditLimit
		}
		rows, err := db.DB.Query(`SELECT id, user_id, action, resource_type, resource_id, details, ip_address, created_at FROM audit_logs ORDER BY created_at DESC LIMIT ?`, limit)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query audit logs"})
		}
		defer rows.Close()
		type auditLogEntry struct {
			ID           string `json:"id"`
			UserID       string `json:"user_id"`
			Action       string `json:"action"`
			ResourceType string `json:"resource_type"`
			ResourceID   string `json:"resource_id"`
			Details      string `json:"details"`
			IPAddress    string `json:"ip_address"`
			CreatedAt    int64  `json:"created_at"`
		}
		logs := []auditLogEntry{}
		for rows.Next() {
			var entry auditLogEntry
			if err := rows.Scan(&entry.ID, &entry.UserID, &entry.Action, &entry.ResourceType, &entry.ResourceID, &entry.Details, &entry.IPAddress, &entry.CreatedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			logs = append(logs, entry)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		return c.JSON(fiber.Map{"logs": logs})
	})
}
