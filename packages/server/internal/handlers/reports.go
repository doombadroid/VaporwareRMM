package handlers

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/email"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

var allowedReportTypes = map[string]bool{
	"fleet_status":     true,
	"sla_monthly":      true,
	"patch_compliance": true,
	"ticket_volume":    true,
	"billing_hours":    true,
}

const maxReportRecipients = 20

func RegisterReportRoutes(api fiber.Router) {
	api.Get("/admin/reports", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tf, tArgs := tenantFilter(c)
		clause := ""
		args := []interface{}{}
		if tf != "" {
			clause = " WHERE tenant_id = ?"
			args = append(args, tArgs...)
		}
		rows, err := db.DB.Query(`SELECT id, name, report_type, weekly_cron, timezone, email_recipients, enabled, COALESCE(last_run_at,0), COALESCE(last_error,''), created_at FROM report_schedules`+clause+` ORDER BY created_at DESC LIMIT 200`, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query"})
		}
		defer rows.Close()
		type r struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			ReportType      string `json:"report_type"`
			WeeklyCron      string `json:"weekly_cron"`
			Timezone        string `json:"timezone"`
			EmailRecipients string `json:"email_recipients"`
			Enabled         bool   `json:"enabled"`
			LastRunAt       int64  `json:"last_run_at,omitempty"`
			LastError       string `json:"last_error,omitempty"`
			CreatedAt       int64  `json:"created_at"`
		}
		out := []r{}
		for rows.Next() {
			var row r
			var en int
			if err := rows.Scan(&row.ID, &row.Name, &row.ReportType, &row.WeeklyCron, &row.Timezone, &row.EmailRecipients, &en, &row.LastRunAt, &row.LastError, &row.CreatedAt); err == nil {
				row.Enabled = en == 1
				out = append(out, row)
			}
		}
		return c.JSON(fiber.Map{"schedules": out})
	})

	api.Post("/admin/reports", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Name            string   `json:"name"`
			ReportType      string   `json:"report_type"`
			WeeklyCron      string   `json:"weekly_cron"`
			Timezone        string   `json:"timezone"`
			EmailRecipients []string `json:"email_recipients"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name required"})
		}
		if !allowedReportTypes[req.ReportType] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid report_type"})
		}
		if _, err := parseCron(req.WeeklyCron); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "weekly_cron: " + err.Error()})
		}
		if req.Timezone == "" {
			req.Timezone = "UTC"
		}
		if !validTZ(req.Timezone) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid timezone"})
		}
		if len(req.EmailRecipients) == 0 || len(req.EmailRecipients) > maxReportRecipients {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("1-%d email recipients required", maxReportRecipients)})
		}
		recipients := strings.Join(req.EmailRecipients, ",")
		if len(recipients) > 4096 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "recipients list too long"})
		}
		id := uuid.New().String()
		tenantID := callerTenantID(c)
		if _, err := db.DB.Exec(`INSERT INTO report_schedules (id, tenant_id, name, report_type, weekly_cron, timezone, email_recipients, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, tenantID, req.Name, req.ReportType, req.WeeklyCron, req.Timezone, recipients, 1, time.Now().Unix()); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "save failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "report.create", "report", id, fmt.Sprintf("created report %s (%s)", req.Name, req.ReportType), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
	})

	api.Delete("/admin/reports/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM report_schedules WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		if _, err := db.DB.Exec(`DELETE FROM report_schedules WHERE id = ?`, id); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "report.delete", "report", id, "deleted report", c.IP())
		return c.JSON(fiber.Map{"message": "deleted"})
	})

	// Manual trigger — admin runs the report now and gets the CSV back.
	api.Post("/admin/reports/:id/run", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var (
			tenantID, name, rtype string
		)
		if err := db.DB.QueryRow(`SELECT tenant_id, name, report_type FROM report_schedules WHERE id = ?`+tf, args...).Scan(&tenantID, &name, &rtype); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		csvBytes, err := generateReport(tenantID, rtype)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		c.Set("Content-Type", "text/csv")
		c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`, rtype, time.Now().UTC().Format("20060102")))
		return c.Send(csvBytes)
	})
}

// reportMu serializes the worker. Single-process — multi-server fan-out
// covered by Stage 16.
var reportMu sync.Mutex

// ReportWorkerOnce evaluates each enabled schedule. Mirrors the
// maintenance-window dispatcher: tz-aware in-window check + last_run
// idempotency. Generates CSV for the report_type and emails it to
// recipients.
func ReportWorkerOnce() {
	reportMu.Lock()
	defer reportMu.Unlock()

	rows, err := db.DB.Query(`SELECT id, tenant_id, name, report_type, weekly_cron, timezone, email_recipients, COALESCE(last_run_at,0) FROM report_schedules WHERE enabled = 1`)
	if err != nil {
		slog.Warn("report worker query failed", "error", err)
		return
	}
	defer rows.Close()
	type job struct {
		id, tenantID, name, rtype, cron, tz, recipients string
		lastRun                                         int64
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.tenantID, &j.name, &j.rtype, &j.cron, &j.tz, &j.recipients, &j.lastRun); err == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()

	for _, j := range jobs {
		spec, err := parseCron(j.cron)
		if err != nil {
			continue
		}
		loc, err := time.LoadLocation(j.tz)
		if err != nil {
			continue
		}
		nowLocal := time.Now().In(loc)
		if !inWindow(nowLocal, spec, 60) {
			continue
		}
		startToday := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), spec.Hour, spec.Minute, 0, 0, loc).Unix()
		if j.lastRun >= startToday {
			continue
		}
		runReport(j.id, j.tenantID, j.name, j.rtype, j.recipients)
		_, _ = db.DB.Exec(`UPDATE report_schedules SET last_run_at = ?, last_error = ? WHERE id = ?`, time.Now().Unix(), "", j.id)
	}
}

func runReport(id, tenantID, name, rtype, recipientsCSV string) {
	csvBytes, err := generateReport(tenantID, rtype)
	if err != nil {
		_, _ = db.DB.Exec(`UPDATE report_schedules SET last_error = ? WHERE id = ?`, err.Error(), id)
		return
	}
	recipients := strings.Split(recipientsCSV, ",")
	for i := range recipients {
		recipients[i] = strings.TrimSpace(recipients[i])
	}
	subject := fmt.Sprintf("[vaporRMM] %s — %s", name, time.Now().UTC().Format("2006-01-02"))
	// existing email.Send is text-only single-recipient; loop over recipients
	// and inline a short CSV preview. Operators can pull the full CSV via
	// /admin/reports/:id/run if the inline preview gets truncated.
	preview := string(csvBytes)
	const maxInline = 32 * 1024
	truncated := false
	if len(preview) > maxInline {
		preview = preview[:maxInline]
		truncated = true
	}
	bodyText := fmt.Sprintf("Scheduled report\n\nReport: %s\nType: %s\nGenerated: %s UTC\n\n--- CSV ---\n%s\n",
		name, rtype, time.Now().UTC().Format(time.RFC3339), preview)
	if truncated {
		bodyText += "\n[truncated — fetch full report at /api/v1/admin/reports/" + id + "/run]\n"
	}
	for _, to := range recipients {
		if to == "" {
			continue
		}
		if err := email.Send(tenantID, to, subject, bodyText); err != nil {
			slog.Warn("report email failed", "error", err, "id", id, "to", to)
			_, _ = db.DB.Exec(`UPDATE report_schedules SET last_error = ? WHERE id = ?`, err.Error(), id)
			return
		}
	}
	events.AuditLogTenant(tenantID, "system", "report.send", "report", id, fmt.Sprintf("emailed report %s to %d recipients", rtype, len(recipients)), "system")
}

// generateReport produces a CSV body for a known report type. Switch
// statement keeps each report's query close to its column list.
func generateReport(tenantID, rtype string) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	switch rtype {
	case "fleet_status":
		_ = w.Write([]string{"hostname", "status", "os_name", "os_version", "ip_address", "last_seen"})
		rows, err := db.DB.Query(`SELECT hostname, status, COALESCE(os_name,''), COALESCE(os_version,''), COALESCE(ip_address,''), COALESCE(last_seen,0) FROM devices WHERE tenant_id = ? ORDER BY hostname ASC LIMIT 50000`, tenantID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var hostname, status, osN, osV, ip string
			var ls int64
			if err := rows.Scan(&hostname, &status, &osN, &osV, &ip, &ls); err == nil {
				_ = w.Write([]string{csvSafe(hostname), status, osN, osV, ip, strconv.FormatInt(ls, 10)})
			}
		}
	case "sla_monthly":
		_ = w.Write([]string{"metric", "value"})
		var total, online, openTickets int
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE tenant_id = ?`, tenantID).Scan(&total)
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE tenant_id = ? AND status = 'online'`, tenantID).Scan(&online)
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tickets WHERE tenant_id = ? AND status NOT IN ('resolved','closed')`, tenantID).Scan(&openTickets)
		_ = w.Write([]string{"total_devices", strconv.Itoa(total)})
		_ = w.Write([]string{"online_devices", strconv.Itoa(online)})
		_ = w.Write([]string{"open_tickets", strconv.Itoa(openTickets)})
	case "patch_compliance":
		_ = w.Write([]string{"hostname", "patch_title", "severity", "status", "kb_id"})
		rows, err := db.DB.Query(`SELECT COALESCE(d.hostname,''), p.title, p.severity, p.status, COALESCE(p.kb_id,'') FROM patches p LEFT JOIN devices d ON d.id = p.device_id AND d.tenant_id = p.tenant_id WHERE p.tenant_id = ? ORDER BY p.created_at DESC LIMIT 50000`, tenantID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var host, title, sev, status, kb string
			if err := rows.Scan(&host, &title, &sev, &status, &kb); err == nil {
				_ = w.Write([]string{csvSafe(host), csvSafe(title), sev, status, csvSafe(kb)})
			}
		}
	case "ticket_volume":
		_ = w.Write([]string{"day", "opened", "resolved"})
		// Per-day counts over last 30 days.
		cutoff := time.Now().Add(-30 * 24 * time.Hour).Unix()
		openedRows, err := db.DB.Query(`SELECT (created_at / 86400) * 86400 AS day, COUNT(*) FROM tickets WHERE tenant_id = ? AND created_at > ? GROUP BY day ORDER BY day ASC`, tenantID, cutoff)
		if err != nil {
			return nil, err
		}
		defer openedRows.Close()
		opened := map[int64]int{}
		for openedRows.Next() {
			var day int64
			var n int
			if err := openedRows.Scan(&day, &n); err == nil {
				opened[day] = n
			}
		}
		resolvedRows, err := db.DB.Query(`SELECT (updated_at / 86400) * 86400 AS day, COUNT(*) FROM tickets WHERE tenant_id = ? AND updated_at > ? AND status IN ('resolved','closed') GROUP BY day ORDER BY day ASC`, tenantID, cutoff)
		if err == nil {
			defer resolvedRows.Close()
			resolved := map[int64]int{}
			for resolvedRows.Next() {
				var day int64
				var n int
				if err := resolvedRows.Scan(&day, &n); err == nil {
					resolved[day] = n
				}
			}
			days := map[int64]struct{}{}
			for d := range opened {
				days[d] = struct{}{}
			}
			for d := range resolved {
				days[d] = struct{}{}
			}
			for d := range days {
				_ = w.Write([]string{time.Unix(d, 0).UTC().Format("2006-01-02"), strconv.Itoa(opened[d]), strconv.Itoa(resolved[d])})
			}
		}
	case "billing_hours":
		_ = w.Write([]string{"ticket_id", "minutes", "billable", "started_at"})
		cutoff := time.Now().Add(-30 * 24 * time.Hour).Unix()
		rows, err := db.DB.Query(`SELECT ticket_id, minutes, billable, started_at FROM ticket_time_entries WHERE tenant_id = ? AND started_at > ? ORDER BY started_at ASC LIMIT 50000`, tenantID, cutoff)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var ticket string
			var minutes, billable int
			var started int64
			if err := rows.Scan(&ticket, &minutes, &billable, &started); err == nil {
				_ = w.Write([]string{ticket, strconv.Itoa(minutes), strconv.Itoa(billable), time.Unix(started, 0).UTC().Format(time.RFC3339)})
			}
		}
	default:
		return nil, fmt.Errorf("unknown report type %q", rtype)
	}
	w.Flush()
	return buf.Bytes(), nil
}
