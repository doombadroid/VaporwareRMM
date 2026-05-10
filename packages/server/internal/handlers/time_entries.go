package handlers

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// RegisterTimeEntryRoutes — admin-side billing time tracking. NEVER
// exposed via the portal scope; ticket_time_entries are not part of
// /portal/* routes by design (Stage 12 plan, P18).
func RegisterTimeEntryRoutes(api fiber.Router) {
	api.Get("/tickets/:id/time-entries", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		ticketID := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{ticketID}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM tickets WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ticket not found"})
		}
		rows, err := db.DB.Query(`SELECT id, user_id, minutes, billable, COALESCE(note,''), started_at, created_at FROM ticket_time_entries WHERE ticket_id = ? ORDER BY started_at DESC LIMIT 1000`, ticketID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query entries"})
		}
		defer rows.Close()
		type entry struct {
			ID        string `json:"id"`
			UserID    string `json:"user_id"`
			Minutes   int    `json:"minutes"`
			Billable  bool   `json:"billable"`
			Note      string `json:"note,omitempty"`
			StartedAt int64  `json:"started_at"`
			CreatedAt int64  `json:"created_at"`
		}
		out := []entry{}
		for rows.Next() {
			var e entry
			var billable int
			if err := rows.Scan(&e.ID, &e.UserID, &e.Minutes, &billable, &e.Note, &e.StartedAt, &e.CreatedAt); err == nil {
				e.Billable = billable == 1
				out = append(out, e)
			}
		}
		return c.JSON(fiber.Map{"entries": out})
	})

	api.Post("/tickets/:id/time-entries", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		ticketID := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{ticketID}, tArgs...)
		var ticketTenant string
		if err := db.DB.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM tickets WHERE id = ?`+tf, args...).Scan(&ticketTenant); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ticket not found"})
		}
		var req struct {
			Minutes   int    `json:"minutes"`
			Billable  *bool  `json:"billable,omitempty"`
			Note      string `json:"note,omitempty"`
			StartedAt int64  `json:"started_at,omitempty"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		// Sanity bounds: 1 minute to 24 hours per entry. Negative or
		// zero minutes is rejected (use DELETE to remove an entry).
		if req.Minutes <= 0 || req.Minutes > 24*60 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "minutes must be 1-1440"})
		}
		if len(req.Note) > 1024 {
			req.Note = req.Note[:1024]
		}
		if req.StartedAt <= 0 {
			req.StartedAt = time.Now().Unix()
		}
		billable := 1
		if req.Billable != nil && !*req.Billable {
			billable = 0
		}
		userID, _ := c.Locals("user_id").(string)
		id := uuid.New().String()
		if _, err := db.DB.Exec(`INSERT INTO ticket_time_entries (id, ticket_id, tenant_id, user_id, minutes, billable, note, started_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, ticketID, ticketTenant, userID, req.Minutes, billable, req.Note, req.StartedAt, time.Now().Unix()); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to insert entry"})
		}
		events.AuditLogTenant(ticketTenant, userID, "time_entry.create", "time_entry", id, fmt.Sprintf("logged %d min on ticket %s", req.Minutes, ticketID), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
	})

	api.Delete("/time-entries/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		entryID := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{entryID}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM ticket_time_entries WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Entry not found"})
		}
		if _, err := db.DB.Exec(`DELETE FROM ticket_time_entries WHERE id = ?`, entryID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "time_entry.delete", "time_entry", entryID, "deleted time entry", c.IP())
		return c.JSON(fiber.Map{"message": "deleted"})
	})

	// Monthly billing export. ?year=YYYY&month=MM (1-12). Returns CSV
	// with one row per entry. Tenant-scoped except for super_admin.
	api.Get("/billing/export", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		yearStr := c.Query("year")
		monthStr := c.Query("month")
		year, err := strconv.Atoi(yearStr)
		if err != nil || year < 2000 || year > 2200 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "year required"})
		}
		month, err := strconv.Atoi(monthStr)
		if err != nil || month < 1 || month > 12 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "month required (1-12)"})
		}
		startOfMonth := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		startOfNext := startOfMonth.AddDate(0, 1, 0)
		startTs := startOfMonth.Unix()
		endTs := startOfNext.Unix()
		args := []interface{}{startTs, endTs}
		where := "WHERE te.started_at >= ? AND te.started_at < ?"
		role, _ := c.Locals("user_role").(string)
		if !auth.IsSuperAdmin(role) {
			where += " AND te.tenant_id = ?"
			args = append(args, callerTenantID(c))
		}
		rows, err := db.DB.Query(`
			SELECT te.id, te.tenant_id, te.ticket_id, COALESCE(t.title,''), te.user_id, te.minutes, te.billable, COALESCE(te.note,''), te.started_at
			  FROM ticket_time_entries te
			  LEFT JOIN tickets t ON t.id = te.ticket_id AND t.tenant_id = te.tenant_id
			  `+where+`
			  ORDER BY te.started_at ASC LIMIT 50000`, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query entries"})
		}
		defer rows.Close()
		var buf strings.Builder
		w := csv.NewWriter(&buf)
		_ = w.Write([]string{"entry_id", "tenant_id", "ticket_id", "ticket_title", "user_id", "minutes", "billable", "note", "started_at"})
		for rows.Next() {
			var (
				id, tid, ticket, title, uid, note string
				minutes                           int
				billable                          int
				started                           int64
			)
			if err := rows.Scan(&id, &tid, &ticket, &title, &uid, &minutes, &billable, &note, &started); err != nil {
				continue
			}
			_ = w.Write([]string{
				id, tid, ticket, csvSafe(title), uid,
				strconv.Itoa(minutes), strconv.Itoa(billable), csvSafe(note),
				time.Unix(started, 0).UTC().Format(time.RFC3339),
			})
		}
		w.Flush()
		c.Set("Content-Type", "text/csv")
		c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="billing-%04d-%02d.csv"`, year, month))
		return c.SendString(buf.String())
	})
}
