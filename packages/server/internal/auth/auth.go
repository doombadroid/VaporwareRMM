package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"vaporrmm/models"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/redis"
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
func GenerateJWT(userID string, role string, expiryHours int) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  userID,
		"role": role,
		"exp":  now.Add(time.Duration(expiryHours) * time.Hour).Unix(),
		"iat":  now.Unix(),
		"iss":  "vaporrmm",
		"jti":  uuid.New().String(),
	})
	return token.SignedString([]byte(JWTSecret))
}

// ValidateJWT validates a JWT token signed with HMAC-SHA256 using golang-jwt/jwt/v5.
func ValidateJWT(tokenString string) (string, string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(JWTSecret), nil
	})
	if err != nil {
		return "", "", fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", "", fmt.Errorf("invalid token claims")
	}

	userID, _ := claims["sub"].(string)
	role, _ := claims["role"].(string)
	if role == "" {
		role = "admin"
	}

	return userID, role, nil
}

// GenerateCSRFToken creates a random 32-byte hex token.
func GenerateCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
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

		userID, role, err := ValidateJWT(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Invalid token",
				"message": err.Error(),
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

		c.Locals("auth_type", "user")
		c.Locals("user_id", userID)
		c.Locals("user_role", role)
		return c.Next()
	}
}

// AdminMiddleware blocks non-admin users.
func AdminMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		role := c.Locals("user_role")
		if role != "admin" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":   "Forbidden",
				"message": "Admin access required",
				"code":    403,
			})
		}
		return c.Next()
	}
}

// RateLimiter limits requests per IP using a sliding window.
// Uses Redis for distributed rate limiting when REDIS_URL is configured.
func RateLimiter(maxRequests int, window time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := c.IP()

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

		c.Locals("device_id", agentTok.DeviceID)
		c.Locals("hostname", agentTok.Hostname)
		return c.Next()
	}
}

// RegisterAgentToken stores an agent token in memory and persists it to the database.
func RegisterAgentToken(token, deviceID, hostname string) {
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
		ExpiresAt: expiresAt,
	}
	var upsertToken string
	if db.DB.Dialect == "postgres" {
		upsertToken = `INSERT INTO agent_tokens (token_hash, device_id, hostname, created_at, expires_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (token_hash) DO UPDATE SET device_id=EXCLUDED.device_id, hostname=EXCLUDED.hostname, created_at=EXCLUDED.created_at, expires_at=EXCLUDED.expires_at`
	} else {
		upsertToken = `INSERT OR REPLACE INTO agent_tokens (token_hash, device_id, hostname, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`
	}
	_, err := db.DB.Exec(upsertToken, tokenHash, deviceID, hostname, now, expiresAt)
	if err != nil {
		slog.Warn("could not persist agent token", "error", err)
	}
}

// LoadAgentTokens restores persisted agent tokens from the database into memory.
func LoadAgentTokens() {
	MigrateLegacyTokens()

	rows, err := db.DB.Query(`SELECT token_hash, device_id, hostname, expires_at FROM agent_tokens`)
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
		if err := rows.Scan(&tok.TokenHash, &tok.DeviceID, &tok.Hostname, &tok.ExpiresAt); err != nil {
			continue
		}
		RegisteredTokens[tok.TokenHash] = &tok
		count++
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "error", err)
	}
	slog.Info("loaded persisted agent tokens", "count", count)
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
			`INSERT INTO users (id, email, password_hash, name, role, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			userID, "admin@vaporrmm.local", string(hashedPassword), "Admin", "admin", time.Now().Unix(),
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
