package handlers

import (
	"database/sql"
	"log/slog"
	"strconv"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
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
		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var rows *sql.Rows
		var err error
		if auth.IsSuperAdmin(role) {
			rows, err = db.DB.Query(`SELECT id, user_id, action, resource_type, resource_id, details, ip_address, created_at FROM audit_logs ORDER BY created_at DESC LIMIT ?`, limit)
		} else {
			rows, err = db.DB.Query(`SELECT id, user_id, action, resource_type, resource_id, details, ip_address, created_at FROM audit_logs WHERE tenant_id = ? ORDER BY created_at DESC LIMIT ?`, tenantID, limit)
		}
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

	// Tamper-evidence verifier. Walks the audit_log hash chain and
	// reports the first row whose stored signature doesn't recompute,
	// or whose signature is missing. Super_admin only — there is no
	// per-tenant scope because the chain is fleet-wide; a tenant_admin
	// who could verify only their own slice could be lied to about
	// rows that were deleted or rewritten in a different tenant slot.
	api.Get("/audit-logs/verify", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		role, _ := c.Locals("user_role").(string)
		if !auth.IsSuperAdmin(role) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "super_admin only"})
		}
		res, err := events.VerifyAuditChain()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(res)
	})
}
