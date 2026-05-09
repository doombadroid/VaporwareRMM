package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/redis"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
)

// generateBackupCodes creates 8 single-use recovery codes (format XXXXXXXX-XXXXXXXX).
// Returns an error on crypto/rand failure so the HTTP handler can produce a
// clean 500 instead of a panic that the recover middleware catches mid-write.
func generateBackupCodes() ([]string, error) {
	codes := make([]string, 8)
	for i := range codes {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("backup-code rand: %w", err)
		}
		codes[i] = fmt.Sprintf("%X-%X", b[:4], b[4:])
	}
	return codes, nil
}

func RegisterTOTPRoutes(publicAPI, api fiber.Router, cfg Config) {
	// TOTP status for the current user
	api.Get("/auth/totp/status", func(c *fiber.Ctx) error {
		userID := c.Locals("user_id").(string)
		var enabled int
		if err := db.DB.QueryRow(`SELECT enabled FROM user_totp WHERE user_id = ?`, userID).Scan(&enabled); err != nil {
			return c.JSON(fiber.Map{"enabled": false})
		}
		return c.JSON(fiber.Map{"enabled": enabled == 1})
	})

	// Generate a new TOTP secret (pending — not active until enable is called)
	api.Post("/auth/totp/setup", func(c *fiber.Ctx) error {
		userID := c.Locals("user_id").(string)

		var email string
		if err := db.DB.QueryRow(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to load user"})
		}

		key, err := totp.Generate(totp.GenerateOpts{
			Issuer:      "vaporRMM",
			AccountName: email,
			SecretSize:  20,
		})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate TOTP secret"})
		}

		// Fail closed on encryption error. The previous behaviour wrote the
		// plaintext TOTP secret to the DB on encrypt failure, which would
		// expose every user's second factor on a DB compromise. Operators
		// running the explicit DEV_ALLOW_UNENCRYPTED_SECRETS=1 path get
		// crypto.Enabled()==false; let them through, but never silently
		// downgrade.
		var encSecret string
		if crypto.Enabled() {
			out, encErr := crypto.Encrypt(key.Secret())
			if encErr != nil {
				slog.Error("failed to encrypt totp secret", "error", encErr)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to store TOTP secret"})
			}
			encSecret = out
		} else {
			encSecret = key.Secret()
		}

		var upsert string
		if db.DB.Dialect == "postgres" {
			upsert = `INSERT INTO user_totp (user_id, secret, enabled, created_at)
				VALUES (?, ?, 0, ?)
				ON CONFLICT (user_id) DO UPDATE SET secret = EXCLUDED.secret, enabled = 0, created_at = EXCLUDED.created_at`
		} else {
			upsert = `INSERT OR REPLACE INTO user_totp (user_id, secret, enabled, created_at) VALUES (?, ?, 0, ?)`
		}
		now := time.Now().Unix()
		if _, err := db.DB.Exec(upsert, userID, encSecret, now); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to store TOTP secret"})
		}

		// Replace backup codes each time setup is initiated
		if _, err := db.DB.Exec(`DELETE FROM user_totp_backup_codes WHERE user_id = ?`, userID); err != nil {
			slog.Warn("failed to clear old backup codes", "error", err)
		}
		plainCodes, codeErr := generateBackupCodes()
		if codeErr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate backup codes"})
		}
		for _, code := range plainCodes {
			codeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(code)))
			if _, err := db.DB.Exec(
				`INSERT INTO user_totp_backup_codes (id, user_id, code_hash, used, created_at) VALUES (?, ?, ?, 0, ?)`,
				uuid.New().String(), userID, codeHash, now,
			); err != nil {
				slog.Warn("failed to store backup code", "error", err)
			}
		}

		return c.JSON(fiber.Map{"uri": key.URL(), "secret": key.Secret(), "backup_codes": plainCodes})
	})

	// Verify a TOTP code against the pending secret and mark as enabled
	api.Post("/auth/totp/enable", auth.RateLimiter(5, time.Minute), func(c *fiber.Ctx) error {
		userID := c.Locals("user_id").(string)
		var req struct {
			Code string `json:"code"`
		}
		if err := c.BodyParser(&req); err != nil || len(req.Code) != 6 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "6-digit code is required"})
		}

		var encSecret string
		var enabled int
		if err := db.DB.QueryRow(`SELECT secret, enabled FROM user_totp WHERE user_id = ?`, userID).Scan(&encSecret, &enabled); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "No TOTP setup in progress — call /auth/totp/setup first"})
		}
		if enabled == 1 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "TOTP already enabled"})
		}

		secret, err := crypto.Decrypt(encSecret)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to read TOTP secret"})
		}
		if !totp.Validate(req.Code, secret) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid TOTP code"})
		}

		if _, err := db.DB.Exec(`UPDATE user_totp SET enabled = 1, enabled_at = ? WHERE user_id = ?`, time.Now().Unix(), userID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to enable TOTP"})
		}

		// Invalidate all existing sessions — they were created before TOTP was required
		if _, err := db.DB.Exec(`DELETE FROM user_sessions WHERE user_id = ?`, userID); err != nil {
			slog.Warn("failed to invalidate sessions after totp enable", "error", err)
		}
		if err := redis.DeleteUserSessions(userID); err != nil {
			slog.Warn("failed to purge redis sessions after totp enable", "error", err)
		}

		callerTenant, _ := c.Locals("tenant_id").(string)
		events.AuditLogTenant(callerTenant, userID, "totp.enabled", "user", userID, "two-factor authentication enabled", c.IP())
		return c.JSON(fiber.Map{"message": "Two-factor authentication enabled"})
	})

	// Verify current TOTP code then disable TOTP for this user. Rate-limited
	// matching /enable so a session-cookie attacker can't brute-force the
	// 6-digit TOTP code (1M codes / unlimited rate = ~17min) to remove the
	// second factor on a victim account.
	api.Post("/auth/totp/disable", auth.RateLimiter(5, time.Minute), func(c *fiber.Ctx) error {
		userID := c.Locals("user_id").(string)
		var req struct {
			Code string `json:"code"`
		}
		if err := c.BodyParser(&req); err != nil || len(req.Code) != 6 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "6-digit code is required"})
		}

		var encSecret string
		var enabled int
		if err := db.DB.QueryRow(`SELECT secret, enabled FROM user_totp WHERE user_id = ?`, userID).Scan(&encSecret, &enabled); err != nil || enabled == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "TOTP is not enabled"})
		}

		secret, err := crypto.Decrypt(encSecret)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to read TOTP secret"})
		}
		if !totp.Validate(req.Code, secret) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid TOTP code"})
		}

		if _, err := db.DB.Exec(`DELETE FROM user_totp WHERE user_id = ?`, userID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to disable TOTP"})
		}

		callerTenant, _ := c.Locals("tenant_id").(string)
		events.AuditLogTenant(callerTenant, userID, "totp.disabled", "user", userID, "two-factor authentication disabled", c.IP())
		return c.JSON(fiber.Map{"message": "Two-factor authentication disabled"})
	})

	// Second factor: validate TOTP challenge token + code, issue full session
	publicAPI.Post("/auth/login/totp", auth.RateLimiter(5, time.Minute), func(c *fiber.Ctx) error {
		var req struct {
			TotpChallenge string `json:"totp_challenge"`
			Code          string `json:"code"`
		}
		if err := c.BodyParser(&req); err != nil || req.TotpChallenge == "" || req.Code == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "totp_challenge and code are required"})
		}

		userID, tenantID, role, err := auth.ValidateJWT(req.TotpChallenge)
		if err != nil || role != "totp_pending" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid or expired challenge"})
		}

		var encSecret string
		if err := db.DB.QueryRow(`SELECT secret FROM user_totp WHERE user_id = ? AND enabled = 1`, userID).Scan(&encSecret); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "TOTP not configured for this account"})
		}
		secret, err := crypto.Decrypt(encSecret)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to read TOTP secret"})
		}

		// Accept 6-digit TOTP code or XXXXXXXX-XXXXXXXX backup code
		code := strings.ToUpper(strings.ReplaceAll(req.Code, " ", ""))
		if len(strings.ReplaceAll(code, "-", "")) == 16 {
			// Backup code path. Use a single conditional UPDATE so two concurrent
			// requests with the same code can never both succeed: only the first
			// to flip used=0→1 will see RowsAffected==1.
			codeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(code)))
			res, err := db.DB.Exec(
				`UPDATE user_totp_backup_codes SET used = 1, used_at = ? WHERE user_id = ? AND code_hash = ? AND used = 0`,
				time.Now().Unix(), userID, codeHash,
			)
			if err != nil {
				slog.Warn("failed to consume backup code", "error", err)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
			}
			affected, _ := res.RowsAffected()
			if affected != 1 {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid or already-used backup code"})
			}
		} else if !totp.Validate(req.Code, secret) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid TOTP code"})
		}

		var actualRole, email, name string
		if err := db.DB.QueryRow(`SELECT role, email, name FROM users WHERE id = ?`, userID).Scan(&actualRole, &email, &name); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to load user"})
		}

		token, err := auth.GenerateJWT(userID, tenantID, actualRole, 24)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
		}

		sessionID := uuid.New().String()
		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		if _, err := db.DB.Exec(
			`INSERT INTO user_sessions (id, user_id, token_hash, ip_address, user_agent, created_at, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			sessionID, userID, tokenHash, c.IP(), c.Get("User-Agent"), time.Now().Unix(), time.Now().Unix(),
		); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
		if err := redis.SetSession(tokenHash, userID, 24*time.Hour); err != nil {
			slog.Warn("failed to cache session in redis", "error", err)
		}

		secure := auth.CookieSecure(c)
		c.Cookie(&fiber.Cookie{
			Name: "auth_token", Value: token,
			HTTPOnly: true, Secure: secure, SameSite: "Strict",
			MaxAge: cfg.DefaultCookieMaxAge, Path: "/",
		})
		csrfToken := auth.GenerateCSRFToken()
		c.Cookie(&fiber.Cookie{
			Name: "csrf_token", Value: csrfToken,
			HTTPOnly: false, Secure: secure, SameSite: "Strict",
			MaxAge: cfg.DefaultCookieMaxAge, Path: "/",
		})

		events.AuditLogTenant(tenantID, userID, "auth.login.totp", "user", userID, fmt.Sprintf("TOTP login from %s", c.IP()), c.IP())
		return c.JSON(models.LoginResponse{Token: token, UserID: userID, Email: email, Name: name})
	})
}
