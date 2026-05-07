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

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/redis"
	"vaporrmm/server/internal/utils"
)

type loginAttempt struct {
	count     int
	lastTime  time.Time
	blockedAt time.Time
}

var (
	loginAttempts  = make(map[string]*loginAttempt)
	ipAttempts     = make(map[string]*loginAttempt)
	loginMu        sync.Mutex
	maxAttempts    = 5
	blockDuration  = 15 * time.Minute
	windowDuration = 5 * time.Minute
	ipMaxAttempts  = 20
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
		loginMu.Lock()
		ipAttempt, ipExists := ipAttempts[clientIP]
		now := time.Now()
		if ipExists {
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
			if !attempt.blockedAt.IsZero() && now.Sub(attempt.blockedAt) < blockDuration {
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

		var userID, email, name, passwordHash string
		err := db.DB.QueryRow("SELECT id, email, name, password_hash FROM users WHERE email = ?", req.Email).Scan(&userID, &email, &name, &passwordHash)
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

		token, err := auth.GenerateJWT(userID, role, 24)
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
			Secure:   os.Getenv("SERVER_CERT") != "",
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
			Secure:   os.Getenv("SERVER_CERT") != "",
			SameSite: "Strict",
			MaxAge:   cfg.DefaultCookieMaxAge,
			Path:     "/",
		})

		events.AuditLog(userID, "auth.login", "user", userID, fmt.Sprintf("login from %s", c.IP()), c.IP())
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

	// Forgot password
	publicAPI.Post("/auth/forgot-password", func(c *fiber.Ctx) error {
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
		// TODO: Send reset token via email instead of returning it in the response.
		// For development/testing, the token is logged server-side.
		slog.Info("password reset token generated", "email", req.Email, "token_hash", tokenHash)
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
		var userID string
		var expiresAt int64
		err := db.DB.QueryRow(`SELECT user_id, expires_at FROM password_resets WHERE token_hash = ? AND used = 0`, tokenHash).Scan(&userID, &expiresAt)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid or expired token"})
		}
		if time.Now().Unix() > expiresAt {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Token has expired"})
		}
		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
		}
		if _, err := db.DB.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(newHash), userID); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
		if _, err := db.DB.Exec("UPDATE password_resets SET used = 1 WHERE token_hash = ?", tokenHash); err != nil {
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
		events.AuditLog(userID, "session.revoke_all", "session", "", "revoked all other sessions", c.IP())
		return c.JSON(fiber.Map{"message": "Other sessions revoked"})
	})

	// User management (admin only)
	api.Get("/users", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		rows, err := db.DB.Query(`SELECT id, email, name, role, created_at, last_login FROM users ORDER BY created_at DESC`)
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
			ID        string `json:"id"`
			Email     string `json:"email"`
			Name      string `json:"name"`
			Role      string `json:"role"`
			CreatedAt int64  `json:"created_at"`
		}
		err := db.DB.QueryRow(`SELECT id, email, name, role, created_at FROM users WHERE id = ?`, userID).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
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
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
		}
		if req.Role == "" {
			req.Role = "admin"
		}
		if req.Role != "admin" && req.Role != "user" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Role must be admin or user"})
		}
		userID := uuid.New().String()
		_, err = db.DB.Exec(`INSERT INTO users (id, email, password_hash, name, role, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			userID, req.Email, string(passwordHash), req.Name, req.Role, time.Now().Unix())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create user", "message": err.Error()})
		}
		adminID, _ := c.Locals("user_id").(string)
		events.AuditLog(adminID, "user.create", "user", userID, fmt.Sprintf("created user %s", req.Email), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": userID, "email": req.Email, "name": req.Name, "role": req.Role, "message": "User created successfully"})
	})

	api.Delete("/users/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		targetID := c.Params("id")
		adminID := c.Locals("user_id").(string)
		if targetID == adminID {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot delete yourself"})
		}
		result, err := db.DB.Exec(`DELETE FROM users WHERE id = ?`, targetID)
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
		events.AuditLog(adminID, "user.delete", "user", targetID, "deleted user", c.IP())
		return c.JSON(fiber.Map{"message": "User deleted successfully"})
	})
}
