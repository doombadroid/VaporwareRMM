package handlers

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/email"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/middleware"
	"vaporrmm/server/internal/redis"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Self-serve signup is off by default. Operator opts in by setting:
//   SIGNUP_OPEN=1                     completely open (rate-limited)
//   SIGNUP_INVITE_CODE=<secret>       require this string in the request body
// Either disabled, this endpoint returns 404 so it doesn't leak existence.

var signupSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

func RegisterSignupRoutes(publicAPI fiber.Router) {
	publicAPI.Post("/signup", auth.RateLimiter(5, time.Hour), func(c *fiber.Ctx) error {
		if !signupAllowed() {
			// 404 not 403, so the endpoint is invisible when disabled.
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Not found"})
		}
		var req struct {
			TenantName string `json:"tenant_name"`
			TenantSlug string `json:"tenant_slug"`
			AdminName  string `json:"admin_name"`
			AdminEmail string `json:"admin_email"`
			Password   string `json:"password"`
			InviteCode string `json:"invite_code"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		// Constant-time invite-code check when configured
		if expected := os.Getenv("SIGNUP_INVITE_CODE"); expected != "" {
			if !subtleEq(req.InviteCode, expected) {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Invalid invite code"})
			}
		}
		if req.TenantName == "" || req.AdminEmail == "" || req.Password == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "tenant_name, admin_email, password required"})
		}
		if !strings.Contains(req.AdminEmail, "@") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid email"})
		}
		if err := auth.ValidatePasswordStrength(req.Password); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if req.TenantSlug == "" {
			req.TenantSlug = autoSlug(req.TenantName)
		}
		if !signupSlugRe.MatchString(req.TenantSlug) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid tenant slug (3-64 lowercase alphanum + hyphens)"})
		}
		if reserved := map[string]bool{"www": true, "api": true, "admin": true, "default": true, "app": true, "auth": true, "signup": true, "login": true, "ws": true, "agent": true}; reserved[req.TenantSlug] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Reserved slug"})
		}

		// Reject if slug already used
		var existing int
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tenants WHERE slug = ?`, req.TenantSlug).Scan(&existing)
		if existing > 0 {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Tenant slug already taken"})
		}

		now := time.Now().Unix()
		tenantID := uuid.New().String()
		if _, err := db.DB.Exec(
			`INSERT INTO tenants (id, name, slug, plan, status, created_at, updated_at) VALUES (?, ?, ?, 'free', 'active', ?, ?)`,
			tenantID, req.TenantName, req.TenantSlug, now, now,
		); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create tenant", "message": err.Error()})
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			// Rollback tenant insert; we don't want orphan tenants.
			_, _ = db.DB.Exec(`DELETE FROM tenants WHERE id = ?`, tenantID)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
		}
		userID := uuid.New().String()
		if _, err := db.DB.Exec(
			`INSERT INTO users (id, email, password_hash, name, role, created_at, tenant_id) VALUES (?, ?, ?, ?, 'admin', ?, ?)`,
			userID, req.AdminEmail, string(hash), req.AdminName, now, tenantID,
		); err != nil {
			_, _ = db.DB.Exec(`DELETE FROM tenants WHERE id = ?`, tenantID)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create admin user", "message": err.Error()})
		}
		middleware.InvalidateSlugCache()
		events.AuditLogTenant(tenantID, userID, "tenant.signup", "tenant", tenantID, fmt.Sprintf("self-serve signup: %s", req.TenantName), c.IP())

		// Issue session immediately so the user lands authenticated.
		token, err := auth.GenerateJWT(userID, tenantID, "admin", 24)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to issue token"})
		}
		sessionID := uuid.New().String()
		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		if _, err := db.DB.Exec(
			`INSERT INTO user_sessions (id, user_id, token_hash, ip_address, user_agent, created_at, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			sessionID, userID, tokenHash, c.IP(), c.Get("User-Agent"), now, now,
		); err != nil {
			slog.Warn("could not record signup session", "error", err)
		}
		_ = redis.SetSession(tokenHash, userID, 24*time.Hour)

		secure := auth.CookieSecure(c)
		c.Cookie(&fiber.Cookie{Name: "auth_token", Value: token, HTTPOnly: true, Secure: secure, SameSite: "Strict", MaxAge: 86400, Path: "/"})
		c.Cookie(&fiber.Cookie{Name: "csrf_token", Value: auth.GenerateCSRFToken(), HTTPOnly: false, Secure: secure, SameSite: "Strict", MaxAge: 86400, Path: "/"})

		// Best-effort welcome email — failures don't block signup.
		baseURL := publicBaseURL(c)
		if err := email.Send(tenantID, req.AdminEmail, "Welcome to "+req.TenantName, fmt.Sprintf("Your tenant %s is ready.\n\nLog in: %s\n", req.TenantName, baseURL)); err != nil {
			slog.Info("welcome email skipped", "tenant_id", tenantID, "error", err)
		}

		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"tenant_id":   tenantID,
			"tenant_slug": req.TenantSlug,
			"admin_email": req.AdminEmail,
			"message":     "Tenant created",
		})
	})
}

func signupAllowed() bool {
	return os.Getenv("SIGNUP_OPEN") == "1" || os.Getenv("SIGNUP_INVITE_CODE") != ""
}

// subtleEq is a constant-time equality check (prevents timing oracles on the invite code).
func subtleEq(a, b string) bool {
	if len(a) != len(b) {
		// Still walk b to avoid revealing length difference too quickly
		v := byte(0)
		for i := 0; i < len(b); i++ {
			v |= b[i]
		}
		_ = v
		return false
	}
	v := byte(0)
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
