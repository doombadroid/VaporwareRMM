package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"log/slog"
)

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
		err := db.DB.QueryRow(`SELECT smtp_host, smtp_port, smtp_user, smtp_password, smtp_from, smtp_tls, enabled FROM alert_settings WHERE id = 'default'`).Scan(
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
		var upsert string
		if db.DB.Dialect == "postgres" {
			upsert = `INSERT INTO alert_settings (id, smtp_host, smtp_port, smtp_user, smtp_password, smtp_from, smtp_tls, enabled, created_at, updated_at)
				VALUES ('default', ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET
					smtp_host = EXCLUDED.smtp_host, smtp_port = EXCLUDED.smtp_port, smtp_user = EXCLUDED.smtp_user,
					smtp_password = EXCLUDED.smtp_password, smtp_from = EXCLUDED.smtp_from, smtp_tls = EXCLUDED.smtp_tls,
					enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at`
		} else {
			upsert = `INSERT OR REPLACE INTO alert_settings (id, smtp_host, smtp_port, smtp_user, smtp_password, smtp_from, smtp_tls, enabled, created_at, updated_at)
				VALUES ('default', ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		}
		now := time.Now().Unix()
		_, err := db.DB.Exec(upsert, req.SMTPHost, req.SMTPPort, req.SMTPUser, req.SMTPPassword, req.SMTPFrom, tls, enabled, now, now)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to save alert settings"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLog(userID, "alert_settings.update", "alert_settings", "default", "updated alert settings", c.IP())
		return c.JSON(fiber.Map{"message": "Alert settings saved successfully"})
	})

	api.Get("/alert-rules", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		rows, err := db.DB.Query(`SELECT id, name, event_type, severity, enabled, email_recipients, webhook_url, created_at FROM alert_rules ORDER BY created_at DESC`)
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
		_, err := db.DB.Exec(`INSERT INTO alert_rules (id, name, event_type, severity, enabled, email_recipients, webhook_url, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ruleID, req.Name, req.EventType, req.Severity, 1, req.EmailRecipients, req.WebhookURL, now, now)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create alert rule"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLog(userID, "alert_rule.create", "alert_rule", ruleID, fmt.Sprintf("created alert rule %s", req.Name), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": ruleID, "message": "Alert rule created successfully"})
	})

	api.Delete("/alert-rules/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		ruleID := c.Params("id")
		_, err := db.DB.Exec(`DELETE FROM alert_rules WHERE id = ?`, ruleID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete alert rule"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLog(userID, "alert_rule.delete", "alert_rule", ruleID, "deleted alert rule", c.IP())
		return c.JSON(fiber.Map{"message": "Alert rule deleted successfully"})
	})
}
