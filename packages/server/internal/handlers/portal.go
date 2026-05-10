package handlers

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxPortalTicketTitleLen = 256
	maxPortalTicketDescLen  = 16 * 1024
	maxPortalCommentLen     = 32 * 1024
	portalCookieName        = "portal_token"
	portalSessionHours      = 12
)

// portalRateBucket is one rolling-window counter per customer per
// route. In-memory only — multi-server horizontal scale-out moves this
// into Redis (Stage 16).
type portalRateBucket struct {
	count   int
	resetAt time.Time
}

var (
	portalRateMu    sync.Mutex
	portalRateStore = map[string]*portalRateBucket{}
)

// portalCustomerRateLimiter caps requests per (route, customer_id) over
// the given window. Returns 429 on exceeded; never bypasses based on
// IP because portal users share egress NAT.
func portalCustomerRateLimiter(maxRequests int, window time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		customerID, _ := c.Locals("customer_id").(string)
		if customerID == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthenticated"})
		}
		key := c.Path() + "|" + customerID + "|" + fmt.Sprint(window.Seconds())
		now := time.Now()
		portalRateMu.Lock()
		b, ok := portalRateStore[key]
		if !ok || now.After(b.resetAt) {
			portalRateStore[key] = &portalRateBucket{count: 1, resetAt: now.Add(window)}
			portalRateMu.Unlock()
			return c.Next()
		}
		b.count++
		if b.count > maxRequests {
			resetAt := b.resetAt
			portalRateMu.Unlock()
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":   "rate limit exceeded",
				"message": fmt.Sprintf("retry after %s", resetAt.Format(time.RFC3339)),
			})
		}
		portalRateMu.Unlock()
		return c.Next()
	}
}

// portalCustomer is the redacted /portal/me response. Never returns
// password_hash or totp_secret.
type portalCustomer struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	TenantID string `json:"tenant_id"`
	DeviceID string `json:"device_id,omitempty"`
}

// RegisterPortalRoutes wires the customer portal under /api/v1/portal/*.
// publicAPI is the un-authed group (for /portal/login, /portal/logout);
// portalAPI is the auth-protected group (PortalAuthMiddleware applied
// upstream by main.go).
func RegisterPortalRoutes(app *fiber.App, publicAPI fiber.Router, portalAPI fiber.Router) {
	// Public: login + logout. Rate-limited to slow brute force; the
	// in-memory limiter shares state with admin login because both
	// hit auth.RateLimiter — acceptable.
	publicAPI.Post("/portal/login", auth.RateLimiter(10, time.Minute), func(c *fiber.Ctx) error {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			TenantID string `json:"tenant_id"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		req.Email = strings.ToLower(strings.TrimSpace(req.Email))
		if req.Email == "" || req.Password == "" || req.TenantID == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email, password, and tenant_id required"})
		}
		// Enumerate-resistant lookup: load row even on miss, then
		// bcrypt-compare a dummy if the row doesn't exist so timing
		// roughly matches.
		var (
			id, name, hash string
			disabled       int
			deviceID       sql.NullString
		)
		err := db.DB.QueryRow(`SELECT id, name, password_hash, disabled, device_id FROM customer_users WHERE tenant_id = ? AND email = ?`,
			req.TenantID, req.Email).Scan(&id, &name, &hash, &disabled, &deviceID)
		if err != nil {
			// constant-time-ish: compare against a fixed valid hash so
			// timing doesn't leak existence
			_ = bcrypt.CompareHashAndPassword(
				[]byte("$2a$12$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
				[]byte(req.Password),
			)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
		}
		if disabled == 1 {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "account disabled"})
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
		}
		dev := ""
		if deviceID.Valid {
			dev = deviceID.String
		}
		token, err := auth.GeneratePortalJWT(id, req.TenantID, dev, portalSessionHours)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "token issue failed"})
		}
		_, _ = db.DB.Exec(`UPDATE customer_users SET last_login = ? WHERE id = ?`, time.Now().Unix(), id)
		c.Cookie(&fiber.Cookie{
			Name:     portalCookieName,
			Value:    token,
			HTTPOnly: true,
			Secure:   c.Protocol() == "https",
			SameSite: "Lax",
			Path:     "/api/v1/portal",
			MaxAge:   portalSessionHours * 3600,
		})
		// Mint a fresh CSRF token paired with the session. Same
		// double-submit pattern as admin login: cookie is JS-readable
		// (the SPA copies the value into the X-CSRF-Token header on
		// every state-changing request).
		csrfToken := uuid.New().String()
		c.Cookie(&fiber.Cookie{
			Name:     "csrf_token",
			Value:    csrfToken,
			HTTPOnly: false,
			Secure:   c.Protocol() == "https",
			SameSite: "Lax",
			Path:     "/",
			MaxAge:   portalSessionHours * 3600,
		})
		events.AuditLogTenant(req.TenantID, id, "portal.login", "customer", id, "portal login", c.IP())
		return c.JSON(fiber.Map{"id": id, "email": req.Email, "name": name, "tenant_id": req.TenantID, "device_id": dev})
	})

	publicAPI.Post("/portal/logout", func(c *fiber.Ctx) error {
		c.Cookie(&fiber.Cookie{
			Name:     portalCookieName,
			Value:    "",
			HTTPOnly: true,
			Secure:   c.Protocol() == "https",
			SameSite: "Lax",
			Path:     "/api/v1/portal",
			MaxAge:   -1,
		})
		return c.JSON(fiber.Map{"message": "logged out"})
	})

	// Protected portal endpoints. portalAPI has PortalAuthMiddleware
	// applied at registration time in main.go.
	portalAPI.Get("/me", func(c *fiber.Ctx) error {
		customerID, _ := c.Locals("customer_id").(string)
		tenantID, _ := c.Locals("tenant_id").(string)
		deviceID, _ := c.Locals("customer_device_id").(string)
		var email, name string
		if err := db.DB.QueryRow(`SELECT email, name FROM customer_users WHERE id = ?`, customerID).Scan(&email, &name); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "customer not found"})
		}
		return c.JSON(portalCustomer{ID: customerID, Email: email, Name: name, TenantID: tenantID, DeviceID: deviceID})
	})

	// Tickets list — scoped to customer's tenant + device (if set).
	portalAPI.Get("/tickets", func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		deviceID, _ := c.Locals("customer_device_id").(string)
		args := []interface{}{tenantID}
		where := "tenant_id = ?"
		if deviceID != "" {
			where += " AND device_id = ?"
			args = append(args, deviceID)
		}
		rows, err := db.DB.Query(`SELECT id, title, status, priority, created_at, updated_at FROM tickets WHERE `+where+` ORDER BY created_at DESC LIMIT 200`, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query tickets"})
		}
		defer rows.Close()
		type t struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Status    string `json:"status"`
			Priority  string `json:"priority"`
			CreatedAt int64  `json:"created_at"`
			UpdatedAt int64  `json:"updated_at"`
		}
		out := []t{}
		for rows.Next() {
			var row t
			if err := rows.Scan(&row.ID, &row.Title, &row.Status, &row.Priority, &row.CreatedAt, &row.UpdatedAt); err == nil {
				out = append(out, row)
			}
		}
		return c.JSON(fiber.Map{"tickets": out})
	})

	// Single ticket — verifies tenant + device scope before returning.
	portalAPI.Get("/tickets/:id", func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		deviceID, _ := c.Locals("customer_device_id").(string)
		ticketID := c.Params("id")
		var (
			title, status, priority, category string
			description                       sql.NullString
			ticketDeviceID                    sql.NullString
			createdAt, updatedAt              int64
		)
		err := db.DB.QueryRow(`SELECT title, description, status, priority, device_id, created_at, updated_at, category FROM tickets WHERE id = ? AND tenant_id = ?`, ticketID, tenantID).
			Scan(&title, &description, &status, &priority, &ticketDeviceID, &createdAt, &updatedAt, &category)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ticket not found"})
		}
		if deviceID != "" && (!ticketDeviceID.Valid || ticketDeviceID.String != deviceID) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ticket not found"})
		}
		return c.JSON(fiber.Map{
			"id": ticketID, "title": title, "description": description.String, "status": status,
			"priority": priority, "device_id": ticketDeviceID.String, "category": category,
			"created_at": createdAt, "updated_at": updatedAt,
		})
	})

	// Per-customer rate limit on ticket creation: 10 / hour, plus a
	// stricter 3 / minute burst guard. In-memory only; multi-server
	// would need Redis fan-out (Stage 16). Keys via the customer_id
	// local that PortalAuthMiddleware sets.
	portalAPI.Post("/tickets", portalCustomerRateLimiter(10, time.Hour), portalCustomerRateLimiter(3, time.Minute), func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		customerID, _ := c.Locals("customer_id").(string)
		deviceID, _ := c.Locals("customer_device_id").(string)
		var req struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    string `json:"priority"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		req.Title = strings.TrimSpace(req.Title)
		if req.Title == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "title required"})
		}
		if len(req.Title) > maxPortalTicketTitleLen {
			req.Title = req.Title[:maxPortalTicketTitleLen]
		}
		if len(req.Description) > maxPortalTicketDescLen {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "description too long"})
		}
		// Customers can't set priority freely (helps avoid every ticket
		// being "critical"). Always start at "medium"; admin can re-rank.
		_ = req.Priority
		ticketID := uuid.New().String()
		now := time.Now().Unix()
		// device_id auto-attached if customer is single-device-scoped.
		var devCol interface{}
		if deviceID != "" {
			devCol = deviceID
		}
		if _, err := db.DB.Exec(`INSERT INTO tickets (id, title, description, status, priority, device_id, created_at, updated_at, category, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ticketID, req.Title, req.Description, "open", "medium", devCol, now, now, "portal", tenantID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create ticket"})
		}
		events.AuditLogTenant(tenantID, customerID, "portal.ticket.create", "ticket", ticketID, "portal-submitted ticket", c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": ticketID})
	})

	// Comments — internal=0 only on read; force internal=false on write.
	portalAPI.Get("/tickets/:id/comments", func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		deviceID, _ := c.Locals("customer_device_id").(string)
		ticketID := c.Params("id")
		// Verify the customer can see this ticket.
		var ticketDeviceID sql.NullString
		if err := db.DB.QueryRow(`SELECT device_id FROM tickets WHERE id = ? AND tenant_id = ?`, ticketID, tenantID).Scan(&ticketDeviceID); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ticket not found"})
		}
		if deviceID != "" && (!ticketDeviceID.Valid || ticketDeviceID.String != deviceID) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ticket not found"})
		}
		rows, err := db.DB.Query(`SELECT id, body, created_at FROM ticket_comments WHERE ticket_id = ? AND tenant_id = ? AND internal = 0 ORDER BY created_at ASC LIMIT 1000`, ticketID, tenantID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query comments"})
		}
		defer rows.Close()
		type cmt struct {
			ID        string `json:"id"`
			Body      string `json:"body"`
			CreatedAt int64  `json:"created_at"`
		}
		out := []cmt{}
		for rows.Next() {
			var c cmt
			if err := rows.Scan(&c.ID, &c.Body, &c.CreatedAt); err == nil {
				out = append(out, c)
			}
		}
		return c.JSON(fiber.Map{"comments": out})
	})

	portalAPI.Post("/tickets/:id/comments", portalCustomerRateLimiter(30, time.Hour), func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		customerID, _ := c.Locals("customer_id").(string)
		deviceID, _ := c.Locals("customer_device_id").(string)
		ticketID := c.Params("id")
		var req struct {
			Body string `json:"body"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		if strings.TrimSpace(req.Body) == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "body required"})
		}
		if len(req.Body) > maxPortalCommentLen {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "comment too long"})
		}
		// Scope check.
		var ticketDeviceID sql.NullString
		if err := db.DB.QueryRow(`SELECT device_id FROM tickets WHERE id = ? AND tenant_id = ?`, ticketID, tenantID).Scan(&ticketDeviceID); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ticket not found"})
		}
		if deviceID != "" && (!ticketDeviceID.Valid || ticketDeviceID.String != deviceID) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Ticket not found"})
		}
		commentID := uuid.New().String()
		// Customer comments are NEVER internal — flag is forced 0 here so
		// the column-level filter on portal GET can stay simple.
		if _, err := db.DB.Exec(`INSERT INTO ticket_comments (id, ticket_id, tenant_id, user_id, body, internal, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			commentID, ticketID, tenantID, customerID, req.Body, 0, time.Now().Unix()); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to add comment"})
		}
		_, _ = db.DB.Exec(`UPDATE tickets SET updated_at = ? WHERE id = ?`, time.Now().Unix(), ticketID)
		events.AuditLogTenant(tenantID, customerID, "portal.ticket.comment", "ticket", ticketID, "portal-submitted comment", c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": commentID})
	})

	// Admin-side customer CRUD lives on the admin chain — see
	// RegisterCustomerAdminAPI, which main.go calls separately so it
	// inherits AuthMiddleware + CSRFMiddleware.
	_ = app
}

// RegisterCustomerAdminAPI is called from main.go on the admin api group
// so it inherits AuthMiddleware + CSRFMiddleware. Keep separate from
// RegisterPortalRoutes to make scope obvious at the call site.
func RegisterCustomerAdminAPI(api fiber.Router) {
	api.Get("/admin/customers", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tenantID := callerTenantID(c)
		role, _ := c.Locals("user_role").(string)
		var rows *sql.Rows
		var err error
		if auth.IsSuperAdmin(role) {
			rows, err = db.DB.Query(`SELECT id, tenant_id, email, name, COALESCE(device_id,''), disabled, COALESCE(last_login,0), created_at FROM customer_users ORDER BY created_at DESC LIMIT 1000`)
		} else {
			rows, err = db.DB.Query(`SELECT id, tenant_id, email, name, COALESCE(device_id,''), disabled, COALESCE(last_login,0), created_at FROM customer_users WHERE tenant_id = ? ORDER BY created_at DESC LIMIT 1000`, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query customers"})
		}
		defer rows.Close()
		type cu struct {
			ID        string `json:"id"`
			TenantID  string `json:"tenant_id"`
			Email     string `json:"email"`
			Name      string `json:"name"`
			DeviceID  string `json:"device_id,omitempty"`
			Disabled  bool   `json:"disabled"`
			LastLogin int64  `json:"last_login,omitempty"`
			CreatedAt int64  `json:"created_at"`
		}
		out := []cu{}
		for rows.Next() {
			var u cu
			var disabled int
			if err := rows.Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.DeviceID, &disabled, &u.LastLogin, &u.CreatedAt); err == nil {
				u.Disabled = disabled == 1
				out = append(out, u)
			}
		}
		return c.JSON(fiber.Map{"customers": out})
	})

	api.Post("/admin/customers", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Password string `json:"password"`
			DeviceID string `json:"device_id,omitempty"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		req.Email = strings.ToLower(strings.TrimSpace(req.Email))
		req.Name = strings.TrimSpace(req.Name)
		if req.Email == "" || req.Name == "" || req.Password == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email, name, password required"})
		}
		if _, err := mail.ParseAddress(req.Email); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid email"})
		}
		if err := auth.ValidatePasswordStrength(req.Password); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		tenantID := callerTenantID(c)
		// Optional device link must be in same tenant.
		if req.DeviceID != "" {
			tf, tArgs := tenantFilter(c)
			args := append([]interface{}{req.DeviceID}, tArgs...)
			var ok int
			if err := db.DB.QueryRow(`SELECT 1 FROM devices WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "device_id not in tenant"})
			}
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "hash failed"})
		}
		id := uuid.New().String()
		var devCol interface{}
		if req.DeviceID != "" {
			devCol = req.DeviceID
		}
		if _, err := db.DB.Exec(`INSERT INTO customer_users (id, tenant_id, email, name, password_hash, device_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, tenantID, req.Email, req.Name, string(hash), devCol, time.Now().Unix()); err != nil {
			slog.Warn("customer insert failed", "error", err)
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already registered"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "customer.create", "customer", id, fmt.Sprintf("created customer %s", req.Email), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
	})

	api.Put("/admin/customers/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM customer_users WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "customer not found"})
		}
		var req struct {
			Name     string `json:"name,omitempty"`
			Disabled *bool  `json:"disabled,omitempty"`
			Password string `json:"password,omitempty"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		if req.Name != "" {
			_, _ = db.DB.Exec(`UPDATE customer_users SET name = ? WHERE id = ?`, req.Name, id)
		}
		if req.Disabled != nil {
			d := 0
			if *req.Disabled {
				d = 1
			}
			_, _ = db.DB.Exec(`UPDATE customer_users SET disabled = ? WHERE id = ?`, d, id)
		}
		if req.Password != "" {
			if err := auth.ValidatePasswordStrength(req.Password); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "hash failed"})
			}
			_, _ = db.DB.Exec(`UPDATE customer_users SET password_hash = ? WHERE id = ?`, string(hash), id)
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "customer.update", "customer", id, "updated customer", c.IP())
		return c.JSON(fiber.Map{"message": "updated"})
	})

	api.Delete("/admin/customers/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM customer_users WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "customer not found"})
		}
		if _, err := db.DB.Exec(`DELETE FROM customer_users WHERE id = ?`, id); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "customer.delete", "customer", id, "deleted customer", c.IP())
		return c.JSON(fiber.Map{"message": "deleted"})
	})
}
