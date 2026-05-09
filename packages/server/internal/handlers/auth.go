package handlers

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/email"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/redis"
	"vaporrmm/server/internal/utils"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type loginAttempt struct {
	count     int
	lastTime  time.Time
	blockedAt time.Time
}

var (
	loginAttempts    = make(map[string]*loginAttempt)
	ipAttempts       = make(map[string]*loginAttempt)
	loginMu          sync.Mutex
	maxAttempts      = 5
	blockDuration    = 15 * time.Minute
	windowDuration   = 5 * time.Minute
	ipMaxAttempts    = 20
	ipWindowDuration = 5 * time.Minute
)

func RegisterAuthRoutes(publicAPI, api fiber.Router, cfg Config) {
	// Login
	publicAPI.Post("/auth/login", func(c *fiber.Ctx) error {
		var req models.LoginRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body", "message": "Email and password required"})
		}
		if req.Email == "" || req.Password == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Missing credentials", "message": "Email and password are required"})
		}
		if !strings.Contains(req.Email, "@") || !strings.Contains(req.Email, ".") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid email", "message": "Please provide a valid email address"})
		}

		clientIP := c.IP()
		bypassLimits := os.Getenv("DISABLE_RATE_LIMIT") == "1"
		loginMu.Lock()
		ipAttempt, ipExists := ipAttempts[clientIP]
		now := time.Now()
		if !bypassLimits && ipExists {
			if now.Sub(ipAttempt.lastTime) > ipWindowDuration {
				ipAttempt.count = 0
			}
			if ipAttempt.count >= ipMaxAttempts {
				loginMu.Unlock()
				return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "Too many requests", "message": "Please try again later"})
			}
		} else {
			ipAttempts[clientIP] = &loginAttempt{}
		}

		attempt, exists := loginAttempts[req.Email]
		if exists {
			if now.Sub(attempt.lastTime) > windowDuration {
				attempt.count = 0
				attempt.blockedAt = time.Time{}
			}
			if !bypassLimits && !attempt.blockedAt.IsZero() && now.Sub(attempt.blockedAt) < blockDuration {
				loginMu.Unlock()
				return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "Too many login attempts", "message": "Account temporarily locked. Please try again later."})
			}
			if !attempt.blockedAt.IsZero() && now.Sub(attempt.blockedAt) >= blockDuration {
				attempt.count = 0
				attempt.blockedAt = time.Time{}
			}
		} else {
			attempt = &loginAttempt{}
			loginAttempts[req.Email] = attempt
		}
		loginMu.Unlock()

		var userID, email, name, passwordHash, tenantID string
		err := db.DB.QueryRow("SELECT id, email, name, password_hash, COALESCE(tenant_id,'default') FROM users WHERE email = ?", req.Email).Scan(&userID, &email, &name, &passwordHash, &tenantID)
		if err != nil {
			loginMu.Lock()
			attempt.count++
			attempt.lastTime = now
			if attempt.count >= maxAttempts {
				attempt.blockedAt = now
			}
			ipAttempts[clientIP].count++
			ipAttempts[clientIP].lastTime = now
			loginMu.Unlock()
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials", "message": "Email or password incorrect"})
		}

		if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
			loginMu.Lock()
			attempt.count++
			attempt.lastTime = now
			if attempt.count >= maxAttempts {
				attempt.blockedAt = now
			}
			ipAttempts[clientIP].count++
			ipAttempts[clientIP].lastTime = now
			loginMu.Unlock()
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials", "message": "Email or password incorrect"})
		}

		loginMu.Lock()
		delete(loginAttempts, req.Email)
		loginMu.Unlock()

		var role string
		if err := db.DB.QueryRow("SELECT role FROM users WHERE id = ?", userID).Scan(&role); err != nil {
			slog.Warn("db query row scan failed", "error", err)
		}
		if role == "" {
			role = "admin"
		}

		// If TOTP is enabled, return a short-lived challenge token instead of the full JWT.
		// The client must complete /auth/login/totp to get a real session.
		var totpEnabled int
		if err := db.DB.QueryRow(`SELECT enabled FROM user_totp WHERE user_id = ?`, userID).Scan(&totpEnabled); err == nil && totpEnabled == 1 {
			challenge, err := auth.GenerateTOTPChallenge(userID, tenantID)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate challenge"})
			}
			return c.JSON(fiber.Map{"requires_totp": true, "totp_challenge": challenge})
		}

		token, err := auth.GenerateJWT(userID, tenantID, role, 24)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
		}

		if _, err := db.DB.Exec("UPDATE users SET last_login = ? WHERE id = ?", time.Now().Unix(), userID); err != nil {
			slog.Warn("db exec failed", "error", err)
		}

		sessionID := uuid.New().String()
		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		if _, err := db.DB.Exec(
			`INSERT INTO user_sessions (id, user_id, token_hash, ip_address, user_agent, created_at, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			sessionID, userID, tokenHash, c.IP(), c.Get("User-Agent"), time.Now().Unix(), time.Now().Unix(),
		); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
		// Cache session in Redis for fast distributed lookups
		if err := redis.SetSession(tokenHash, userID, 24*time.Hour); err != nil {
			slog.Warn("failed to cache session in redis", "error", err)
		}

		cookie := &fiber.Cookie{
			Name:     "auth_token",
			Value:    token,
			HTTPOnly: true,
			Secure:   auth.CookieSecure(c),
			SameSite: "Strict",
			MaxAge:   cfg.DefaultCookieMaxAge,
			Path:     "/",
		}
		c.Cookie(cookie)

		csrfToken := auth.GenerateCSRFToken()
		c.Cookie(&fiber.Cookie{
			Name:     "csrf_token",
			Value:    csrfToken,
			HTTPOnly: false,
			Secure:   auth.CookieSecure(c),
			SameSite: "Strict",
			MaxAge:   cfg.DefaultCookieMaxAge,
			Path:     "/",
		})

		events.AuditLogTenant(tenantID, userID, "auth.login", "user", userID, fmt.Sprintf("login from %s", c.IP()), c.IP())
		return c.JSON(models.LoginResponse{Token: token, UserID: userID, Email: email, Name: name})
	})

	// Logout
	publicAPI.Post("/auth/logout", func(c *fiber.Ctx) error {
		token := c.Cookies("auth_token")
		if token == "" {
			authHeader := c.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}
		if token != "" {
			tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
			if _, err := db.DB.Exec(`DELETE FROM user_sessions WHERE token_hash = ?`, tokenHash); err != nil {
				slog.Warn("db exec failed", "error", err)
			}
			if err := redis.DeleteSession(tokenHash); err != nil {
				slog.Warn("failed to delete session from redis", "error", err)
			}
		}
		c.ClearCookie("auth_token")
		c.ClearCookie("csrf_token")
		return c.JSON(fiber.Map{"message": "Logged out"})
	})

	// Forgot password. Rate-limited to prevent: (a) account enumeration via
	// email-existence side channels (timing/log volume), (b) email-bombing a
	// single user, (c) generic spam abuse of the SMTP relay.
	publicAPI.Post("/auth/forgot-password", auth.RateLimiter(3, time.Hour), func(c *fiber.Ctx) error {
		var req struct {
			Email string `json:"email"`
		}
		if err := c.BodyParser(&req); err != nil || req.Email == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Email is required"})
		}
		var userID string
		err := db.DB.QueryRow("SELECT id FROM users WHERE email = ?", req.Email).Scan(&userID)
		if err != nil {
			return c.JSON(fiber.Map{"message": "If this email exists, a reset link has been sent"})
		}
		resetToken := utils.GenerateSecureKey()
		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(resetToken)))
		expiresAt := time.Now().Add(1 * time.Hour).Unix()
		if _, err := db.DB.Exec(
			`INSERT INTO password_resets (id, user_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
			uuid.New().String(), userID, tokenHash, expiresAt, time.Now().Unix(),
		); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
		baseURL := os.Getenv("PUBLIC_URL")
		if baseURL == "" {
			scheme := "http"
			if c.Protocol() == "https" || os.Getenv("SERVER_CERT") != "" {
				scheme = "https"
			}
			baseURL = scheme + "://" + c.Hostname()
		}
		if err := sendPasswordResetEmail(req.Email, resetToken, baseURL); err != nil {
			slog.Warn("failed to send password reset email", "email", req.Email, "error", err)
			slog.Info("password reset token stored", "email", req.Email, "token_hash", tokenHash)
		}
		return c.JSON(fiber.Map{"message": "If this email exists, a reset link has been sent"})
	})

	// Reset password
	publicAPI.Post("/auth/reset-password", func(c *fiber.Ctx) error {
		var req struct {
			Token       string `json:"token"`
			NewPassword string `json:"new_password"`
		}
		if err := c.BodyParser(&req); err != nil || req.Token == "" || req.NewPassword == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Token and new password are required"})
		}
		if err := auth.ValidatePasswordStrength(req.NewPassword); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.Token)))
		// Atomically claim the token. The conditional UPDATE is the single
		// race-safe gate: two concurrent requests with the same token can't
		// both observe used=0. RowsAffected==1 proves we're the winner.
		// expires_at is checked inside the same UPDATE so an expired-but-
		// unclaimed token can't be used.
		now := time.Now().Unix()
		res, err := db.DB.Exec(
			`UPDATE password_resets SET used = 1 WHERE token_hash = ? AND used = 0 AND expires_at >= ?`,
			tokenHash, now,
		)
		if err != nil {
			slog.Warn("db exec failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Internal error"})
		}
		affected, _ := res.RowsAffected()
		if affected != 1 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid or expired token"})
		}
		var userID string
		if err := db.DB.QueryRow(`SELECT user_id FROM password_resets WHERE token_hash = ?`, tokenHash).Scan(&userID); err != nil {
			slog.Warn("db query failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Internal error"})
		}
		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
		}
		if _, err := db.DB.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(newHash), userID); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
		if _, err := db.DB.Exec("DELETE FROM user_sessions WHERE user_id = ?", userID); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
		return c.JSON(fiber.Map{"message": "Password reset successfully"})
	})

	// Session management
	api.Get("/sessions", func(c *fiber.Ctx) error {
		userID := c.Locals("user_id").(string)
		rows, err := db.DB.Query(`SELECT id, ip_address, user_agent, created_at, last_seen FROM user_sessions WHERE user_id = ? ORDER BY last_seen DESC`, userID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query sessions"})
		}
		defer rows.Close()
		type session struct {
			ID        string `json:"id"`
			IPAddress string `json:"ip_address"`
			UserAgent string `json:"user_agent"`
			CreatedAt int64  `json:"created_at"`
			LastSeen  int64  `json:"last_seen"`
			Current   bool   `json:"current"`
		}
		sessions := []session{}
		for rows.Next() {
			var s session
			if err := rows.Scan(&s.ID, &s.IPAddress, &s.UserAgent, &s.CreatedAt, &s.LastSeen); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			sessions = append(sessions, s)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		return c.JSON(fiber.Map{"sessions": sessions})
	})

	api.Delete("/sessions/:id", func(c *fiber.Ctx) error {
		userID := c.Locals("user_id").(string)
		sessionID := c.Params("id")
		var tokenHash string
		if err := db.DB.QueryRow(`SELECT token_hash FROM user_sessions WHERE id = ? AND user_id = ?`, sessionID, userID).Scan(&tokenHash); err == nil && tokenHash != "" {
			if err := redis.DeleteSession(tokenHash); err != nil {
				slog.Warn("failed to delete session from redis", "error", err)
			}
		}
		_, err := db.DB.Exec(`DELETE FROM user_sessions WHERE id = ? AND user_id = ?`, sessionID, userID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to revoke session"})
		}
		return c.JSON(fiber.Map{"message": "Session revoked"})
	})

	api.Delete("/sessions", func(c *fiber.Ctx) error {
		userID := c.Locals("user_id").(string)
		var currentSessionID string
		token := c.Cookies("auth_token")
		if token == "" {
			authHeader := c.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}
		if token != "" {
			tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
			if err := db.DB.QueryRow(`SELECT id FROM user_sessions WHERE user_id = ? AND token_hash = ?`, userID, tokenHash).Scan(&currentSessionID); err != nil {
				slog.Warn("db query row scan failed", "error", err)
			}
		}
		if currentSessionID != "" {
			_, err := db.DB.Exec(`DELETE FROM user_sessions WHERE user_id = ? AND id != ?`, userID, currentSessionID)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to revoke sessions"})
			}
		}
		if err := redis.DeleteUserSessions(userID); err != nil {
			slog.Warn("failed to delete user sessions from redis", "error", err)
		}
		callerTenant, _ := c.Locals("tenant_id").(string)
		events.AuditLogTenant(callerTenant, userID, "session.revoke_all", "session", "", "revoked all other sessions", c.IP())
		return c.JSON(fiber.Map{"message": "Other sessions revoked"})
	})

	// User management (admin only)
	api.Get("/users", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		role, _ := c.Locals("user_role").(string)
		var rows *sql.Rows
		var err error
		if auth.IsSuperAdmin(role) {
			rows, err = db.DB.Query(`SELECT id, email, name, role, created_at, last_login FROM users ORDER BY created_at DESC`)
		} else {
			tenantID, _ := c.Locals("tenant_id").(string)
			if tenantID == "" {
				tenantID = "default"
			}
			rows, err = db.DB.Query(`SELECT id, email, name, role, created_at, last_login FROM users WHERE tenant_id = ? ORDER BY created_at DESC`, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query users"})
		}
		defer rows.Close()
		type user struct {
			ID        string `json:"id"`
			Email     string `json:"email"`
			Name      string `json:"name"`
			Role      string `json:"role"`
			CreatedAt int64  `json:"created_at"`
			LastLogin *int64 `json:"last_login,omitempty"`
		}
		users := []user{}
		for rows.Next() {
			var u user
			var lastLogin sql.NullInt64
			if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt, &lastLogin); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			if lastLogin.Valid {
				u.LastLogin = &lastLogin.Int64
			}
			users = append(users, u)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		return c.JSON(fiber.Map{"users": users})
	})

	api.Get("/users/me", func(c *fiber.Ctx) error {
		userID := c.Locals("user_id").(string)
		var u struct {
			ID               string `json:"id"`
			Email            string `json:"email"`
			Name             string `json:"name"`
			Role             string `json:"role"`
			CreatedAt        int64  `json:"created_at"`
			TenantID         string `json:"tenant_id"`
			TenantName       string `json:"tenant_name"`
			Impersonating    bool   `json:"impersonating"`
			OriginalRole     string `json:"original_role,omitempty"`
			OriginalTenantID string `json:"original_tenant_id,omitempty"`
		}
		err := db.DB.QueryRow(
			`SELECT u.id, u.email, u.name, u.role, u.created_at, COALESCE(u.tenant_id,'default'), COALESCE(t.name,'Default')
			   FROM users u LEFT JOIN tenants t ON t.id = u.tenant_id WHERE u.id = ?`, userID,
		).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt, &u.TenantID, &u.TenantName)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
		}
		// Override role + tenant_id with whatever's in the JWT, since we may
		// be in an impersonation session (role=admin in another tenant).
		if claimRole, ok := c.Locals("user_role").(string); ok && claimRole != "" {
			u.Role = claimRole
		}
		if claimTid, ok := c.Locals("tenant_id").(string); ok && claimTid != "" {
			u.TenantID = claimTid
			var name string
			if err := db.DB.QueryRow(`SELECT name FROM tenants WHERE id = ?`, claimTid).Scan(&name); err == nil && name != "" {
				u.TenantName = name
			}
		}
		// Detect impersonation by re-parsing the JWT for impersonation claims
		token := c.Cookies("auth_token")
		if token == "" {
			if h := c.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				token = strings.TrimPrefix(h, "Bearer ")
			}
		}
		if token != "" {
			if _, origTid, origRole, ok := auth.ParseImpersonationClaims(token); ok {
				u.Impersonating = true
				u.OriginalRole = origRole
				u.OriginalTenantID = origTid
			}
		}
		// Suspension grace banner (when applicable). Tenant is functional but
		// users should be told it's been suspended and when access ends.
		if inGrace, deadline := auth.TenantInGrace(u.TenantID); inGrace {
			return c.JSON(fiber.Map{
				"id":                 u.ID,
				"email":              u.Email,
				"name":               u.Name,
				"role":               u.Role,
				"created_at":         u.CreatedAt,
				"tenant_id":          u.TenantID,
				"tenant_name":        u.TenantName,
				"impersonating":      u.Impersonating,
				"original_role":      u.OriginalRole,
				"original_tenant_id": u.OriginalTenantID,
				"tenant_in_grace":    true,
				"grace_deadline":     deadline,
			})
		}
		return c.JSON(u)
	})

	api.Post("/users", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Name     string `json:"name"`
			Role     string `json:"role"`
		}
		if err := c.BodyParser(&req); err != nil || req.Email == "" || req.Password == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Email and password are required"})
		}
		if err := auth.ValidatePasswordStrength(req.Password); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
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
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid role"})
		}
		// New user inherits caller's tenant unless caller is super_admin and specifies one
		newUserTenant, _ := c.Locals("tenant_id").(string)
		if newUserTenant == "" {
			newUserTenant = "default"
		}
		// Enforce tenant user cap (0 = unlimited; super_admin bypasses)
		if !auth.IsSuperAdmin(callerRole) {
			var maxUsers int
			if err := db.DB.QueryRow(`SELECT COALESCE(max_users,0) FROM tenants WHERE id = ?`, newUserTenant).Scan(&maxUsers); err != nil {
				slog.Warn("could not read tenant user cap", "tenant_id", newUserTenant, "error", err)
			}
			if maxUsers > 0 {
				var count int
				if err := db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id = ?`, newUserTenant).Scan(&count); err == nil && count >= maxUsers {
					return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
						"error":   "User limit reached",
						"message": fmt.Sprintf("Tenant has reached its user cap (%d).", maxUsers),
					})
				}
			}
		}
		userID := uuid.New().String()
		_, err = db.DB.Exec(`INSERT INTO users (id, email, password_hash, name, role, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			userID, req.Email, string(passwordHash), req.Name, req.Role, time.Now().Unix(), newUserTenant)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create user", "message": err.Error()})
		}
		adminID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(newUserTenant, adminID, "user.create", "user", userID, fmt.Sprintf("created user %s", req.Email), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": userID, "email": req.Email, "name": req.Name, "role": req.Role, "message": "User created successfully"})
	})

	api.Put("/users/me/password", func(c *fiber.Ctx) error {
		userID := c.Locals("user_id").(string)
		var req struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if err := c.BodyParser(&req); err != nil || req.CurrentPassword == "" || req.NewPassword == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "current_password and new_password are required"})
		}
		if err := auth.ValidatePasswordStrength(req.NewPassword); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		var passwordHash string
		if err := db.DB.QueryRow("SELECT password_hash FROM users WHERE id = ?", userID).Scan(&passwordHash); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to load user"})
		}
		if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.CurrentPassword)); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Current password is incorrect"})
		}
		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
		}
		if _, err := db.DB.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(newHash), userID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update password"})
		}
		// Revoke all other sessions; keep the one making this request
		currentToken := c.Cookies("auth_token")
		if currentToken == "" {
			if h := c.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				currentToken = strings.TrimPrefix(h, "Bearer ")
			}
		}
		if currentToken != "" {
			currentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(currentToken)))
			if _, err := db.DB.Exec(`DELETE FROM user_sessions WHERE user_id = ? AND token_hash != ?`, userID, currentHash); err != nil {
				slog.Warn("db exec failed", "error", err)
			}
		}
		callerTenant, _ := c.Locals("tenant_id").(string)
		events.AuditLogTenant(callerTenant, userID, "auth.password_change", "user", userID, "password changed", c.IP())
		return c.JSON(fiber.Map{"message": "Password updated successfully"})
	})

	api.Delete("/users/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		targetID := c.Params("id")
		adminID := c.Locals("user_id").(string)
		if targetID == adminID {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot delete yourself"})
		}
		callerRole, _ := c.Locals("user_role").(string)
		var result sql.Result
		var err error
		if auth.IsSuperAdmin(callerRole) {
			result, err = db.DB.Exec(`DELETE FROM users WHERE id = ?`, targetID)
		} else {
			tenantID, _ := c.Locals("tenant_id").(string)
			if tenantID == "" {
				tenantID = "default"
			}
			result, err = db.DB.Exec(`DELETE FROM users WHERE id = ? AND tenant_id = ?`, targetID, tenantID)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete user"})
		}
		if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
		}
		if _, err := db.DB.Exec(`DELETE FROM user_sessions WHERE user_id = ?`, targetID); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
		if err := redis.DeleteUserSessions(targetID); err != nil {
			slog.Warn("failed to delete user sessions from redis", "error", err)
		}
		callerTenant, _ := c.Locals("tenant_id").(string)
		events.AuditLogTenant(callerTenant, adminID, "user.delete", "user", targetID, "deleted user", c.IP())
		return c.JSON(fiber.Map{"message": "User deleted successfully"})
	})
}

// sendPasswordResetEmail sends a password reset link via the central email package.
// Tenant SMTP is resolved by user_id; falls back to 'default' tenant SMTP.
func sendPasswordResetEmail(toEmail, plainToken, baseURL string) error {
	var userTenant string
	if err := db.DB.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM users WHERE email = ?`, toEmail).Scan(&userTenant); err != nil {
		userTenant = "default"
	}
	resetURL := fmt.Sprintf("%s/reset-password?token=%s", strings.TrimRight(baseURL, "/"), plainToken)
	return email.SendPasswordReset(userTenant, toEmail, resetURL)
}
