package handlers

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"log/slog"
)

func RegisterComplianceRoutes(api fiber.Router, cfg Config) {
	api.Get("/compliance/scan", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		now := time.Now().Unix()
		results := []fiber.Map{}
		threshold := now - int64(cfg.DefaultOfflineThreshold)
		offlineRows, err := db.DB.Query(`SELECT id, hostname, last_seen FROM devices WHERE last_seen < ? AND status != 'offline'`, threshold)
		if err == nil {
			for offlineRows.Next() {
				var id, hostname string
				var lastSeen int64
				if err := offlineRows.Scan(&id, &hostname, &lastSeen); err != nil {
					continue
				}
				results = append(results, fiber.Map{"device_id": id, "hostname": hostname, "check": "device_heartbeat", "status": "fail", "details": fmt.Sprintf("No heartbeat for %d seconds", now-lastSeen), "severity": "high"})
			}
			if err := offlineRows.Err(); err != nil {
				slog.Warn("rows iteration error", "error", err)
			}
			offlineRows.Close()
		}
		agentRows, err := db.DB.Query(`SELECT id, hostname, agent_version FROM devices WHERE agent_version != '' AND agent_version != '1.1.0'`)
		if err == nil {
			for agentRows.Next() {
				var id, hostname, version string
				if err := agentRows.Scan(&id, &hostname, &version); err != nil {
					continue
				}
				results = append(results, fiber.Map{"device_id": id, "hostname": hostname, "check": "agent_version", "status": "fail", "details": fmt.Sprintf("Agent version %s is outdated (latest: 1.1.0)", version), "severity": "medium"})
			}
			if err := agentRows.Err(); err != nil {
				slog.Warn("rows iteration error", "error", err)
			}
			agentRows.Close()
		}
		patchRows, err := db.DB.Query(`SELECT device_id, title, severity FROM patches WHERE status = 'pending'`)
		if err == nil {
			for patchRows.Next() {
				var deviceID, title, severity string
				if err := patchRows.Scan(&deviceID, &title, &severity); err != nil {
					continue
				}
				var hostname string
				if err := db.DB.QueryRow(`SELECT hostname FROM devices WHERE id = ?`, deviceID).Scan(&hostname); err != nil {
					slog.Warn("db query row scan failed", "error", err)
				}
				results = append(results, fiber.Map{"device_id": deviceID, "hostname": hostname, "check": "pending_patches", "status": "fail", "details": fmt.Sprintf("Pending patch: %s", title), "severity": severity})
			}
			if err := patchRows.Err(); err != nil {
				slog.Warn("rows iteration error", "error", err)
			}
			patchRows.Close()
		}
		var adminCount int
		if err := db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE email = 'admin@vaporrmm.local' AND last_login > 0`).Scan(&adminCount); err != nil {
			slog.Warn("db query row scan failed", "error", err)
		}
		if adminCount > 0 {
			results = append(results, fiber.Map{"device_id": "", "hostname": "server", "check": "default_admin", "status": "warning", "details": "Default admin account is still in use. Consider creating a dedicated admin user.", "severity": "medium"})
		}
		for _, r := range results {
			resultID := uuid.New().String()
			deviceID, _ := r["device_id"].(string)
			checkType, _ := r["check"].(string)
			status, _ := r["status"].(string)
			details, _ := r["details"].(string)
			severity, _ := r["severity"].(string)
			if _, err := db.DB.Exec(`INSERT INTO compliance_results (id, device_id, check_type, status, details, severity, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				resultID, deviceID, checkType, status, details, severity, now); err != nil {
				slog.Warn("db exec failed", "error", err)
			}
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLog(userID, "compliance.scan", "compliance", "", fmt.Sprintf("scanned %d issues", len(results)), c.IP())
		return c.JSON(fiber.Map{"scanned_at": now, "issues": len(results), "results": results})
	})

	api.Get("/compliance/results", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		limit := 100
		if l := c.Query("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		if limit > cfg.MaxAuditLimit {
			limit = cfg.MaxAuditLimit
		}
		rows, err := db.DB.Query(`SELECT id, device_id, check_type, status, details, severity, created_at FROM compliance_results ORDER BY created_at DESC LIMIT ?`, limit)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query compliance results"})
		}
		defer rows.Close()
		type result struct {
			ID        string `json:"id"`
			DeviceID  string `json:"device_id,omitempty"`
			CheckType string `json:"check_type"`
			Status    string `json:"status"`
			Details   string `json:"details"`
			Severity  string `json:"severity"`
			CreatedAt int64  `json:"created_at"`
		}
		results := []result{}
		for rows.Next() {
			var r result
			if err := rows.Scan(&r.ID, &r.DeviceID, &r.CheckType, &r.Status, &r.Details, &r.Severity, &r.CreatedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			results = append(results, r)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		if results == nil {
			results = []result{}
		}
		return c.JSON(fiber.Map{"results": results})
	})
}
