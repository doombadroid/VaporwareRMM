package handlers

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// maintenanceCron is "minute hour dow" — three integers separated by
// spaces. dow is 0-6 (Sunday=0). We deliberately keep this simpler than
// full cron syntax: hands-off operators don't need wildcards or steps,
// and parsing is trivial without a library dependency.
type cronSpec struct {
	Minute int
	Hour   int
	DOW    int // 0-6 Sunday
}

func parseCron(s string) (cronSpec, error) {
	fields := strings.Fields(s)
	if len(fields) != 3 {
		return cronSpec{}, fmt.Errorf("cron must have 3 fields (minute hour dow): %q", s)
	}
	m, err := strconv.Atoi(fields[0])
	if err != nil || m < 0 || m > 59 {
		return cronSpec{}, fmt.Errorf("minute out of range")
	}
	h, err := strconv.Atoi(fields[1])
	if err != nil || h < 0 || h > 23 {
		return cronSpec{}, fmt.Errorf("hour out of range")
	}
	d, err := strconv.Atoi(fields[2])
	if err != nil || d < 0 || d > 6 {
		return cronSpec{}, fmt.Errorf("dow out of range")
	}
	return cronSpec{Minute: m, Hour: h, DOW: d}, nil
}

// inWindow returns true if `now` (in the window's tz) falls within the
// scheduled period of `duration` minutes starting at the cron-defined
// instant.
func inWindow(now time.Time, spec cronSpec, durationMinutes int) bool {
	if int(now.Weekday()) != spec.DOW {
		return false
	}
	startMins := spec.Hour*60 + spec.Minute
	currentMins := now.Hour()*60 + now.Minute()
	return currentMins >= startMins && currentMins < startMins+durationMinutes
}

// validTZ ensures the timezone string is loadable. IANA names only.
func validTZ(name string) bool {
	_, err := time.LoadLocation(name)
	return err == nil
}

func RegisterMaintenanceRoutes(api fiber.Router) {
	api.Get("/maintenance-windows", func(c *fiber.Ctx) error {
		tf, tArgs := tenantFilter(c)
		clause := ""
		args := []interface{}{}
		if tf != "" {
			clause = " WHERE tenant_id = ?"
			args = append(args, tArgs...)
		}
		rows, err := db.DB.Query(`SELECT id, name, COALESCE(group_id,''), weekly_cron, duration_minutes, timezone, enabled, COALESCE(last_run_at,0), created_at FROM maintenance_windows`+clause+` ORDER BY created_at DESC LIMIT 500`, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query windows"})
		}
		defer rows.Close()
		type window struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			GroupID         string `json:"group_id,omitempty"`
			WeeklyCron      string `json:"weekly_cron"`
			DurationMinutes int    `json:"duration_minutes"`
			Timezone        string `json:"timezone"`
			Enabled         bool   `json:"enabled"`
			LastRunAt       int64  `json:"last_run_at,omitempty"`
			CreatedAt       int64  `json:"created_at"`
		}
		out := []window{}
		for rows.Next() {
			var w window
			var enabled int
			if err := rows.Scan(&w.ID, &w.Name, &w.GroupID, &w.WeeklyCron, &w.DurationMinutes, &w.Timezone, &enabled, &w.LastRunAt, &w.CreatedAt); err != nil {
				continue
			}
			w.Enabled = enabled == 1
			out = append(out, w)
		}
		return c.JSON(fiber.Map{"windows": out})
	})

	api.Post("/maintenance-windows", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Name            string `json:"name"`
			GroupID         string `json:"group_id,omitempty"`
			WeeklyCron      string `json:"weekly_cron"`
			DurationMinutes int    `json:"duration_minutes"`
			Timezone        string `json:"timezone"`
		}
		if err := c.BodyParser(&req); err != nil || req.Name == "" || req.WeeklyCron == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name and weekly_cron required"})
		}
		if _, err := parseCron(req.WeeklyCron); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "weekly_cron: " + err.Error()})
		}
		if req.DurationMinutes <= 0 || req.DurationMinutes > 1440 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "duration_minutes must be 1-1440"})
		}
		if req.Timezone == "" {
			req.Timezone = "UTC"
		}
		if !validTZ(req.Timezone) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid timezone"})
		}
		if len(req.Name) > 128 {
			req.Name = req.Name[:128]
		}
		tenantID := callerTenantID(c)
		// Verify group_id (if provided) is in the same tenant.
		if req.GroupID != "" {
			tf, tArgs := tenantFilter(c)
			args := append([]interface{}{req.GroupID}, tArgs...)
			var ok int
			if err := db.DB.QueryRow(`SELECT 1 FROM device_groups WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "group_id not found in tenant"})
			}
		}
		id := uuid.New().String()
		groupArg := interface{}(nil)
		if req.GroupID != "" {
			groupArg = req.GroupID
		}
		_, err := db.DB.Exec(`INSERT INTO maintenance_windows (id, tenant_id, name, group_id, weekly_cron, duration_minutes, timezone, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, tenantID, req.Name, groupArg, req.WeeklyCron, req.DurationMinutes, req.Timezone, 1, time.Now().Unix())
		if err != nil {
			slog.Warn("window insert failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create window"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "maintenance_window.create", "maintenance_window", id, fmt.Sprintf("created window %s (%s, %s)", req.Name, req.WeeklyCron, req.Timezone), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
	})

	api.Delete("/maintenance-windows/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM maintenance_windows WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Window not found"})
		}
		if _, err := db.DB.Exec(`DELETE FROM maintenance_windows WHERE id = ?`, id); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete window"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "maintenance_window.delete", "maintenance_window", id, "deleted window", c.IP())
		return c.JSON(fiber.Map{"message": "Window deleted"})
	})
}

// MaintenanceWorkerOnce evaluates all enabled windows. If a window is
// currently in its scheduled period AND it hasn't run in this
// occurrence (last_run_at < window-start), queue patch installs for
// every pending patch on every device in scope.
//
// We deliberately do NOT spread installs across the window — operators
// expect "patch on Sunday 02:00" to start now and finish whenever the
// agent's done. Spreading would need a per-device kick algorithm.
//
// The mutex is a single-process guard. Multi-server fan-out is Stage 16.
var maintenanceMu sync.Mutex

func MaintenanceWorkerOnce() {
	maintenanceMu.Lock()
	defer maintenanceMu.Unlock()

	rows, err := db.DB.Query(`SELECT id, tenant_id, COALESCE(group_id,''), weekly_cron, duration_minutes, timezone, COALESCE(last_run_at,0) FROM maintenance_windows WHERE enabled = 1`)
	if err != nil {
		slog.Warn("maintenance worker query failed", "error", err)
		return
	}
	defer rows.Close()

	type windowRow struct {
		id, tenantID, groupID, cronStr, tz string
		duration                           int
		lastRun                            int64
	}
	var windows []windowRow
	for rows.Next() {
		var w windowRow
		if err := rows.Scan(&w.id, &w.tenantID, &w.groupID, &w.cronStr, &w.duration, &w.tz, &w.lastRun); err == nil {
			windows = append(windows, w)
		}
	}
	rows.Close()

	for _, w := range windows {
		spec, err := parseCron(w.cronStr)
		if err != nil {
			continue
		}
		loc, err := time.LoadLocation(w.tz)
		if err != nil {
			continue
		}
		nowLocal := time.Now().In(loc)
		if !inWindow(nowLocal, spec, w.duration) {
			continue
		}
		// Compute the start instant of the current occurrence to
		// idempotency-check last_run_at: only fire once per window per
		// week, no matter how often the worker ticks.
		startToday := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), spec.Hour, spec.Minute, 0, 0, loc).Unix()
		if w.lastRun >= startToday {
			continue
		}
		fired := fireWindow(w.id, w.tenantID, w.groupID)
		_, _ = db.DB.Exec(`UPDATE maintenance_windows SET last_run_at = ? WHERE id = ?`, time.Now().Unix(), w.id)
		slog.Info("maintenance window fired", "window_id", w.id, "tenant", w.tenantID, "patches_queued", fired)
	}
}

// fireWindow queues install commands for every pending patch on every
// in-scope device. Returns count of commands queued.
func fireWindow(windowID, tenantID, groupID string) int {
	// Resolve the device scope.
	var deviceRows *sql.Rows
	var err error
	if groupID == "" {
		deviceRows, err = db.DB.Query(`SELECT id FROM devices WHERE tenant_id = ?`, tenantID)
	} else {
		deviceRows, err = db.DB.Query(`SELECT d.id FROM device_group_members m JOIN devices d ON d.id = m.device_id WHERE m.group_id = ? AND d.tenant_id = ?`, groupID, tenantID)
	}
	if err != nil {
		slog.Warn("window device scope query failed", "error", err)
		return 0
	}
	defer deviceRows.Close()
	var deviceIDs []string
	for deviceRows.Next() {
		var id string
		if err := deviceRows.Scan(&id); err == nil {
			deviceIDs = append(deviceIDs, id)
		}
	}
	deviceRows.Close()

	count := 0
	for _, deviceID := range deviceIDs {
		patchRows, err := db.DB.Query(`SELECT id, COALESCE(source,''), COALESCE(kb_id,'') FROM patches WHERE device_id = ? AND status = 'pending' AND tenant_id = ?`, deviceID, tenantID)
		if err != nil {
			continue
		}
		var pending []struct {
			id, source, kbID string
		}
		for patchRows.Next() {
			var p struct{ id, source, kbID string }
			if err := patchRows.Scan(&p.id, &p.source, &p.kbID); err == nil {
				pending = append(pending, p)
			}
		}
		patchRows.Close()
		for _, p := range pending {
			if p.source == "" || p.kbID == "" {
				continue
			}
			cmdType, payload, err := installCommandFor(p.source, p.kbID)
			if err != nil {
				continue
			}
			cmdID := uuid.New().String()
			if _, err := db.DB.Exec(`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				cmdID, deviceID, cmdType, payload, "pending", time.Now().Unix(), tenantID); err != nil {
				slog.Warn("window install queue failed", "error", err)
				continue
			}
			_, _ = db.DB.Exec(`UPDATE patches SET status = ? WHERE id = ?`, "installing", p.id)
			count++
		}
	}
	events.AuditLogTenant(tenantID, "system", "maintenance_window.fire", "maintenance_window", windowID, fmt.Sprintf("queued %d patch installs", count), "system")
	return count
}
