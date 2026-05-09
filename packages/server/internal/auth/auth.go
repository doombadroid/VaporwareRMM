package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"vaporrmm/models"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/redis"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	JWTSecret        string
	RegisteredTokens = make(map[string]*models.AgentToken)
	TokenMu          sync.RWMutex
	HashToken        = func(token string) string {
		sum := sha256.Sum256([]byte(token))
		return hex.EncodeToString(sum[:])
	}
)

type rateLimitEntry struct {
	count   int
	resetAt time.Time
}

var (
	rateLimitStore = make(map[string]*rateLimitEntry)
	rateLimitMu    sync.RWMutex
)

// GenerateJWT creates a JWT token signed with HMAC-SHA256 using golang-jwt/jwt/v5.
func GenerateJWT(userID, tenantID, role string, expiryHours int) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  userID,
		"tid":  tenantID,
		"role": role,
		"exp":  now.Add(time.Duration(expiryHours) * time.Hour).Unix(),
		"iat":  now.Unix(),
		"iss":  "vaporrmm",
		"jti":  uuid.New().String(),
	})
	return token.SignedString([]byte(JWTSecret))
}

// GenerateImpersonationJWT issues a session token where a super_admin acts as
// a tenant_admin inside another tenant. The original identity is embedded so
// EndImpersonation can restore it without trusting client-supplied state.
//
// claims:
//
//	sub  - super_admin's user_id (audit trail)
//	tid  - target tenant
//	role - "admin" (NOT super_admin — impersonator only has tenant_admin powers
//	       inside the target tenant; super_admin endpoints stay locked)
//	imp_for       - same as sub, makes the impersonation explicit for /users/me
//	imp_orig_tid  - super_admin's home tenant (to return to)
//	imp_orig_role - "super_admin" (to restore on end)
func GenerateImpersonationJWT(superUserID, targetTenantID, originalTenantID, originalRole string, expiryHours int) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":           superUserID,
		"tid":           targetTenantID,
		"role":          "admin",
		"imp_for":       superUserID,
		"imp_orig_tid":  originalTenantID,
		"imp_orig_role": originalRole,
		"exp":           now.Add(time.Duration(expiryHours) * time.Hour).Unix(),
		"iat":           now.Unix(),
		"iss":           "vaporrmm",
		"jti":           uuid.New().String(),
	})
	return token.SignedString([]byte(JWTSecret))
}

// ParseImpersonationClaims returns (impFor, origTid, origRole, isImpersonating).
// Returns ("", "", "", false) when the token has no impersonation claims.
func ParseImpersonationClaims(tokenString string) (string, string, string, bool) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(JWTSecret), nil
	})
	if err != nil {
		return "", "", "", false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", "", "", false
	}
	impFor, _ := claims["imp_for"].(string)
	if impFor == "" {
		return "", "", "", false
	}
	origTid, _ := claims["imp_orig_tid"].(string)
	origRole, _ := claims["imp_orig_role"].(string)
	return impFor, origTid, origRole, true
}

// GenerateTOTPChallenge creates a short-lived (5 min) token used during TOTP login.
// role is set to "totp_pending" so AuthMiddleware rejects it on all protected endpoints.
func GenerateTOTPChallenge(userID, tenantID string) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  userID,
		"tid":  tenantID,
		"role": "totp_pending",
		"exp":  now.Add(5 * time.Minute).Unix(),
		"iat":  now.Unix(),
		"iss":  "vaporrmm",
		"jti":  uuid.New().String(),
	})
	return token.SignedString([]byte(JWTSecret))
}

// ValidateJWT validates a JWT token signed with HMAC-SHA256 using golang-jwt/jwt/v5.
// Returns (userID, tenantID, role, error).
func ValidateJWT(tokenString string) (string, string, string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(JWTSecret), nil
	})
	if err != nil {
		return "", "", "", fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", "", "", fmt.Errorf("invalid token claims")
	}

	userID, _ := claims["sub"].(string)
	tenantID, _ := claims["tid"].(string)
	if tenantID == "" {
		tenantID = "default"
	}
	role, _ := claims["role"].(string)
	if role == "" {
		role = "admin"
	}

	return userID, tenantID, role, nil
}

// IsSuperAdmin reports whether the role grants cross-tenant access.
func IsSuperAdmin(role string) bool { return role == "super_admin" }

// TenantAllowed reports whether the named tenant is permitted to use the system.
// Honors a grace period after suspension: if SUSPENSION_GRACE_HOURS > 0,
// suspended tenants remain functional during that window so users see in-app
// warnings rather than a hard lock-out. After the window, full block.
//
// Returns false for missing tenants. Fails open on DB errors so a transient
// DB issue does not lock all tenants out at once.
func TenantAllowed(tenantID string) bool {
	if tenantID == "" {
		return false
	}
	var status string
	var suspendedAt sql.NullInt64
	err := db.DB.QueryRow(`SELECT status, suspended_at FROM tenants WHERE id = ?`, tenantID).Scan(&status, &suspendedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		slog.Warn("tenant status lookup failed", "tenant_id", tenantID, "error", err)
		return true
	}
	if status == "active" {
		return true
	}
	// suspended: allow only if still within grace
	if status == "suspended" {
		graceHours := suspensionGraceHours()
		if graceHours > 0 && suspendedAt.Valid {
			deadline := suspendedAt.Int64 + int64(graceHours)*3600
			if time.Now().Unix() < deadline {
				return true
			}
		}
	}
	return false
}

// TenantInGrace reports whether the tenant is suspended but still within the
// grace window. Used by the dashboard to render an in-app warning banner.
func TenantInGrace(tenantID string) (bool, int64) {
	if tenantID == "" {
		return false, 0
	}
	var status string
	var suspendedAt sql.NullInt64
	if err := db.DB.QueryRow(`SELECT status, suspended_at FROM tenants WHERE id = ?`, tenantID).Scan(&status, &suspendedAt); err != nil {
		return false, 0
	}
	if status != "suspended" || !suspendedAt.Valid {
		return false, 0
	}
	graceHours := suspensionGraceHours()
	if graceHours <= 0 {
		return false, 0
	}
	deadline := suspendedAt.Int64 + int64(graceHours)*3600
	if time.Now().Unix() >= deadline {
		return false, 0
	}
	return true, deadline
}

func suspensionGraceHours() int {
	v := os.Getenv("SUSPENSION_GRACE_HOURS")
	if v == "" {
		return 0
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// GenerateCSRFToken creates a random 32-byte hex token.
func GenerateCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// CookieSecure returns true when the dashboard is being served over HTTPS,
// covering all the deployment topologies we ship:
//
//   - direct-TLS (the Go server itself terminates with SERVER_CERT/SERVER_KEY)
//   - Caddy / reverse proxy in front, talking plaintext to the server
//     (X-Forwarded-Proto: https)
//   - operator opt-in via PUBLIC_URL=https://...
//
// Without this, every cookie issuance would default to Secure=false behind
// Caddy, and the auth_token + csrf_token would be sent over plaintext on a
// MITM downgrade.
func CookieSecure(c *fiber.Ctx) bool {
	if os.Getenv("SERVER_CERT") != "" {
		return true
	}
	if c != nil {
		if c.Protocol() == "https" {
			return true
		}
		if strings.EqualFold(c.Get("X-Forwarded-Proto"), "https") {
			return true
		}
	}
	if u := os.Getenv("PUBLIC_URL"); strings.HasPrefix(u, "https://") {
		return true
	}
	return false
}

// CSRFMiddleware validates X-CSRF-Token header against csrf_token cookie.
func CSRFMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		method := c.Method()
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			return c.Next()
		}
		authType := c.Locals("auth_type")
		if authType != "user" {
			return c.Next()
		}
		cookieCSRF := c.Cookies("csrf_token")
		headerCSRF := c.Get("X-CSRF-Token")
		if cookieCSRF == "" || headerCSRF == "" || cookieCSRF != headerCSRF {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":   "Invalid CSRF token",
				"message": "Please refresh the page and try again",
				"code":    403,
			})
		}
		return c.Next()
	}
}

// AuthMiddleware validates Bearer token or httpOnly cookie authentication.
func AuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		var token string

		cookieToken := c.Cookies("auth_token")
		if cookieToken != "" {
			token = cookieToken
		} else {
			authHeader := c.Get("Authorization")
			if authHeader == "" {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error":   "Missing authorization",
					"message": "Bearer token or auth cookie required",
					"code":    401,
				})
			}
			if !strings.HasPrefix(authHeader, "Bearer ") {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error":   "Invalid authorization header",
					"message": "Expected Bearer token",
					"code":    401,
				})
			}
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}

		TokenMu.RLock()
		if _, exists := RegisteredTokens[HashToken(token)]; exists {
			TokenMu.RUnlock()
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Agent tokens cannot access user endpoints",
				"message": "Use user authentication for this endpoint",
				"code":    401,
			})
		}
		TokenMu.RUnlock()

		userID, tenantID, role, err := ValidateJWT(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Invalid token",
				"message": err.Error(),
				"code":    401,
			})
		}

		if role == "totp_pending" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "TOTP verification required",
				"message": "Complete TOTP verification at /api/auth/login/totp",
				"code":    401,
			})
		}

		// Stateful session check: verify token is still in user_sessions
		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		var sessionUserID string

		// Check Redis cache first for fast lookup
		if redis.IsEnabled() {
			cachedUserID, err := redis.Client.Get(redis.Ctx, "session:"+tokenHash).Result()
			if err == nil && cachedUserID != "" {
				sessionUserID = cachedUserID
			}
		}

		// Fall back to database if not in Redis
		if sessionUserID == "" {
			err = db.DB.QueryRow(`SELECT user_id FROM user_sessions WHERE token_hash = ?`, tokenHash).Scan(&sessionUserID)
			if err != nil {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error":   "Session revoked",
					"message": "Your session has been revoked or expired. Please log in again.",
					"code":    401,
				})
			}
			// Cache in Redis for subsequent lookups
			if redis.IsEnabled() {
				if err := redis.Client.Set(redis.Ctx, "session:"+tokenHash, sessionUserID, 24*time.Hour).Err(); err != nil {
					slog.Warn("failed to cache session in redis", "error", err)
				}
			}
		}

		if sessionUserID != userID {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Session mismatch",
				"message": "Invalid session. Please log in again.",
				"code":    401,
			})
		}

		// Block all access for suspended (or deleted) tenants. super_admin bypasses
		// so the platform owner can always reach the admin endpoints to fix it.
		if !IsSuperAdmin(role) && !TenantAllowed(tenantID) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":   "Tenant inactive",
				"message": "Your organization's account is not currently active. Contact your administrator.",
				"code":    403,
			})
		}

		c.Locals("auth_type", "user")
		c.Locals("user_id", userID)
		c.Locals("user_role", role)
		c.Locals("tenant_id", tenantID)
		return c.Next()
	}
}

// SuperAdminMiddleware blocks any caller who is not a super_admin.
// Use for cross-tenant management endpoints.
func SuperAdminMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		role, _ := c.Locals("user_role").(string)
		if !IsSuperAdmin(role) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":   "Forbidden",
				"message": "Super-admin access required",
				"code":    403,
			})
		}
		return c.Next()
	}
}

// AdminMiddleware blocks non-admin users. super_admin and admin both pass.
func AdminMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		role, _ := c.Locals("user_role").(string)
		if role != "admin" && role != "super_admin" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":   "Forbidden",
				"message": "Admin access required",
				"code":    403,
			})
		}
		return c.Next()
	}
}

// rateLimitKey returns the bucket key for rate limiting.
// Priority order:
//
//	device_id  (per-agent — set by AgentAuthMiddleware) — keeps one noisy
//	           agent from blocking the rest of its tenant's fleet.
//	tenant_id  (per-tenant — set by AuthMiddleware) — one tenant can't
//	           exhaust another's allowance behind a shared egress IP.
//	IP         (unauthenticated fallback for /auth/login, /agent/register, etc.)
func rateLimitKey(c *fiber.Ctx) string {
	if did, _ := c.Locals("device_id").(string); did != "" {
		return "agent:" + did
	}
	if tid, _ := c.Locals("tenant_id").(string); tid != "" {
		return "tenant:" + tid
	}
	return "ip:" + c.IP()
}

// RateLimiter limits requests using a sliding window.
// Bucket key prefers tenant_id (when set by AuthMiddleware) so a noisy tenant
// can't exhaust the per-IP allowance for unrelated tenants sharing an egress.
// Falls back to client IP for unauthenticated routes.
// Uses Redis when REDIS_URL is configured for cross-instance correctness.
// Set DISABLE_RATE_LIMIT=1 in tests to bypass entirely.
func RateLimiter(maxRequests int, window time.Duration) fiber.Handler {
	bypass := os.Getenv("DISABLE_RATE_LIMIT") == "1"
	return func(c *fiber.Ctx) error {
		if bypass {
			return c.Next()
		}
		ip := rateLimitKey(c)

		// Try Redis first for distributed rate limiting
		if redis.IsEnabled() {
			allowed, resetAt, err := redis.IncrementRateLimit(ip, window, maxRequests)
			if err != nil {
				slog.Warn("redis rate limit error", "error", err)
				// Fall through to in-memory rate limiter on Redis error
			} else if !allowed {
				return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
					"error":   "Rate limit exceeded",
					"message": fmt.Sprintf("Too many requests. Retry after %s", resetAt.Format(time.RFC3339)),
					"code":    429,
				})
			} else {
				return c.Next()
			}
		}

		now := time.Now()
		rateLimitMu.Lock()
		entry, exists := rateLimitStore[ip]
		if !exists || now.After(entry.resetAt) {
			rateLimitStore[ip] = &rateLimitEntry{count: 1, resetAt: now.Add(window)}
			rateLimitMu.Unlock()
			return c.Next()
		}

		entry.count++
		if entry.count > maxRequests {
			rateLimitMu.Unlock()
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":   "Rate limit exceeded",
				"message": fmt.Sprintf("Too many requests. Retry after %s", entry.resetAt.Format(time.RFC3339)),
				"code":    429,
			})
		}
		rateLimitMu.Unlock()
		return c.Next()
	}
}

// AgentAuthMiddleware validates agent-specific Bearer tokens.
func AgentAuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Missing authorization header",
				"message": "Agent Bearer token required",
				"code":    401,
			})
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Invalid authorization header",
				"message": "Expected Bearer token",
				"code":    401,
			})
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		tokenHash := HashToken(token)

		TokenMu.RLock()
		agentTok, exists := RegisteredTokens[tokenHash]
		TokenMu.RUnlock()

		if !exists {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Invalid agent token",
				"message": "Agent not registered",
				"code":    401,
			})
		}

		if agentTok.ExpiresAt > 0 && time.Now().Unix() > agentTok.ExpiresAt {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Invalid agent token",
				"message": "Agent token expired",
				"code":    401,
			})
		}

		// Block agents whose tenant has been suspended or deleted.
		if !TenantAllowed(agentTok.TenantID) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":   "Tenant inactive",
				"message": "This tenant is not currently active.",
				"code":    403,
			})
		}

		c.Locals("device_id", agentTok.DeviceID)
		c.Locals("hostname", agentTok.Hostname)
		c.Locals("tenant_id", agentTok.TenantID)
		return c.Next()
	}
}

// RegisterAgentToken stores an agent token in memory and persists it to the database.
func RegisterAgentToken(token, deviceID, hostname, tenantID string) {
	if tenantID == "" {
		tenantID = "default"
	}
	tokenHash := HashToken(token)
	now := time.Now().Unix()
	// Default agent token expiry: 90 days
	expiresAt := now + 90*24*60*60
	TokenMu.Lock()
	defer TokenMu.Unlock()
	RegisteredTokens[tokenHash] = &models.AgentToken{
		TokenHash: tokenHash,
		DeviceID:  deviceID,
		Hostname:  hostname,
		TenantID:  tenantID,
		ExpiresAt: expiresAt,
	}
	var upsertToken string
	if db.DB.Dialect == "postgres" {
		upsertToken = `INSERT INTO agent_tokens (token_hash, device_id, hostname, tenant_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (token_hash) DO UPDATE SET device_id=EXCLUDED.device_id, hostname=EXCLUDED.hostname, tenant_id=EXCLUDED.tenant_id, created_at=EXCLUDED.created_at, expires_at=EXCLUDED.expires_at`
	} else {
		upsertToken = `INSERT OR REPLACE INTO agent_tokens (token_hash, device_id, hostname, tenant_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`
	}
	_, err := db.DB.Exec(upsertToken, tokenHash, deviceID, hostname, tenantID, now, expiresAt)
	if err != nil {
		slog.Warn("could not persist agent token", "error", err)
	}
}

// LoadAgentTokens restores persisted agent tokens from the database into memory
// and starts a background goroutine to prune expired tokens hourly.
func LoadAgentTokens() {
	MigrateLegacyTokens()

	rows, err := db.DB.Query(`SELECT token_hash, device_id, hostname, COALESCE(tenant_id,'default'), expires_at FROM agent_tokens`)
	if err != nil {
		slog.Warn("could not load agent tokens", "error", err)
		return
	}
	defer rows.Close()

	TokenMu.Lock()
	defer TokenMu.Unlock()

	count := 0
	for rows.Next() {
		var tok models.AgentToken
		if err := rows.Scan(&tok.TokenHash, &tok.DeviceID, &tok.Hostname, &tok.TenantID, &tok.ExpiresAt); err != nil {
			continue
		}
		RegisteredTokens[tok.TokenHash] = &tok
		count++
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "error", err)
	}
	slog.Info("loaded persisted agent tokens", "count", count)

	go pruneExpiredTokens()
}

// pruneExpiredTokens removes expired tokens from the in-memory map every hour.
func pruneExpiredTokens() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().Unix()
		TokenMu.Lock()
		pruned := 0
		for hash, tok := range RegisteredTokens {
			if tok.ExpiresAt > 0 && now > tok.ExpiresAt {
				delete(RegisteredTokens, hash)
				pruned++
			}
		}
		TokenMu.Unlock()
		if pruned > 0 {
			slog.Info("pruned expired agent tokens", "count", pruned)
		}
	}
}

// MigrateLegacyTokens hashes any plaintext tokens still in the DB.
func MigrateLegacyTokens() {
	rows, err := db.DB.Query(`SELECT token, device_id, hostname, created_at FROM agent_tokens WHERE token_hash IS NULL OR token_hash = ''`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var plainToken, deviceID, hostname string
		var createdAt int64
		if err := rows.Scan(&plainToken, &deviceID, &hostname, &createdAt); err != nil {
			continue
		}
		tokenHash := HashToken(plainToken)
		expiresAt := createdAt + 90*24*60*60
		if _, err := db.DB.Exec(`UPDATE agent_tokens SET token_hash = ?, token = '', expires_at = ? WHERE token = ?`, tokenHash, expiresAt, plainToken); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "error", err)
	}
}

// ValidatePasswordStrength checks minimum complexity: 8 chars, 1 upper, 1 lower, 1 digit.
func ValidatePasswordStrength(pw string) error {
	if len(pw) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range pw {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	if !hasUpper {
		return fmt.Errorf("password must contain at least one uppercase letter")
	}
	if !hasLower {
		return fmt.Errorf("password must contain at least one lowercase letter")
	}
	if !hasDigit {
		return fmt.Errorf("password must contain at least one digit")
	}
	return nil
}

// CreateDefaultAdmin creates a default admin user if none exists.
func CreateDefaultAdmin() {
	var count int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		slog.Warn("could not check users table", "error", err)
		return
	}

	if count == 0 {
		password := os.Getenv("ADMIN_PASSWORD")
		generated := false
		if password == "" {
			b := make([]byte, 16)
			if _, err := rand.Read(b); err != nil {
				slog.Error("failed to generate admin password", "error", err)
			}
			password = base64.RawURLEncoding.EncodeToString(b)
			generated = true
		}

		if !generated {
			if err := ValidatePasswordStrength(password); err != nil {
				slog.Error("ADMIN_PASSWORD does not meet strength requirements", "error", err)
				os.Exit(1)
			}
		}

		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			slog.Warn("could not hash password", "error", err)
			return
		}

		userID := uuid.New().String()
		_, err = db.DB.Exec(
			`INSERT INTO users (id, email, password_hash, name, role, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			userID, "admin@vaporrmm.local", string(hashedPassword), "Admin", "super_admin", time.Now().Unix(), "default",
		)
		if err != nil {
			slog.Warn("could not create default admin user", "error", err)
			return
		}

		if generated {
			fmt.Printf("\n==========================================================\n")
			fmt.Printf("  ADMIN CREDENTIALS (first run only — save these now!)\n")
			fmt.Printf("  Email:    admin@vaporrmm.local\n")
			fmt.Printf("  Password: %s\n", password)
			fmt.Printf("==========================================================\n\n")
		} else {
			slog.Info("Created admin user with ADMIN_PASSWORD from environment")
		}
	}
}
