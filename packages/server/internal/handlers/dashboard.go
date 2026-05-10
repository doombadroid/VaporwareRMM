package handlers

import (
	"database/sql"
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
		// scope helpers — super_admin sees everything, tenant admin only their own
		scope := " WHERE tenant_id = ?"
		scopeArg := []interface{}{tenantID}
		if auth.IsSuperAdmin(role) {
			scope = ""
			scopeArg = nil
		}

		args := append([]interface{}{}, scopeArg...)
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices`+scope, args...).Scan(&total)
		args = append([]interface{}{staleThreshold}, scopeArg...)
		onlineWhere := " WHERE last_seen > ?"
		if scope != "" {
			onlineWhere += " AND tenant_id = ?"
		}
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices`+onlineWhere, args...).Scan(&online)
		offline := total - online

		// Open-incident + open-ticket counts for the badge cluster.
		var alertCount, ticketCount int
		alertWhere := " WHERE resolved = 0"
		ticketWhere := " WHERE status NOT IN ('resolved','closed')"
		if scope != "" {
			alertWhere += " AND tenant_id = ?"
			ticketWhere += " AND tenant_id = ?"
		}
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM alerts`+alertWhere, scopeArg...).Scan(&alertCount)
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tickets`+ticketWhere, scopeArg...).Scan(&ticketCount)

		// Latest CPU / memory / disk averaged across online devices.
		// metrics_history is the per-heartbeat snapshot; take the most recent
		// row per device, then average. SUBSELECT keeps it portable.
		var avgCPU, avgMem, avgDisk sql.NullFloat64
		metricArgs := []interface{}{staleThreshold}
		metricWhere := " WHERE m.timestamp > ?"
		if scope != "" {
			metricWhere += " AND d.tenant_id = ?"
			metricArgs = append(metricArgs, tenantID)
		}
		_ = db.DB.QueryRow(`
			SELECT AVG(m.cpu_usage), AVG(m.memory_usage), AVG(m.disk_usage)
			  FROM metrics_history m
			  JOIN devices d ON d.id = m.device_id`+metricWhere, metricArgs...,
		).Scan(&avgCPU, &avgMem, &avgDisk)

		// Active alert + pending ticket previews — small lists for the
		// dashboard cards. Empty arrays when the tenant has none.
		activeAlerts := []map[string]interface{}{}
		alertRows, _ := db.DB.Query(`SELECT id, COALESCE(device_id,''), type, severity, message, created_at FROM alerts`+alertWhere+` ORDER BY created_at DESC LIMIT 10`, scopeArg...)
		if alertRows != nil {
			for alertRows.Next() {
				var id, dev, t, sev, msg string
				var createdAt int64
				if err := alertRows.Scan(&id, &dev, &t, &sev, &msg, &createdAt); err == nil {
					activeAlerts = append(activeAlerts, map[string]interface{}{
						"id": id, "device_id": dev, "type": t, "severity": sev, "message": msg, "created_at": createdAt,
					})
				}
			}
			alertRows.Close()
		}

		pendingTickets := []map[string]interface{}{}
		tArgs := append([]interface{}{}, scopeArg...)
		ticketRows, _ := db.DB.Query(`SELECT id, title, status, priority, created_at FROM tickets`+ticketWhere+` ORDER BY created_at DESC LIMIT 10`, tArgs...)
		if ticketRows != nil {
			for ticketRows.Next() {
				var id, title, status, priority string
				var createdAt int64
				if err := ticketRows.Scan(&id, &title, &status, &priority, &createdAt); err == nil {
					pendingTickets = append(pendingTickets, map[string]interface{}{
						"id": id, "title": title, "status": status, "priority": priority, "created_at": createdAt,
					})
				}
			}
			ticketRows.Close()
		}

		return c.JSON(fiber.Map{
			"device_stats": fiber.Map{"total": total, "online": online, "offline": offline, "maintenance": 0},
			"system_health": fiber.Map{
				"total_devices":   total,
				"online_devices":  online,
				"offline_devices": offline,
				"alert_count":     alertCount,
				"ticket_count":    ticketCount,
				"cpu_usage":       roundPct(avgCPU),
				"memory_usage":    roundPct(avgMem),
				"disk_usage":      roundPct(avgDisk),
				"network_latency": 0,
				"uptime_hours":    0,
			},
			"active_alerts":    activeAlerts,
			"pending_tickets":  pendingTickets,
			"resource_history": []fiber.Map{},
		})
	})
}

// roundPct turns a possibly-NULL avg into a 0-100 number with one decimal.
func roundPct(v sql.NullFloat64) float64 {
	if !v.Valid {
		return 0
	}
	r := v.Float64
	if r < 0 {
		r = 0
	}
	if r > 100 {
		r = 100
	}
	return float64(int(r*10)) / 10
}
