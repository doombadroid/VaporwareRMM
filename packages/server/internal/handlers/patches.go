package handlers

import (
	"database/sql"
	"log/slog"
	"strings"

	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
)

// allowedPatchStatuses gates the ?status= filter on GET. Includes the
// pseudo-value "all" which means "no status WHERE clause".
var allowedPatchStatuses = map[string]bool{
	"pending":   true,
	"installed": true,
	"failed":    true,
	"all":       true,
}

// writeablePatchStatuses gates PUT /patches/:id payloads so untrusted
// callers can't write a junk status that would later be invisible to the
// status-filtered GET (and break dashboards that bucket on status).
// Excludes "all".
var writeablePatchStatuses = map[string]bool{
	"pending":   true,
	"installed": true,
	"failed":    true,
}

func RegisterPatchRoutes(api fiber.Router) {
	// Fleet-wide patch list. Tenant-scoped (super_admin sees all).
	// LEFT JOIN devices so a stale device_id (device deleted, patch row
	// orphaned by tenant data export) still renders without crashing.
	api.Get("/patches", func(c *fiber.Ctx) error {
		statusFilter := c.Query("status", "pending")
		if !allowedPatchStatuses[statusFilter] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid status"})
		}

		tf, tArgs := tenantFilter(c)
		where := []string{}
		args := []interface{}{}
		if tf != "" {
			// tenantFilter returns " AND tenant_id = ?" — strip the leading AND.
			where = append(where, "p.tenant_id = ?")
			args = append(args, tArgs...)
		}
		if statusFilter != "all" {
			where = append(where, "p.status = ?")
			args = append(args, statusFilter)
		}
		clause := ""
		if len(where) > 0 {
			clause = " WHERE " + strings.Join(where, " AND ")
		}

		// Tenant match in the JOIN prevents a forged or stale patch.device_id
		// pointing into another tenant's devices table from leaking that
		// tenant's hostname. POST /devices/:id/patches already validates the
		// parent device tenant, so this is defense-in-depth.
		rows, err := db.DB.Query(`
			SELECT p.id, p.device_id, COALESCE(d.hostname,''), p.title, COALESCE(p.description,''), p.severity, p.status, p.installed_at, p.created_at
			  FROM patches p
			  LEFT JOIN devices d ON d.id = p.device_id AND d.tenant_id = p.tenant_id`+clause+`
			  ORDER BY p.created_at DESC LIMIT 1000`, args...)
		if err != nil {
			slog.Warn("patches list query failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query patches"})
		}
		defer rows.Close()

		type patch struct {
			ID          string `json:"id"`
			DeviceID    string `json:"device_id"`
			DeviceName  string `json:"device_name,omitempty"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Severity    string `json:"severity"`
			Status      string `json:"status"`
			InstalledAt *int64 `json:"installed_at,omitempty"`
			CreatedAt   int64  `json:"created_at"`
		}
		patches := []patch{}
		for rows.Next() {
			var p patch
			var installedAt sql.NullInt64
			if err := rows.Scan(&p.ID, &p.DeviceID, &p.DeviceName, &p.Title, &p.Description, &p.Severity, &p.Status, &installedAt, &p.CreatedAt); err != nil {
				slog.Warn("patches scan failed", "error", err)
				continue
			}
			if installedAt.Valid {
				p.InstalledAt = &installedAt.Int64
			}
			patches = append(patches, p)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("patches iteration error", "error", err)
		}
		return c.JSON(fiber.Map{"patches": patches})
	})
}
