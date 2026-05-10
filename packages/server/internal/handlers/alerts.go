package handlers

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// EmitAlert persists an incident row that the dashboard /alerts page renders.
// Fire-and-forget: we don't surface insert errors to the caller because the
// alert path is supplementary (webhook + email already fired in parallel via
// events.TriggerWebhooks). De-duplication is the caller's responsibility —
// repeated identical (tenant, device, type) pairs become repeated rows.
func EmitAlert(tenantID, deviceID, alertType, severity, message string) {
	if tenantID == "" {
		tenantID = "default"
	}
	if severity == "" {
		severity = "warning"
	}
	id := uuid.New().String()
	if _, err := db.DB.Exec(
		`INSERT INTO alerts (id, tenant_id, device_id, type, severity, message, resolved, created_at) VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		id, tenantID, sqlNullable(deviceID), alertType, severity, message, time.Now().Unix(),
	); err != nil {
		slog.Warn("alert insert failed", "tenant", tenantID, "type", alertType, "error", err)
	}
}

func sqlNullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func RegisterAlertRoutes(api fiber.Router) {
	api.Get("/alert-settings", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var settings struct {
			SMTPHost     string `json:"smtp_host"`
			SMTPPort     int    `json:"smtp_port"`
			SMTPUser     string `json:"smtp_user"`
			SMTPPassword string `json:"smtp_password"`
			SMTPFrom     string `json:"smtp_from"`
			SMTPTLS      bool   `json:"smtp_tls"`
			Enabled      bool   `json:"enabled"`
		}
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		err := db.DB.QueryRow(`SELECT smtp_host, smtp_port, smtp_user, smtp_password, smtp_from, smtp_tls, enabled FROM alert_settings WHERE tenant_id = ?`, tenantID).Scan(
			&settings.SMTPHost, &settings.SMTPPort, &settings.SMTPUser, &settings.SMTPPassword, &settings.SMTPFrom, &settings.SMTPTLS, &settings.Enabled)
		if err != nil {
			return c.JSON(fiber.Map{"smtp_host": "", "smtp_port": 587, "smtp_user": "", "smtp_password": "", "smtp_from": "", "smtp_tls": true, "enabled": false})
		}
		// Mask password in response; presence indicated by non-empty value
		if settings.SMTPPassword != "" {
			settings.SMTPPassword = "********"
		}
		return c.JSON(settings)
	})

	api.Put("/alert-settings", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			SMTPHost     string `json:"smtp_host"`
			SMTPPort     int    `json:"smtp_port"`
			SMTPUser     string `json:"smtp_user"`
			SMTPPassword string `json:"smtp_password"`
			SMTPFrom     string `json:"smtp_from"`
			SMTPTLS      bool   `json:"smtp_tls"`
			Enabled      bool   `json:"enabled"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		tls := 0
		if req.SMTPTLS {
			tls = 1
		}
		enabled := 0
		if req.Enabled {
			enabled = 1
		}
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var upsert string
		if db.DB.Dialect == "postgres" {
			upsert = `INSERT INTO alert_settings (id, smtp_host, smtp_port, smtp_user, smtp_password, smtp_from, smtp_tls, enabled, created_at, updated_at, tenant_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET
					smtp_host = EXCLUDED.smtp_host, smtp_port = EXCLUDED.smtp_port, smtp_user = EXCLUDED.smtp_user,
					smtp_password = EXCLUDED.smtp_password, smtp_from = EXCLUDED.smtp_from, smtp_tls = EXCLUDED.smtp_tls,
					enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at`
		} else {
			upsert = `INSERT OR REPLACE INTO alert_settings (id, smtp_host, smtp_port, smtp_user, smtp_password, smtp_from, smtp_tls, enabled, created_at, updated_at, tenant_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		}
		encPassword, err := crypto.Encrypt(req.SMTPPassword)
		if err != nil {
			slog.Warn("failed to encrypt smtp password", "error", err)
			encPassword = req.SMTPPassword
		}
		now := time.Now().Unix()
		// id == tenantID so each tenant has its own row
		_, err = db.DB.Exec(upsert, tenantID, req.SMTPHost, req.SMTPPort, req.SMTPUser, encPassword, req.SMTPFrom, tls, enabled, now, now, tenantID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to save alert settings"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "alert_settings.update", "alert_settings", tenantID, "updated alert settings", c.IP())
		return c.JSON(fiber.Map{"message": "Alert settings saved successfully"})
	})

	api.Get("/alert-rules", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		rows, err := db.DB.Query(`SELECT id, name, event_type, severity, enabled, email_recipients, webhook_url, created_at FROM alert_rules WHERE tenant_id = ? ORDER BY created_at DESC`, tenantID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query alert rules"})
		}
		defer rows.Close()
		type rule struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			EventType       string `json:"event_type"`
			Severity        string `json:"severity"`
			Enabled         bool   `json:"enabled"`
			EmailRecipients string `json:"email_recipients,omitempty"`
			WebhookURL      string `json:"webhook_url,omitempty"`
			CreatedAt       int64  `json:"created_at"`
		}
		rules := []rule{}
		for rows.Next() {
			var r rule
			var enabled int
			if err := rows.Scan(&r.ID, &r.Name, &r.EventType, &r.Severity, &enabled, &r.EmailRecipients, &r.WebhookURL, &r.CreatedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			r.Enabled = enabled == 1
			rules = append(rules, r)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		if rules == nil {
			rules = []rule{}
		}
		return c.JSON(fiber.Map{"rules": rules})
	})

	api.Post("/alert-rules", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Name            string `json:"name"`
			EventType       string `json:"event_type"`
			Severity        string `json:"severity"`
			EmailRecipients string `json:"email_recipients"`
			WebhookURL      string `json:"webhook_url"`
		}
		if err := c.BodyParser(&req); err != nil || req.Name == "" || req.EventType == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Name and event_type are required"})
		}
		if req.Severity == "" {
			req.Severity = "medium"
		}
		ruleID := uuid.New().String()
		now := time.Now().Unix()
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		_, err := db.DB.Exec(`INSERT INTO alert_rules (id, name, event_type, severity, enabled, email_recipients, webhook_url, created_at, updated_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ruleID, req.Name, req.EventType, req.Severity, 1, req.EmailRecipients, req.WebhookURL, now, now, tenantID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create alert rule"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "alert_rule.create", "alert_rule", ruleID, fmt.Sprintf("created alert rule %s", req.Name), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": ruleID, "message": "Alert rule created successfully"})
	})

	// ── Active alert log ────────────────────────────────────────────────
	// Surfaces the persistent incident list shown on /alerts. Open
	// alerts only by default; ?include_resolved=1 returns everything.
	// Tenant-scoped except for super_admin.
	api.Get("/alerts", func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		role, _ := c.Locals("user_role").(string)
		includeResolved := c.Query("include_resolved") == "1"

		where := []string{}
		args := []interface{}{}
		if !auth.IsSuperAdmin(role) {
			where = append(where, "tenant_id = ?")
			args = append(args, tenantID)
		}
		if !includeResolved {
			where = append(where, "resolved = 0")
		}
		clause := ""
		if len(where) > 0 {
			clause = " WHERE " + strings.Join(where, " AND ")
		}
		rows, err := db.DB.Query(`SELECT id, COALESCE(device_id,''), type, severity, message, resolved, COALESCE(resolved_at,0), COALESCE(resolved_by,''), created_at FROM alerts`+clause+` ORDER BY created_at DESC LIMIT 500`, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query alerts"})
		}
		defer rows.Close()
		type alert struct {
			ID         string `json:"id"`
			DeviceID   string `json:"device_id,omitempty"`
			Type       string `json:"type"`
			Severity   string `json:"severity"`
			Message    string `json:"message"`
			Resolved   bool   `json:"resolved"`
			ResolvedAt int64  `json:"resolved_at,omitempty"`
			ResolvedBy string `json:"resolved_by,omitempty"`
			CreatedAt  int64  `json:"created_at"`
		}
		out := []alert{}
		for rows.Next() {
			var a alert
			var resolved int
			if err := rows.Scan(&a.ID, &a.DeviceID, &a.Type, &a.Severity, &a.Message, &resolved, &a.ResolvedAt, &a.ResolvedBy, &a.CreatedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
				continue
			}
			a.Resolved = resolved == 1
			out = append(out, a)
		}
		return c.JSON(fiber.Map{"alerts": out})
	})

	// Resolve / acknowledge an open alert. Idempotent — calling twice
	// keeps the original resolver in place. Admin-only to match the
	// rest of the alert-config surface (alert-rules, alert-settings).
	api.Post("/alerts/:id/resolve", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		alertID := c.Params("id")
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		userID, _ := c.Locals("user_id").(string)
		role, _ := c.Locals("user_role").(string)
		now := time.Now().Unix()
		var res sql.Result
		var err error
		if auth.IsSuperAdmin(role) {
			res, err = db.DB.Exec(`UPDATE alerts SET resolved = 1, resolved_at = ?, resolved_by = ? WHERE id = ? AND resolved = 0`, now, userID, alertID)
		} else {
			res, err = db.DB.Exec(`UPDATE alerts SET resolved = 1, resolved_at = ?, resolved_by = ? WHERE id = ? AND tenant_id = ? AND resolved = 0`, now, userID, alertID, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to resolve alert"})
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Alert not found or already resolved"})
		}
		events.AuditLogTenant(tenantID, userID, "alert.resolve", "alert", alertID, "alert resolved", c.IP())
		return c.JSON(fiber.Map{"message": "Resolved"})
	})

	api.Delete("/alert-rules/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		ruleID := c.Params("id")
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		role, _ := c.Locals("user_role").(string)
		var err error
		if auth.IsSuperAdmin(role) {
			_, err = db.DB.Exec(`DELETE FROM alert_rules WHERE id = ?`, ruleID)
		} else {
			_, err = db.DB.Exec(`DELETE FROM alert_rules WHERE id = ? AND tenant_id = ?`, ruleID, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete alert rule"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "alert_rule.delete", "alert_rule", ruleID, "deleted alert rule", c.IP())
		return c.JSON(fiber.Map{"message": "Alert rule deleted successfully"})
	})
}
