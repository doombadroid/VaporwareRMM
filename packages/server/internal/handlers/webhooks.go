package handlers

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
)

func RegisterWebhookRoutes(api fiber.Router) {
	api.Get("/webhooks", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		rows, err := db.DB.Query(`SELECT id, url, secret, events, enabled, created_at FROM webhooks WHERE tenant_id = ? ORDER BY created_at DESC`, tenantID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query webhooks"})
		}
		defer rows.Close()
		type webhook struct {
			ID        string `json:"id"`
			URL       string `json:"url"`
			Secret    string `json:"secret,omitempty"`
			Events    string `json:"events"`
			Enabled   bool   `json:"enabled"`
			CreatedAt int64  `json:"created_at"`
		}
		hooks := []webhook{}
		for rows.Next() {
			var h webhook
			var enabled int
			if err := rows.Scan(&h.ID, &h.URL, &h.Secret, &h.Events, &enabled, &h.CreatedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			h.Enabled = enabled == 1
			hooks = append(hooks, h)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		for i := range hooks {
			if hooks[i].Secret != "" {
				hooks[i].Secret = "********"
			}
		}
		return c.JSON(fiber.Map{"webhooks": hooks})
	})

	api.Post("/webhooks", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			URL     string `json:"url"`
			Secret  string `json:"secret"`
			Events  string `json:"events"`
			Enabled bool   `json:"enabled"`
		}
		if err := c.BodyParser(&req); err != nil || req.URL == "" || req.Events == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "URL and events are required"})
		}
		id := uuid.New().String()
		enabled := 0
		if req.Enabled {
			enabled = 1
		}
		encSecret, err := crypto.Encrypt(req.Secret)
		if err != nil {
			slog.Warn("failed to encrypt webhook secret", "error", err)
			encSecret = req.Secret
		}
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		_, err = db.DB.Exec(`INSERT INTO webhooks (id, url, secret, events, enabled, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, req.URL, encSecret, req.Events, enabled, time.Now().Unix(), tenantID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create webhook"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "webhook.create", "webhook", id, fmt.Sprintf("created webhook %s", req.URL), c.IP())
		return c.JSON(fiber.Map{"id": id, "message": "Webhook created"})
	})

	api.Delete("/webhooks/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		role, _ := c.Locals("user_role").(string)
		var err error
		if auth.IsSuperAdmin(role) {
			_, err = db.DB.Exec(`DELETE FROM webhooks WHERE id = ?`, id)
		} else {
			_, err = db.DB.Exec(`DELETE FROM webhooks WHERE id = ? AND tenant_id = ?`, id, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete webhook"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "webhook.delete", "webhook", id, "deleted webhook", c.IP())
		return c.JSON(fiber.Map{"message": "Webhook deleted"})
	})
}
