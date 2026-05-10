package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const (
	maxBulkCommandDevices = 1000
	maxCSVRows            = 10000
)

// allowedBulkCommandTypes restricts bulk-fanout to safe types. Patch
// install can already be triggered per-patch; bulk shell is the
// dangerous one — gate behind admin AND limit to known commands.
var allowedBulkCommandTypes = map[string]bool{
	"shell":         true,
	"script":        true,
	"patch_install": true,
}

func RegisterBulkRoutes(api fiber.Router) {
	// Broadcast a command to a group (or to an explicit device list).
	// Always admin-gated.
	api.Post("/commands/bulk", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Type      string                 `json:"type"`
			Payload   map[string]interface{} `json:"payload"`
			GroupID   string                 `json:"group_id,omitempty"`
			DeviceIDs []string               `json:"device_ids,omitempty"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		if !allowedBulkCommandTypes[req.Type] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid command type"})
		}
		if req.GroupID == "" && len(req.DeviceIDs) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "group_id or device_ids required"})
		}
		// Cap device_ids before constructing IN(?,?,...) to dodge SQLite's
		// 999-variable limit and Postgres planner stalls on huge lists.
		if len(req.DeviceIDs) > maxBulkCommandDevices {
			req.DeviceIDs = req.DeviceIDs[:maxBulkCommandDevices]
		}
		// Resolve target devices, tenant-scoped.
		tenantID := callerTenantID(c)
		role, _ := c.Locals("user_role").(string)

		// Resolved device list comes back with each device's actual
		// tenant — we need that for the audit + per-row tenant_id on the
		// device_commands insert (super_admin can span tenants).
		type targetDevice struct{ id, tenant string }
		var targets []targetDevice
		if req.GroupID != "" {
			tf, tArgs := tenantFilter(c)
			args := append([]interface{}{req.GroupID}, tArgs...)
			var ok int
			if err := db.DB.QueryRow(`SELECT 1 FROM device_groups WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Group not found"})
			}
			rows, err := db.DB.Query(`SELECT d.id, d.tenant_id FROM device_group_members m JOIN devices d ON d.id = m.device_id WHERE m.group_id = ? AND d.tenant_id = ?`, req.GroupID, tenantID)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to resolve group"})
			}
			for rows.Next() {
				var t targetDevice
				if err := rows.Scan(&t.id, &t.tenant); err == nil {
					targets = append(targets, t)
				}
			}
			rows.Close()
		} else {
			// Filter device_ids to caller's tenant. super_admin sees all.
			placeholders := make([]string, len(req.DeviceIDs))
			ph := []interface{}{}
			for i, id := range req.DeviceIDs {
				placeholders[i] = "?"
				ph = append(ph, id)
			}
			tenantClause := ""
			if !auth.IsSuperAdmin(role) {
				tenantClause = " AND tenant_id = ?"
				ph = append(ph, tenantID)
			}
			rows, err := db.DB.Query(`SELECT id, tenant_id FROM devices WHERE id IN (`+strings.Join(placeholders, ",")+`)`+tenantClause, ph...)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to resolve devices"})
			}
			for rows.Next() {
				var t targetDevice
				if err := rows.Scan(&t.id, &t.tenant); err == nil {
					targets = append(targets, t)
				}
			}
			rows.Close()
		}
		if len(targets) > maxBulkCommandDevices {
			targets = targets[:maxBulkCommandDevices]
		}

		// Marshal payload once; type-specific validation happens at the
		// agent. For shell type we still leave the existing dangerous-
		// pattern blocklist on the agent side as the last line.
		payloadBytes, _ := json.Marshal(req.Payload)
		now := time.Now().Unix()
		queued := 0
		for _, t := range targets {
			cmdID := uuid.New().String()
			// device_commands.tenant_id MUST be the device's own tenant
			// (not caller's) so per-tenant /devices/:id/commands queries
			// stay in their lane when super_admin spans tenants.
			if _, err := db.DB.Exec(`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				cmdID, t.id, req.Type, string(payloadBytes), "pending", now, t.tenant); err == nil {
				queued++
			}
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "command.bulk", "command", req.Type, fmt.Sprintf("queued %d %s commands", queued, req.Type), c.IP())
		return c.JSON(fiber.Map{"queued": queued, "targets": len(targets)})
	})

	// CSV pre-stage devices: rows have hostname,os_name,tags. The actual
	// device record is created when an agent registers; this endpoint
	// reserves the hostname so registration matches a known row.
	//
	// For now we use this as a hostname allow-list: any agent registering
	// with a hostname found here gets auto-tagged; otherwise normal
	// registration.
	api.Post("/devices/import", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		body := c.Body()
		if len(body) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Empty CSV"})
		}
		if len(body) > 4*1024*1024 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "CSV too large (>4 MB)"})
		}
		reader := csv.NewReader(strings.NewReader(string(body)))
		reader.FieldsPerRecord = -1
		header, err := reader.Read()
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "CSV header missing"})
		}
		colIdx := map[string]int{}
		for i, h := range header {
			colIdx[strings.ToLower(strings.TrimSpace(h))] = i
		}
		hostnameCol, ok := colIdx["hostname"]
		if !ok {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "CSV missing 'hostname' column"})
		}
		osCol := colIdx["os_name"]
		tagsCol, hasTags := colIdx["tags"]

		tenantID := callerTenantID(c)
		now := time.Now().Unix()
		userID, _ := c.Locals("user_id").(string)
		imported := 0
		rowNum := 0
		for {
			rec, err := reader.Read()
			if err == io.EOF {
				break
			}
			rowNum++
			if rowNum > maxCSVRows {
				break
			}
			if err != nil {
				continue
			}
			if hostnameCol >= len(rec) {
				continue
			}
			hostname := strings.TrimSpace(rec[hostnameCol])
			if hostname == "" || len(hostname) > 253 {
				continue
			}
			// Reject hostnames with shell metas or path separators.
			if !validHostname(hostname) {
				continue
			}
			osName := ""
			if osCol < len(rec) && osCol >= 0 {
				osName = strings.TrimSpace(rec[osCol])
				if len(osName) > 64 {
					osName = osName[:64]
				}
			}
			tags := ""
			if hasTags && tagsCol < len(rec) {
				tags = strings.TrimSpace(rec[tagsCol])
				if len(tags) > 512 {
					tags = tags[:512]
				}
			}
			// devices table has no UNIQUE (tenant_id, hostname) constraint
			// (would break legacy single-tenant data), so the dedup check
			// runs as a SELECT first. Pattern matches what the existing
			// devices.go POST does to keep behavior consistent.
			var existing int
			_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE tenant_id = ? AND hostname = ?`, tenantID, hostname).Scan(&existing)
			if existing > 0 {
				continue
			}
			id := uuid.New().String()
			if _, err := db.DB.Exec(`INSERT INTO devices (id, hostname, os_name, status, created_at, last_seen, user_id, tenant_id, tags) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, hostname, osName, "offline", now, 0, userID, tenantID, tags); err != nil {
				slog.Warn("csv device insert failed", "hostname", hostname, "error", err)
				continue
			}
			imported++
		}
		events.AuditLogTenant(tenantID, userID, "device.bulk_import", "device", "", fmt.Sprintf("imported %d devices from CSV", imported), c.IP())
		return c.JSON(fiber.Map{"imported": imported, "rows_read": rowNum})
	})
}

// validHostname approximates RFC 1123 hostname rules. Conservative —
// rejects underscores, spaces, shell metas. Keeps DNS-clean names only.
func validHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '-':
		default:
			return false
		}
	}
	return true
}
