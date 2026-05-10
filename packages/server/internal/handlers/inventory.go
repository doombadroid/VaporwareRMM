package handlers

import (
	"database/sql"
	"fmt"
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
	// Hard cap on per-device software entries. A typical workstation has
	// 200-2000 packages; 5000 covers heavy dev rigs while bounding storage.
	// Agent already truncates; server re-enforces.
	maxSoftwareEntries = 5000
	maxSoftwareName    = 256
	maxSoftwareVersion = 64
	maxSoftwareVendor  = 256
)

type inventorySoftware struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Vendor      string `json:"vendor,omitempty"`
	InstallDate int64  `json:"install_date,omitempty"`
}

type inventoryHardware struct {
	CPUModel        string `json:"cpu_model,omitempty"`
	CPUCores        int    `json:"cpu_cores,omitempty"`
	RAMBytes        int64  `json:"ram_bytes,omitempty"`
	DiskTotalBytes  int64  `json:"disk_total_bytes,omitempty"`
	Platform        string `json:"platform,omitempty"`
	PlatformVersion string `json:"platform_version,omitempty"`
	KernelVersion   string `json:"kernel_version,omitempty"`
}

// truncateString limits string length and strips control chars to avoid
// storing megabytes of crud or NUL/CR/LF that breaks display from a
// malicious or buggy agent.
func truncateString(s string, max int) string {
	if s == "" {
		return s
	}
	// Strip ASCII control characters (0x00-0x1F + 0x7F) except tab; keep
	// printable plus tab so package descriptions stay readable.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || (r >= 0x20 && r != 0x7F) {
			b.WriteRune(r)
		}
	}
	cleaned := b.String()
	if len(cleaned) <= max {
		return cleaned
	}
	return cleaned[:max]
}

// RegisterInventoryRoutes wires the agent-write and user-read sides of
// the device inventory. Agent posts on a schedule (~every 6h); server
// rebuilds the per-device row set atomically (delete-then-insert in tx
// is the simplest portable approach).
func RegisterInventoryRoutes(app *fiber.App, api fiber.Router) {
	// Agent endpoint — agent token auth on the route directly to mirror
	// other /agent/* routes (see RegisterAgentRoutes).
	//
	// Security: AgentAuthMiddleware binds the request to the device the
	// token was issued to. We REJECT a URL :id that doesn't match — without
	// this an agent could overwrite any other device's inventory.
	app.Post("/agent/inventory/:id", auth.AgentAuthMiddleware(), func(c *fiber.Ctx) error {
		urlDeviceID := c.Params("id")
		boundDeviceID, _ := c.Locals("device_id").(string)
		if urlDeviceID == "" || boundDeviceID == "" || urlDeviceID != boundDeviceID {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "device id mismatch"})
		}
		deviceID := boundDeviceID
		var req struct {
			Software []inventorySoftware `json:"software"`
			Hardware *inventoryHardware  `json:"hardware,omitempty"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		// Resolve tenant via the device — agent token middleware doesn't
		// set tenant_id; we look it up.
		var tenantID string
		if err := db.DB.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM devices WHERE id = ?`, deviceID).Scan(&tenantID); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		// Cap entry count — drop the tail rather than 4xx. A bad agent
		// shouldn't lose its hardware update because its software list
		// overflowed.
		software := req.Software
		if len(software) > maxSoftwareEntries {
			software = software[:maxSoftwareEntries]
		}

		now := time.Now().Unix()

		// Software: replace-all in a single tx so we never serve a
		// half-rebuilt list. Tx wraps DELETE + bulk INSERT.
		tx, err := db.DB.Begin()
		if err != nil {
			slog.Warn("inventory tx begin failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to begin tx"})
		}
		defer tx.Rollback()

		// tx.Exec doesn't pass through Wrapper.q — must rewrite placeholders
		// via db.DB.Q for Postgres. SQLite is a no-op.
		if _, err := tx.Exec(db.DB.Q(`DELETE FROM device_software WHERE device_id = ?`), deviceID); err != nil {
			slog.Warn("software delete failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to clear software"})
		}
		insertSoftware := db.DB.Q(`INSERT INTO device_software (id, device_id, tenant_id, name, version, vendor, install_date, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
		for _, s := range software {
			if s.Name == "" {
				continue
			}
			id := uuid.New().String()
			_, err := tx.Exec(insertSoftware,
				id, deviceID, tenantID,
				truncateString(s.Name, maxSoftwareName),
				truncateString(s.Version, maxSoftwareVersion),
				truncateString(s.Vendor, maxSoftwareVendor),
				s.InstallDate, now)
			if err != nil {
				slog.Warn("software insert failed", "error", err)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to write software"})
			}
		}

		if req.Hardware != nil {
			h := req.Hardware
			// Single-row replace via DELETE + INSERT keeps it portable
			// (SQLite + Postgres). UPSERT would require dialect-specific
			// syntax through the wrapper.
			if _, err := tx.Exec(db.DB.Q(`DELETE FROM device_hardware WHERE device_id = ?`), deviceID); err != nil {
				slog.Warn("hardware delete failed", "error", err)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to clear hardware"})
			}
			if _, err := tx.Exec(db.DB.Q(`INSERT INTO device_hardware (device_id, tenant_id, cpu_model, cpu_cores, ram_bytes, disk_total_bytes, platform, platform_version, kernel_version, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
				deviceID, tenantID,
				truncateString(h.CPUModel, maxSoftwareName),
				h.CPUCores,
				h.RAMBytes,
				h.DiskTotalBytes,
				truncateString(h.Platform, 64),
				truncateString(h.PlatformVersion, 64),
				truncateString(h.KernelVersion, 128),
				now,
			); err != nil {
				slog.Warn("hardware insert failed", "error", err)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to write hardware"})
			}
		}

		if err := tx.Commit(); err != nil {
			slog.Warn("inventory tx commit failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Commit failed"})
		}
		return c.JSON(fiber.Map{"message": "Inventory updated", "software_count": len(software)})
	})

	// User endpoints — under main /api/v1 group, auth/CSRF inherited.
	api.Get("/devices/:id/software", func(c *fiber.Ctx) error {
		deviceID := c.Params("id")
		tf, tArgs := tenantFilter(c)
		// Verify ticket belongs to caller's tenant via devices row.
		args := append([]interface{}{deviceID}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM devices WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		rows, err := db.DB.Query(`SELECT name, COALESCE(version,''), COALESCE(vendor,''), COALESCE(install_date,0), updated_at FROM device_software WHERE device_id = ? ORDER BY name ASC LIMIT 5000`, deviceID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query software"})
		}
		defer rows.Close()
		type entry struct {
			Name        string `json:"name"`
			Version     string `json:"version,omitempty"`
			Vendor      string `json:"vendor,omitempty"`
			InstallDate int64  `json:"install_date,omitempty"`
			UpdatedAt   int64  `json:"updated_at"`
		}
		out := []entry{}
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.Name, &e.Version, &e.Vendor, &e.InstallDate, &e.UpdatedAt); err != nil {
				continue
			}
			out = append(out, e)
		}
		return c.JSON(fiber.Map{"software": out})
	})

	api.Get("/devices/:id/hardware", func(c *fiber.Ctx) error {
		deviceID := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{deviceID}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM devices WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		var h inventoryHardware
		var updatedAt sql.NullInt64
		err := db.DB.QueryRow(`SELECT COALESCE(cpu_model,''), COALESCE(cpu_cores,0), COALESCE(ram_bytes,0), COALESCE(disk_total_bytes,0), COALESCE(platform,''), COALESCE(platform_version,''), COALESCE(kernel_version,''), updated_at FROM device_hardware WHERE device_id = ?`, deviceID).
			Scan(&h.CPUModel, &h.CPUCores, &h.RAMBytes, &h.DiskTotalBytes, &h.Platform, &h.PlatformVersion, &h.KernelVersion, &updatedAt)
		if err == sql.ErrNoRows {
			return c.JSON(fiber.Map{"hardware": nil})
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query hardware"})
		}
		return c.JSON(fiber.Map{"hardware": h, "updated_at": updatedAt.Int64})
	})

	// Fleet-wide aggregation: how many devices have each software name.
	// Useful for "everyone with Chrome <120" style searches.
	api.Get("/software", func(c *fiber.Ctx) error {
		nameFilter := c.Query("name")
		if len(nameFilter) > maxSoftwareName {
			nameFilter = nameFilter[:maxSoftwareName]
		}
		tenantID, _ := c.Locals("tenant_id").(string)
		role, _ := c.Locals("user_role").(string)
		if tenantID == "" {
			tenantID = "default"
		}

		where := []string{}
		args := []interface{}{}
		if !auth.IsSuperAdmin(role) {
			where = append(where, "tenant_id = ?")
			args = append(args, tenantID)
		}
		if nameFilter != "" {
			where = append(where, "name LIKE ?")
			args = append(args, "%"+nameFilter+"%")
		}
		clause := ""
		if len(where) > 0 {
			clause = " WHERE " + strings.Join(where, " AND ")
		}
		rows, err := db.DB.Query(`
			SELECT name, COUNT(DISTINCT device_id) AS device_count
			  FROM device_software`+clause+`
			  GROUP BY name
			  ORDER BY device_count DESC, name ASC
			  LIMIT 500`, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query software"})
		}
		defer rows.Close()
		type row struct {
			Name        string `json:"name"`
			DeviceCount int    `json:"device_count"`
		}
		out := []row{}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.Name, &r.DeviceCount); err != nil {
				continue
			}
			out = append(out, r)
		}
		return c.JSON(fiber.Map{"software": out})
	})

	// Device groups — flat hierarchy. Admin gates create/update/delete.
	api.Get("/device-groups", func(c *fiber.Ctx) error {
		tf, tArgs := tenantFilter(c)
		clause := ""
		args := []interface{}{}
		if tf != "" {
			clause = " WHERE tenant_id = ?"
			args = append(args, tArgs...)
		}
		rows, err := db.DB.Query(`SELECT id, name, COALESCE(description,''), created_at FROM device_groups`+clause+` ORDER BY name ASC LIMIT 500`, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query groups"})
		}
		defer rows.Close()
		type group struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			CreatedAt   int64  `json:"created_at"`
		}
		groups := []group{}
		for rows.Next() {
			var g group
			if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
				continue
			}
			groups = append(groups, g)
		}
		return c.JSON(fiber.Map{"groups": groups})
	})

	api.Post("/device-groups", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := c.BodyParser(&req); err != nil || req.Name == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Name required"})
		}
		if len(req.Name) > 128 {
			req.Name = req.Name[:128]
		}
		if len(req.Description) > 512 {
			req.Description = req.Description[:512]
		}
		id := uuid.New().String()
		tenantID := callerTenantID(c)
		_, err := db.DB.Exec(`INSERT INTO device_groups (id, tenant_id, name, description, created_at) VALUES (?, ?, ?, ?, ?)`,
			id, tenantID, req.Name, req.Description, time.Now().Unix())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create group"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "device_group.create", "device_group", id, fmt.Sprintf("created group %s", req.Name), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
	})

	api.Delete("/device-groups/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		// Verify ownership before delete; same pattern as patches.
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM device_groups WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Group not found"})
		}
		if _, err := db.DB.Exec(`DELETE FROM device_group_members WHERE group_id = ?`, id); err != nil {
			slog.Warn("group members delete failed", "error", err)
		}
		if _, err := db.DB.Exec(`DELETE FROM device_groups WHERE id = ?`, id); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete group"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "device_group.delete", "device_group", id, "deleted group", c.IP())
		return c.JSON(fiber.Map{"message": "Group deleted"})
	})

	api.Get("/device-groups/:id/members", func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM device_groups WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Group not found"})
		}
		// JOIN tenant-pinned to the group's tenant so a stray member row
		// referencing a device that since changed tenants doesn't leak its
		// hostname. The group itself is already verified above.
		rows, err := db.DB.Query(`
			SELECT d.id, d.hostname, COALESCE(d.status,''), COALESCE(d.last_seen,0)
			  FROM device_group_members m
			  JOIN devices d ON d.id = m.device_id
			  JOIN device_groups g ON g.id = m.group_id AND g.tenant_id = d.tenant_id
			  WHERE m.group_id = ?
			  ORDER BY d.hostname ASC LIMIT 1000`, id)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query members"})
		}
		defer rows.Close()
		type member struct {
			ID       string `json:"id"`
			Hostname string `json:"hostname"`
			Status   string `json:"status"`
			LastSeen int64  `json:"last_seen"`
		}
		members := []member{}
		for rows.Next() {
			var m member
			if err := rows.Scan(&m.ID, &m.Hostname, &m.Status, &m.LastSeen); err != nil {
				continue
			}
			members = append(members, m)
		}
		return c.JSON(fiber.Map{"members": members})
	})

	api.Post("/device-groups/:id/members", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		groupID := c.Params("id")
		var req struct {
			DeviceIDs []string `json:"device_ids"`
		}
		if err := c.BodyParser(&req); err != nil || len(req.DeviceIDs) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "device_ids required"})
		}
		if len(req.DeviceIDs) > 1000 {
			req.DeviceIDs = req.DeviceIDs[:1000]
		}
		tf, tArgs := tenantFilter(c)
		// Verify group ownership.
		args := append([]interface{}{groupID}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM device_groups WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Group not found"})
		}
		// Each device must belong to caller's tenant. Filter by tenant before
		// insert; ignore IDs that don't match (silently skip).
		placeholders := make([]string, len(req.DeviceIDs))
		for i := range req.DeviceIDs {
			placeholders[i] = "?"
		}
		validArgs := make([]interface{}, 0, len(req.DeviceIDs)+len(tArgs))
		for _, id := range req.DeviceIDs {
			validArgs = append(validArgs, id)
		}
		validArgs = append(validArgs, tArgs...)
		validRows, err := db.DB.Query(`SELECT id FROM devices WHERE id IN (`+strings.Join(placeholders, ",")+`)`+tf, validArgs...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to validate devices"})
		}
		valid := []string{}
		for validRows.Next() {
			var id string
			if err := validRows.Scan(&id); err == nil {
				valid = append(valid, id)
			}
		}
		validRows.Close()
		// Bulk insert (ignore duplicates via INSERT OR IGNORE on SQLite,
		// ON CONFLICT DO NOTHING on Postgres — dialect split).
		var stmt string
		if db.DB.Dialect == "postgres" {
			stmt = `INSERT INTO device_group_members (group_id, device_id) VALUES (?, ?) ON CONFLICT DO NOTHING`
		} else {
			stmt = `INSERT OR IGNORE INTO device_group_members (group_id, device_id) VALUES (?, ?)`
		}
		for _, deviceID := range valid {
			if _, err := db.DB.Exec(stmt, groupID, deviceID); err != nil {
				slog.Warn("group member insert failed", "error", err)
			}
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "device_group.add_members", "device_group", groupID, fmt.Sprintf("added %d devices", len(valid)), c.IP())
		return c.JSON(fiber.Map{"added": len(valid)})
	})

	api.Delete("/device-groups/:id/members/:deviceId", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		groupID := c.Params("id")
		deviceID := c.Params("deviceId")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{groupID}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM device_groups WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Group not found"})
		}
		if _, err := db.DB.Exec(`DELETE FROM device_group_members WHERE group_id = ? AND device_id = ?`, groupID, deviceID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to remove member"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "device_group.remove_member", "device_group", groupID, fmt.Sprintf("removed device %s", deviceID), c.IP())
		return c.JSON(fiber.Map{"message": "Member removed"})
	})

}
