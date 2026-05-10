package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"vaporrmm/server/internal/ai/capabilities"
	"vaporrmm/server/internal/ai/rag"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// allowedTicketStatuses / allowedTicketPriorities gate user-supplied values
// before they hit the DB. Without these the dashboard's status-bucket
// queries (`WHERE status NOT IN (...)`) silently miss rows with junk
// statuses, and free-text priority breaks badge rendering.
var (
	allowedTicketStatuses   = map[string]bool{"open": true, "in_progress": true, "pending": true, "resolved": true, "closed": true}
	allowedTicketPriorities = map[string]bool{"low": true, "medium": true, "high": true, "critical": true}
)

func RegisterTicketRoutes(api fiber.Router, cfg Config) {
	// List tickets
	api.Get("/tickets", func(c *fiber.Ctx) error {
		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var rows *sql.Rows
		var err error
		// Hard cap so a runaway tenant can't OOM the server. Pagination
		// can land later; for now 1k rows is well past dashboard needs.
		if auth.IsSuperAdmin(role) {
			rows, err = db.DB.Query(`SELECT id, title, description, status, priority, device_id, assigned_to, created_at, updated_at, due_date, category FROM tickets ORDER BY created_at DESC LIMIT 1000`)
		} else {
			rows, err = db.DB.Query(`SELECT id, title, description, status, priority, device_id, assigned_to, created_at, updated_at, due_date, category FROM tickets WHERE tenant_id = ? ORDER BY created_at DESC LIMIT 1000`, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query tickets"})
		}
		defer rows.Close()

		tickets := []fiber.Map{}
		for rows.Next() {
			var id, title, status, priority, category string
			var description, deviceID, assignedTo sql.NullString
			var createdAt, updatedAt int64
			var dueDate sql.NullInt64
			if err := rows.Scan(&id, &title, &description, &status, &priority, &deviceID, &assignedTo, &createdAt, &updatedAt, &dueDate, &category); err != nil {
				slog.Warn("ticket scan failed", "error", err)
				continue
			}
			tickets = append(tickets, fiber.Map{
				"id": id, "title": title, "description": description.String, "status": status,
				"priority": priority, "device_id": deviceID.String, "assigned_to": assignedTo.String,
				"created_at": createdAt, "updated_at": updatedAt, "due_date": dueDate.Int64, "category": category,
			})
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		return c.JSON(fiber.Map{"tickets": tickets})
	})

	// Create ticket
	api.Post("/tickets", func(c *fiber.Ctx) error {
		var req struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    string `json:"priority"`
			DeviceID    string `json:"device_id,omitempty"`
			Category    string `json:"category,omitempty"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		if req.Title == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Title is required"})
		}
		if req.Priority == "" {
			req.Priority = "medium"
		}
		if !allowedTicketPriorities[req.Priority] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid priority"})
		}
		if req.Category == "" {
			req.Category = "general"
		}
		id := uuid.New().String()
		now := time.Now().Unix()
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		// If a device_id is supplied, it must belong to the caller's tenant.
		if req.DeviceID != "" {
			role, _ := c.Locals("user_role").(string)
			var exists int
			if auth.IsSuperAdmin(role) {
				_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE id = ?`, req.DeviceID).Scan(&exists)
			} else {
				_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE id = ? AND tenant_id = ?`, req.DeviceID, tenantID).Scan(&exists)
			}
			if exists == 0 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "device_id not found in your tenant"})
			}
		}
		_, err := db.DB.Exec(`INSERT INTO tickets (id, title, description, status, priority, device_id, created_at, updated_at, category, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, req.Title, req.Description, "open", req.Priority, req.DeviceID, now, now, req.Category, tenantID)
		if err != nil {
			slog.Error("ticket insert failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create ticket"})
		}

		// Fire-and-forget AI triage. The capability is gated by the
		// chokepoint (returns ErrCapabilityDisabled when off), so this is
		// safe to call unconditionally; the audit log will show "disabled"
		// for tenants that haven't opted in. We persist the JSON result to
		// tickets.ai_triage so the dashboard can render it without re-running
		// the call.
		go func(ticketID, title, body string) {
			// Belt-and-suspenders panic guard. The chokepoint's callSafely
			// recovers panics inside the AI provider closure, but a panic
			// in code that runs BEFORE the closure (rag.Retrieve nil-deref,
			// for example) would crash the goroutine. We swallow it here so
			// a buggy capability can't take down the server.
			defer func() {
				if r := recover(); r != nil {
					slog.Warn("ticket triage goroutine panicked", "ticket_id", ticketID, "panic", r)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			triage, err := capabilities.SummariseTicket(ctx, tenantID, "", ticketID, title, body)
			if err != nil {
				return // chokepoint already audited; nothing more to do
			}
			payload, _ := json.Marshal(triage)
			// Tenant-scoped UPDATE so a UUID collision (astronomically
			// unlikely but possible) cannot write triage data into another
			// tenant's ticket. Every other ticket-table mutation in this
			// file is tenant-scoped; this one must be too.
			_, _ = db.DB.Exec(`UPDATE tickets SET ai_triage = ? WHERE id = ? AND tenant_id = ?`,
				string(payload), ticketID, tenantID)
		}(id, req.Title, req.Description)

		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "message": "Ticket created"})
	})

	// Get ticket by ID
	api.Get("/tickets/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var title, status, priority, category string
		var description, deviceID, assignedTo sql.NullString
		var createdAt, updatedAt int64
		var dueDate sql.NullInt64
		var err error
		if auth.IsSuperAdmin(role) {
			err = db.DB.QueryRow(`SELECT id, title, description, status, priority, device_id, assigned_to, created_at, updated_at, due_date, category FROM tickets WHERE id = ?`, id).
				Scan(&id, &title, &description, &status, &priority, &deviceID, &assignedTo, &createdAt, &updatedAt, &dueDate, &category)
		} else {
			err = db.DB.QueryRow(`SELECT id, title, description, status, priority, device_id, assigned_to, created_at, updated_at, due_date, category FROM tickets WHERE id = ? AND tenant_id = ?`, id, tenantID).
				Scan(&id, &title, &description, &status, &priority, &deviceID, &assignedTo, &createdAt, &updatedAt, &dueDate, &category)
		}
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ticket not found"})
		}
		return c.JSON(fiber.Map{
			"id": id, "title": title, "description": description.String, "status": status,
			"priority": priority, "device_id": deviceID.String, "assigned_to": assignedTo.String,
			"created_at": createdAt, "updated_at": updatedAt, "due_date": dueDate.Int64, "category": category,
		})
	})

	// Update ticket
	api.Put("/tickets/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		var req struct {
			Title       string `json:"title,omitempty"`
			Description string `json:"description,omitempty"`
			Status      string `json:"status,omitempty"`
			Priority    string `json:"priority,omitempty"`
			AssignedTo  string `json:"assigned_to,omitempty"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}
		if req.Status != "" && !allowedTicketStatuses[req.Status] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid status"})
		}
		if req.Priority != "" && !allowedTicketPriorities[req.Priority] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid priority"})
		}
		now := time.Now().Unix()
		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var err error
		if auth.IsSuperAdmin(role) {
			_, err = db.DB.Exec(`UPDATE tickets SET title = COALESCE(NULLIF(?, ''), title), description = COALESCE(NULLIF(?, ''), description), status = COALESCE(NULLIF(?, ''), status), priority = COALESCE(NULLIF(?, ''), priority), assigned_to = COALESCE(NULLIF(?, ''), assigned_to), updated_at = ? WHERE id = ?`,
				req.Title, req.Description, req.Status, req.Priority, req.AssignedTo, now, id)
		} else {
			_, err = db.DB.Exec(`UPDATE tickets SET title = COALESCE(NULLIF(?, ''), title), description = COALESCE(NULLIF(?, ''), description), status = COALESCE(NULLIF(?, ''), status), priority = COALESCE(NULLIF(?, ''), priority), assigned_to = COALESCE(NULLIF(?, ''), assigned_to), updated_at = ? WHERE id = ? AND tenant_id = ?`,
				req.Title, req.Description, req.Status, req.Priority, req.AssignedTo, now, id, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update ticket"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "ticket.update", "ticket", id,
			fmt.Sprintf("status=%s priority=%s assigned_to=%s", req.Status, req.Priority, req.AssignedTo), c.IP())

		// On status transition to resolved/closed, fire-and-forget RAG
		// indexing of the ticket so future tickets in this tenant can use
		// it as a reference. We embed the title + description + any
		// resolution_summary the ticket carried; the rag indexer
		// short-circuits if the text hash matches (idempotent).
		if req.Status == "resolved" || req.Status == "closed" {
			go func(ticketID string) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				var t, d, r sql.NullString
				_ = db.DB.QueryRow(`SELECT title, description, COALESCE(resolution_summary,'') FROM tickets WHERE id = ? AND tenant_id = ?`, ticketID, tenantID).Scan(&t, &d, &r)
				body := t.String + "\n\n" + d.String
				if r.String != "" {
					body += "\n\nresolution: " + r.String
				}
				_ = rag.New().Index(ctx, rag.Scope{TenantID: tenantID}, rag.SourceTicket, ticketID, body)
			}(id)
		}

		return c.JSON(fiber.Map{"message": "Ticket updated"})
	})

	// Delete ticket
	api.Delete("/tickets/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		role, _ := c.Locals("user_role").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		var err error
		if auth.IsSuperAdmin(role) {
			_, err = db.DB.Exec(`DELETE FROM tickets WHERE id = ?`, id)
		} else {
			_, err = db.DB.Exec(`DELETE FROM tickets WHERE id = ? AND tenant_id = ?`, id, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete ticket"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "ticket.delete", "ticket", id, "ticket deleted", c.IP())
		return c.JSON(fiber.Map{"message": "Ticket deleted"})
	})
}
