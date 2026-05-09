package handlers

import (
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
)

func RegisterDashboardRoutes(api fiber.Router, cfg Config) {
	api.Get("/dashboard/overview", func(c *fiber.Ctx) error {
		now := time.Now().Unix()
		staleThreshold := now - int64(cfg.DefaultOfflineThreshold)

		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}

		var total, online int
		if auth.IsSuperAdmin(role) {
			_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&total)
			_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE last_seen > ?`, staleThreshold).Scan(&online)
		} else {
			_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE tenant_id = ?`, tenantID).Scan(&total)
			_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE last_seen > ? AND tenant_id = ?`, staleThreshold, tenantID).Scan(&online)
		}
		offline := total - online
		return c.JSON(fiber.Map{
			"device_stats":     fiber.Map{"total": total, "online": online, "offline": offline, "maintenance": 0},
			"system_health":    fiber.Map{"total_devices": total, "online_devices": online, "offline_devices": offline, "alert_count": 0, "ticket_count": 0, "cpu_usage": 0, "memory_usage": 0, "disk_usage": 0, "network_latency": 0, "uptime_hours": 0},
			"active_alerts":    []fiber.Map{},
			"pending_tickets":  []fiber.Map{},
			"resource_history": []fiber.Map{},
		})
	})
}
