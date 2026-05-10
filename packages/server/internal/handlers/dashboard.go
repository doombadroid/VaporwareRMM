package handlers

import (
	"database/sql"
	"log/slog"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
)

// serverStartTime is captured at process boot so /dashboard/overview can
// report uptime_hours without persistence. Reset on every restart.
var serverStartTime = time.Now()

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

		// Latest CPU / memory / disk averaged across recently-active devices.
		// Subquery picks the newest metrics_history row per device (rn=1)
		// then averages those, so a noisy heartbeater doesn't outweigh a
		// quiet one. ROW_NUMBER works in Postgres and SQLite ≥3.25.
		var avgCPU, avgMem, avgDisk sql.NullFloat64
		metricArgs := []interface{}{staleThreshold}
		metricWhere := " WHERE recorded_at > ?"
		if scope != "" {
			metricWhere += " AND tenant_id = ?"
			metricArgs = append(metricArgs, tenantID)
		}
		if err := db.DB.QueryRow(`
			SELECT AVG(cpu_usage), AVG(memory_usage), AVG(disk_usage) FROM (
				SELECT cpu_usage, memory_usage, disk_usage,
					ROW_NUMBER() OVER (PARTITION BY device_id ORDER BY recorded_at DESC) AS rn
				FROM metrics_history`+metricWhere+`
			) latest WHERE rn = 1`, metricArgs...,
		).Scan(&avgCPU, &avgMem, &avgDisk); err != nil && err != sql.ErrNoRows {
			slog.Warn("dashboard metrics avg query failed", "error", err)
		}

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

		// 24h resource history. Hour-bucketed averages across all metrics_history
		// rows in the tenant (or fleet for super_admin). Returns up to 24
		// points; missing buckets are simply absent (chart will gap-fill).
		resourceHistory := buildResourceHistory(now, scope != "", tenantID)

		// Recent activity feed — top 10 audit_logs for the tenant. Heavily
		// redacted: action + resource_type + timestamp only. We deliberately
		// drop user_id, ip_address, free-text details, and resource_id —
		// resource_id would leak UUIDs of resources the viewer may not
		// have direct access to (e.g. user.create on an admin user a
		// regular tenant member cannot enumerate via /admin). Full audit
		// log stays admin-only at /audit-logs.
		recentActivity := []map[string]interface{}{}
		actRows, _ := db.DB.Query(`SELECT action, resource_type, created_at FROM audit_logs`+scope+` ORDER BY created_at DESC LIMIT 10`, scopeArg...)
		if actRows != nil {
			for actRows.Next() {
				var action, rtype string
				var createdAt int64
				if err := actRows.Scan(&action, &rtype, &createdAt); err == nil {
					recentActivity = append(recentActivity, map[string]interface{}{
						"action": action, "resource_type": rtype, "created_at": createdAt,
					})
				}
			}
			actRows.Close()
		}

		// SLA card metrics. Real numbers from the tickets table — no
		// CSAT / response_time targets configured yet, so fields are scoped
		// to what we can actually measure.
		sla := buildSLA(now, scope != "", tenantID, total, online)

		uptimeHours := int(time.Since(serverStartTime).Hours())

		return c.JSON(fiber.Map{
			"device_stats": fiber.Map{"total": total, "online": online, "offline": offline},
			"system_health": fiber.Map{
				"total_devices":   total,
				"online_devices":  online,
				"offline_devices": offline,
				"alert_count":     alertCount,
				"ticket_count":    ticketCount,
				"cpu_usage":       roundPct(avgCPU),
				"memory_usage":    roundPct(avgMem),
				"disk_usage":      roundPct(avgDisk),
				"uptime_hours":    uptimeHours,
			},
			"active_alerts":    activeAlerts,
			"pending_tickets":  pendingTickets,
			"resource_history": resourceHistory,
			"recent_activity":  recentActivity,
			"sla":              sla,
		})
	})
}

// buildResourceHistory returns a 24-point hourly average of CPU/mem/disk
// for the requested scope. Buckets are computed in SQL for efficiency
// (one query, not 24). Missing hours are simply absent — recharts handles
// the gaps.
func buildResourceHistory(now int64, scoped bool, tenantID string) []map[string]interface{} {
	windowStart := now - 24*3600
	args := []interface{}{windowStart}
	where := " WHERE recorded_at > ?"
	if scoped {
		where += " AND tenant_id = ?"
		args = append(args, tenantID)
	}
	// Bucket by hour: floor(recorded_at / 3600). AVG is fine even on the
	// noisy heartbeats — it's a fleet-level smoothed view, not per-device.
	rows, err := db.DB.Query(`
		SELECT (recorded_at / 3600) * 3600 AS bucket,
			AVG(cpu_usage), AVG(memory_usage), AVG(disk_usage)
		FROM metrics_history`+where+`
		GROUP BY bucket
		ORDER BY bucket ASC
		LIMIT 24`, args...)
	if err != nil {
		slog.Warn("resource_history query failed", "error", err)
		return []map[string]interface{}{}
	}
	defer rows.Close()
	out := []map[string]interface{}{}
	for rows.Next() {
		var bucket int64
		var cpu, mem, disk sql.NullFloat64
		if err := rows.Scan(&bucket, &cpu, &mem, &disk); err != nil {
			slog.Warn("resource_history scan failed", "error", err)
			continue
		}
		out = append(out, map[string]interface{}{
			"time":   time.Unix(bucket, 0).UTC().Format("15:04"),
			"cpu":    roundPct(cpu),
			"memory": roundPct(mem),
			"disk":   roundPct(disk),
		})
	}
	return out
}

// buildSLA computes the four numbers shown on the SLA card from real
// ticket/device data. All percentages are clamped to [0,100]. resolved_30d
// counts tickets that hit a terminal status in the last 30 days; the
// resolution-rate denominator is created tickets in the same window.
// avg_response_minutes is the mean (updated_at - created_at) for resolved
// tickets in the window — a rough first-touch proxy until ticket
// comments / first-update tracking lands.
func buildSLA(now int64, scoped bool, tenantID string, totalDevices, onlineDevices int) map[string]interface{} {
	windowStart := now - 30*24*3600

	scopeArg := []interface{}{}
	scopeAnd := ""
	if scoped {
		scopeAnd = " AND tenant_id = ?"
		scopeArg = []interface{}{tenantID}
	}

	var createdCount, resolvedCount int
	createdArgs := append([]interface{}{windowStart}, scopeArg...)
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tickets WHERE created_at > ?`+scopeAnd, createdArgs...).Scan(&createdCount)
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tickets WHERE created_at > ? AND status IN ('resolved','closed')`+scopeAnd, createdArgs...).Scan(&resolvedCount)

	// First-response = (earliest non-internal ticket_comment) - tickets.created_at.
	// Coalesce with the legacy proxy (updated_at - created_at) when the
	// ticket was resolved without a customer-visible comment, so older
	// tickets predating the comments table still contribute to the average.
	tenantClause := ""
	if scoped {
		tenantClause = " AND t.tenant_id = ?"
	}
	var avgResponseSec sql.NullFloat64
	_ = db.DB.QueryRow(`
		SELECT AVG(response_seconds) FROM (
			SELECT t.id,
				COALESCE(
					(SELECT MIN(c.created_at) FROM ticket_comments c
						WHERE c.ticket_id = t.id AND c.internal = 0 AND c.created_at > t.created_at),
					t.updated_at
				) - t.created_at AS response_seconds
			FROM tickets t
			WHERE t.created_at > ? AND t.status IN ('resolved','closed') AND t.updated_at > t.created_at`+tenantClause+`
		) firstResponse WHERE response_seconds > 0`, createdArgs...).Scan(&avgResponseSec)

	resolutionRate := 0.0
	if createdCount > 0 {
		resolutionRate = float64(resolvedCount) / float64(createdCount) * 100.0
		if resolutionRate > 100 {
			resolutionRate = 100
		}
	}
	uptimePct := 0.0
	if totalDevices > 0 {
		uptimePct = float64(onlineDevices) / float64(totalDevices) * 100.0
	}
	avgMinutes := 0.0
	if avgResponseSec.Valid {
		avgMinutes = avgResponseSec.Float64 / 60.0
		if avgMinutes < 0 {
			avgMinutes = 0
		}
	}
	return map[string]interface{}{
		"window_days":          30,
		"online_pct":           clampPct(uptimePct),
		"resolution_rate_pct":  clampPct(resolutionRate),
		"resolved_count":       resolvedCount,
		"created_count":        createdCount,
		"avg_response_minutes": round1(avgMinutes),
	}
}

// roundPct turns a possibly-NULL avg into a 0-100 number with one decimal.
// Used for CPU / memory / disk percentages.
func roundPct(v sql.NullFloat64) float64 {
	if !v.Valid {
		return 0
	}
	return clampPct(v.Float64)
}

// clampPct clamps r to [0, 100] and rounds to one decimal.
func clampPct(r float64) float64 {
	if r < 0 {
		r = 0
	}
	if r > 100 {
		r = 100
	}
	return round1(r)
}

// round1 rounds to one decimal without clamping. Use for unbounded values
// like avg_response_minutes.
func round1(r float64) float64 {
	if r < 0 {
		r = 0
	}
	return float64(int(r*10)) / 10
}
