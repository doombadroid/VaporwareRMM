package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/email"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// inviteTTL is how long a tenant invite is valid before requiring a fresh send.
const inviteTTL = 7 * 24 * time.Hour

func RegisterInviteRoutes(publicAPI, api fiber.Router) {
	// List pending invites for the caller's tenant
	api.Get("/invites", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		role, _ := c.Locals("user_role").(string)
		var rows *sql.Rows
		var err error
		if auth.IsSuperAdmin(role) {
			rows, err = db.DB.Query(`SELECT id, tenant_id, email, role, invited_by, expires_at, accepted_at, created_at FROM user_invites ORDER BY created_at DESC`)
		} else {
			if tenantID == "" {
				tenantID = "default"
			}
			rows, err = db.DB.Query(`SELECT id, tenant_id, email, role, invited_by, expires_at, accepted_at, created_at FROM user_invites WHERE tenant_id = ? ORDER BY created_at DESC`, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query invites"})
		}
		defer rows.Close()
		type invite struct {
			ID         string `json:"id"`
			TenantID   string `json:"tenant_id"`
			Email      string `json:"email"`
			Role       string `json:"role"`
			InvitedBy  string `json:"invited_by"`
			ExpiresAt  int64  `json:"expires_at"`
			AcceptedAt *int64 `json:"accepted_at,omitempty"`
			CreatedAt  int64  `json:"created_at"`
			Status     string `json:"status"`
		}
		out := []invite{}
		now := time.Now().Unix()
		for rows.Next() {
			var i invite
			var accepted sql.NullInt64
			if err := rows.Scan(&i.ID, &i.TenantID, &i.Email, &i.Role, &i.InvitedBy, &i.ExpiresAt, &accepted, &i.CreatedAt); err != nil {
				slog.Warn("invite scan failed", "error", err)
				continue
			}
			if accepted.Valid {
				i.AcceptedAt = &accepted.Int64
				i.Status = "accepted"
			} else if i.ExpiresAt < now {
				i.Status = "expired"
			} else {
				i.Status = "pending"
			}
			out = append(out, i)
		}
		return c.JSON(fiber.Map{"invites": out})
	})

	// Create + send invite. Tenant_admin invites into their own tenant; super_admin can target any tenant.
	api.Post("/invites", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Email    string `json:"email"`
			Role     string `json:"role"`
			TenantID string `json:"tenant_id,omitempty"` // super_admin only
		}
		if err := c.BodyParser(&req); err != nil || req.Email == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email is required"})
		}
		if !strings.Contains(req.Email, "@") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid email"})
		}
		if req.Role == "" {
			req.Role = "user"
		}
		callerRole, _ := c.Locals("user_role").(string)
		validRoles := map[string]bool{"admin": true, "user": true}
		if auth.IsSuperAdmin(callerRole) {
			validRoles["super_admin"] = true
		}
		if !validRoles[req.Role] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid role"})
		}
		// Resolve target tenant.
		callerTenant, _ := c.Locals("tenant_id").(string)
		if callerTenant == "" {
			callerTenant = "default"
		}
		targetTenant := callerTenant
		if auth.IsSuperAdmin(callerRole) && req.TenantID != "" {
			targetTenant = req.TenantID
		}
		// Reject if email already exists in target tenant.
		var existing int
		if err := db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ? AND tenant_id = ?`, req.Email, targetTenant).Scan(&existing); err == nil && existing > 0 {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "user already exists in this tenant"})
		}
		// Enforce tenant user cap.
		if !auth.IsSuperAdmin(callerRole) {
			var maxUsers int
			_ = db.DB.QueryRow(`SELECT COALESCE(max_users,0) FROM tenants WHERE id = ?`, targetTenant).Scan(&maxUsers)
			if maxUsers > 0 {
				var userCount, pendingInvites int
				_ = db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id = ?`, targetTenant).Scan(&userCount)
				_ = db.DB.QueryRow(`SELECT COUNT(*) FROM user_invites WHERE tenant_id = ? AND accepted_at IS NULL AND expires_at > ?`, targetTenant, time.Now().Unix()).Scan(&pendingInvites)
				if userCount+pendingInvites >= maxUsers {
					return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": fmt.Sprintf("user cap (%d) would be exceeded by accepted users + pending invites", maxUsers)})
				}
			}
		}

		token, err := newInviteToken()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
		}
		tokenHash := hashInviteToken(token)
		inviteID := uuid.New().String()
		now := time.Now().Unix()
		expiresAt := time.Now().Add(inviteTTL).Unix()
		inviterID, _ := c.Locals("user_id").(string)

		if _, err := db.DB.Exec(
			`INSERT INTO user_invites (id, tenant_id, email, role, token_hash, invited_by, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			inviteID, targetTenant, req.Email, req.Role, tokenHash, inviterID, expiresAt, now,
		); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create invite", "message": err.Error()})
		}

		// Best-effort send the email; failure logs but invite still exists for resend / manual link share.
		baseURL := publicBaseURL(c)
		acceptURL := fmt.Sprintf("%s/accept-invite?token=%s", strings.TrimRight(baseURL, "/"), url.QueryEscape(token))

		var inviterName, tenantName string
		_ = db.DB.QueryRow(`SELECT name FROM users WHERE id = ?`, inviterID).Scan(&inviterName)
		if inviterName == "" {
			inviterName = "An administrator"
		}
		_ = db.DB.QueryRow(`SELECT name FROM tenants WHERE id = ?`, targetTenant).Scan(&tenantName)
		if tenantName == "" {
			tenantName = targetTenant
		}
		if err := email.SendInvite(targetTenant, req.Email, inviterName, tenantName, acceptURL); err != nil {
			slog.Warn("invite email failed", "tenant_id", targetTenant, "email", req.Email, "error", err)
			// Surface the link to the caller so they can deliver out-of-band.
			c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
			events.AuditLogTenant(targetTenant, inviterID, "invite.create", "user_invite", inviteID, fmt.Sprintf("invited %s as %s (email failed)", req.Email, req.Role), c.IP())
			return c.Status(fiber.StatusCreated).JSON(fiber.Map{
				"id": inviteID, "email": req.Email, "role": req.Role,
				"accept_url": acceptURL,
				"warning":    "email delivery failed; share the accept_url manually",
			})
		}

		events.AuditLogTenant(targetTenant, inviterID, "invite.create", "user_invite", inviteID, fmt.Sprintf("invited %s as %s", req.Email, req.Role), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": inviteID, "email": req.Email, "role": req.Role, "message": "invite sent"})
	})

	// Revoke a pending invite
	api.Delete("/invites/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		callerRole, _ := c.Locals("user_role").(string)
		callerTenant, _ := c.Locals("tenant_id").(string)
		var err error
		if auth.IsSuperAdmin(callerRole) {
			_, err = db.DB.Exec(`DELETE FROM user_invites WHERE id = ?`, id)
		} else {
			_, err = db.DB.Exec(`DELETE FROM user_invites WHERE id = ? AND tenant_id = ?`, id, callerTenant)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to revoke invite"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenant, userID, "invite.revoke", "user_invite", id, "revoked invite", c.IP())
		return c.JSON(fiber.Map{"message": "Invite revoked"})
	})

	// Look up invite metadata by token (no auth — invitee uses this to render the accept form)
	publicAPI.Get("/invites/preview", func(c *fiber.Ctx) error {
		token := c.Query("token")
		if token == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "token required"})
		}
		tokenHash := hashInviteToken(token)
		var (
			inviteEmail, inviteRole, tenantID, tenantName string
			expiresAt                                     int64
			accepted                                      sql.NullInt64
		)
		err := db.DB.QueryRow(
			`SELECT i.email, i.role, i.tenant_id, COALESCE(t.name,''), i.expires_at, i.accepted_at
			   FROM user_invites i LEFT JOIN tenants t ON t.id = i.tenant_id
			  WHERE i.token_hash = ?`, tokenHash,
		).Scan(&inviteEmail, &inviteRole, &tenantID, &tenantName, &expiresAt, &accepted)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "invite not found"})
		}
		if accepted.Valid {
			return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "invite already used"})
		}
		if expiresAt < time.Now().Unix() {
			return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "invite expired"})
		}
		return c.JSON(fiber.Map{
			"email":       inviteEmail,
			"role":        inviteRole,
			"tenant_id":   tenantID,
			"tenant_name": tenantName,
		})
	})

	// Accept invite + create user account
	publicAPI.Post("/invites/accept", func(c *fiber.Ctx) error {
		var req struct {
			Token    string `json:"token"`
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if err := c.BodyParser(&req); err != nil || req.Token == "" || req.Password == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "token, name, and password are required"})
		}
		if err := auth.ValidatePasswordStrength(req.Password); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		tokenHash := hashInviteToken(req.Token)
		var (
			inviteID, inviteEmail, inviteRole, tenantID string
			expiresAt                                   int64
			accepted                                    sql.NullInt64
		)
		err := db.DB.QueryRow(
			`SELECT id, email, role, tenant_id, expires_at, accepted_at FROM user_invites WHERE token_hash = ?`, tokenHash,
		).Scan(&inviteID, &inviteEmail, &inviteRole, &tenantID, &expiresAt, &accepted)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "invite not found"})
		}
		if accepted.Valid {
			return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "invite already used"})
		}
		if expiresAt < time.Now().Unix() {
			return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "invite expired"})
		}

		// Reject if a user with this email already exists in the tenant (prevents accidental dup-create after manual creation)
		var existing int
		if err := db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ? AND tenant_id = ?`, inviteEmail, tenantID).Scan(&existing); err == nil && existing > 0 {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "user already exists; ask an admin to revoke the invite"})
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
		}
		userID := uuid.New().String()
		now := time.Now().Unix()
		if _, err := db.DB.Exec(
			`INSERT INTO users (id, email, password_hash, name, role, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			userID, inviteEmail, string(hash), req.Name, inviteRole, now, tenantID,
		); err != nil {
			// The unique index on (email, tenant_id) guarantees we don't double-create
			// even on concurrent invite-accept races; turn the DB error into 409.
			es := strings.ToLower(err.Error())
			if strings.Contains(es, "unique") || strings.Contains(es, "duplicate") {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "user already exists; ask an admin to revoke the invite"})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create user", "message": err.Error()})
		}
		if _, err := db.DB.Exec(`UPDATE user_invites SET accepted_at = ? WHERE id = ?`, now, inviteID); err != nil {
			slog.Warn("could not mark invite accepted", "invite_id", inviteID, "error", err)
		}
		events.AuditLogTenant(tenantID, userID, "invite.accept", "user", userID, "accepted invite, account created", c.IP())
		return c.JSON(fiber.Map{"message": "Account created. You can now log in.", "email": inviteEmail})
	})
}

func newInviteToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// publicBaseURL returns the URL the dashboard is hosted on, preferring PUBLIC_URL.
func publicBaseURL(c *fiber.Ctx) string {
	if u := os.Getenv("PUBLIC_URL"); u != "" {
		return u
	}
	scheme := "http"
	if c.Protocol() == "https" || os.Getenv("SERVER_CERT") != "" {
		scheme = "https"
	}
	return scheme + "://" + c.Hostname()
}
