package auth

import (
	"context"
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

// ResetRateLimitStoreForTests clears the per-IP rate-limit map so a
// test that fires many requests against the same handler from the
// httptest client doesn't get hit by the prior test's accumulated
// budget. Production code never calls this.
func ResetRateLimitStoreForTests() {
	rateLimitMu.Lock()
	rateLimitStore = make(map[string]*rateLimitEntry)
	rateLimitMu.Unlock()
}

func init() {
	// Background pruner for the in-memory rate-limit map. Without this the
	// map grows unbounded as unique IPs hit the server (months of uptime
	// + many crawlers = a slow leak that operators would only notice on
	// OOM). The 10-minute interval is well above any rate-limit window,
	// so a legitimate caller's entry is never pruned mid-window.
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for range t.C {
			now := time.Now()
			rateLimitMu.Lock()
			for k, v := range rateLimitStore {
				if now.After(v.resetAt) {
					delete(rateLimitStore, k)
				}
			}
			rateLimitMu.Unlock()
		}
	}()
}

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

// ValidateJWT validates an admin-side JWT. Hard-rejects tokens issued
// for the customer portal (iss="vaporrmm-portal") so a portal cookie
// can never be used to reach admin endpoints — the auth middleware
// chain assumes any caller past this point has admin/user/super_admin
// role semantics.
//
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

	// Hard-reject portal tokens here. A misconfigured cookie path or
	// a forged Authorization header would otherwise let a portal user
	// hit admin endpoints; the issuer check is the one source of truth
	// for "this token belongs to the admin scope".
	iss, _ := claims["iss"].(string)
	if iss == "vaporrmm-portal" {
		return "", "", "", fmt.Errorf("portal token rejected on admin endpoint")
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
	// Portal-style "customer" role MUST never appear on admin tokens.
	// Defense-in-depth: even if someone crafts a token with
	// iss=vaporrmm but role=customer, refuse it.
	if role == "customer" {
		return "", "", "", fmt.Errorf("customer role not valid on admin endpoint")
	}

	return userID, tenantID, role, nil
}

// GeneratePortalJWT issues a customer-portal session token. Issuer is
// distinct so admin-side ValidateJWT rejects it. Role is always
// "customer". device_id is optional (NULL device_id = full-tenant
// portal user; set device_id = single-machine portal user).
func GeneratePortalJWT(customerID, tenantID, deviceID string, expiryHours int) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  customerID,
		"tid":  tenantID,
		"role": "customer",
		"exp":  now.Add(time.Duration(expiryHours) * time.Hour).Unix(),
		"iat":  now.Unix(),
		"iss":  "vaporrmm-portal",
		"jti":  uuid.New().String(),
	}
	if deviceID != "" {
		claims["did"] = deviceID
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(JWTSecret))
}

// ValidatePortalJWT validates a portal-issued token. Symmetric refusal
// of admin tokens — only iss="vaporrmm-portal" passes. Returns
// (customerID, tenantID, deviceID, error). deviceID is "" when the
// customer has full-tenant scope.
func ValidatePortalJWT(tokenString string) (string, string, string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(JWTSecret), nil
	})
	if err != nil {
		return "", "", "", fmt.Errorf("invalid portal token: %w", err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", "", "", fmt.Errorf("invalid portal token claims")
	}
	iss, _ := claims["iss"].(string)
	if iss != "vaporrmm-portal" {
		return "", "", "", fmt.Errorf("not a portal token")
	}
	role, _ := claims["role"].(string)
	if role != "customer" {
		return "", "", "", fmt.Errorf("portal token role mismatch")
	}
	customerID, _ := claims["sub"].(string)
	tenantID, _ := claims["tid"].(string)
	deviceID, _ := claims["did"].(string)
	if tenantID == "" {
		return "", "", "", fmt.Errorf("portal token missing tenant")
	}
	return customerID, tenantID, deviceID, nil
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
// Enforced for both admin (auth_type="user") and portal (auth_type="portal")
// scopes. Agent / public endpoints fall through.
func CSRFMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		method := c.Method()
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			return c.Next()
		}
		authType, _ := c.Locals("auth_type").(string)
		if authType != "user" && authType != "portal" {
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
//
// Fiber Group middleware is registered as `app.use(prefix, ...)` and matches
// by path prefix — meaning a Group at /api/v1 fires its middleware on
// /api/v1/portal/* too, even when those routes belong to a separate Group
// that has its own auth chain. Without this guard, portal endpoints would
// 401 here before PortalAuthMiddleware ever ran.
func AuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Strict prefix match (trailing slash) so a route like
		// /api/v1/portalfoo (none today, but defence-in-depth) doesn't
		// inherit the bypass.
		path := c.Path()
		if path == "/api/v1/portal" || strings.HasPrefix(path, "/api/v1/portal/") {
			return c.Next()
		}
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

// PortalAuthMiddleware validates a customer-portal session token from
// the `portal_token` cookie (or Bearer header for non-browser clients).
// Hard-rejects admin tokens — only ValidatePortalJWT-passing tokens
// reach the handler. Stateful session check via portal_sessions table
// keeps revoke semantics symmetric with the admin AuthMiddleware.
//
// Sets locals:
//
//	auth_type        = "portal"
//	customer_id      = customer_users.id
//	tenant_id        = customer's tenant
//	customer_device_id = optional device-scope filter (empty for full-tenant
//	                    portal users)
//
// Note: deliberately does NOT set user_id / user_role so a downstream
// handler that forgot to use portal-specific helpers won't accidentally
// see this caller as an admin/user.
func PortalAuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		var token string
		cookieToken := c.Cookies("portal_token")
		if cookieToken != "" {
			token = cookieToken
		} else {
			authHeader := c.Get("Authorization")
			if authHeader == "" {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error":   "Missing portal authorization",
					"message": "Portal session cookie or Bearer token required",
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

		// Reject agent tokens up front — same defense pattern as
		// AuthMiddleware. Without this an agent token would be tried as
		// a portal JWT, fail signature validation harmlessly, but waste
		// CPU on every request.
		TokenMu.RLock()
		if _, exists := RegisteredTokens[HashToken(token)]; exists {
			TokenMu.RUnlock()
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "agent tokens cannot access portal",
				"code":  401,
			})
		}
		TokenMu.RUnlock()

		// JWT-side device id is intentionally discarded; the DB row's
		// device_id is the source of truth (see below) so an admin
		// revoking the link applies immediately without forcing logout.
		customerID, tenantID, _, err := ValidatePortalJWT(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Invalid portal token",
				"message": err.Error(),
				"code":    401,
			})
		}

		// Stateful check via the customer_users.disabled flag; cheap
		// because we already need to know the row's still active.
		var disabled int
		var dbDeviceID sql.NullString
		err = db.DB.QueryRow(`SELECT disabled, device_id FROM customer_users WHERE id = ? AND tenant_id = ?`, customerID, tenantID).Scan(&disabled, &dbDeviceID)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "portal account not found"})
		}
		if disabled == 1 {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "portal account disabled"})
		}
		// Trust the DB value over the JWT for device scope so an
		// admin's revoke of the device link applies immediately
		// (without forcing the customer to re-login).
		var deviceID string
		if dbDeviceID.Valid {
			deviceID = dbDeviceID.String
		}

		if !TenantAllowed(tenantID) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant inactive"})
		}

		c.Locals("auth_type", "portal")
		c.Locals("customer_id", customerID)
		c.Locals("tenant_id", tenantID)
		c.Locals("customer_device_id", deviceID)
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

		// Reject tokens past their supersede TTL. Inside the grace
		// window we still honour the token so an in-flight request
		// from the rotating agent completes; past the window the
		// agent should already be using the new token, so any traffic
		// on the old one is either a misconfigured agent or replay.
		if agentTok.SupersededAt > 0 && time.Now().Unix() > agentTok.SupersededAt {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "Invalid agent token",
				"message": "Agent token superseded; re-register or rotate",
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

// AgentTokenSupersedeWindow is the grace period after which a
// superseded token stops being honoured. Long enough for an
// in-flight heartbeat to complete; short enough to bound the row
// count to ~1 active + 0-1 grace-window rows per device.
const AgentTokenSupersedeWindow = 60 * time.Second

// AgentPoPGraceWindow bounds how long the previous token (the one
// rotated out by the most recent re-register) remains valid as
// proof-of-possession for ANOTHER re-register. Same physical 60s as
// the supersede window — they describe the same race (in-flight
// rotation acks, brief network blips) — but kept as a separate
// constant so a future change to one doesn't quietly change the
// other. Codex #6 spec mandates 60s.
const AgentPoPGraceWindow = 60 * time.Second

// PoPVerdict is the result of VerifyAgentPoP. The grace-window
// outcomes split into rotation_ack vs crash_recovery so the audit
// log can tell network flakiness from a crashing agent without
// the operator having to correlate timestamps after the fact.
type PoPVerdict int

const (
	// PoPNoPriorToken means no active agent_tokens row exists for
	// this (tenant, device, hostname) tuple — there is no current
	// secret to prove possession of. The handler decides whether
	// this is a first-time registration (proceed) or a legacy agent
	// needing one-time bypass (mark + proceed) or a hard failure.
	PoPNoPriorToken PoPVerdict = iota
	// PoPAcceptCurrent means the presented token matches the active
	// token_hash on the row. Standard happy-path re-register.
	PoPAcceptCurrent
	// PoPAcceptGraceRotationAck means the presented token matches
	// previous_token_hash, the rotation is within
	// AgentPoPGraceWindow, AND the device has heartbeat traffic
	// post-rotation under the new token. The agent persisted the
	// new token successfully but a stale in-flight request from
	// before rotation is using the old one.
	PoPAcceptGraceRotationAck
	// PoPAcceptGraceCrashRecovery means the presented token matches
	// previous_token_hash within the grace window, BUT the device
	// has not heartbeated under the new token since rotation. Most
	// likely the agent crashed before persisting the new token and
	// is now re-registering with the only token it kept.
	PoPAcceptGraceCrashRecovery
	// PoPReject means the presented token matches neither current
	// nor (in-window) previous. Handler returns 409.
	PoPReject
)

// String renders the verdict as the audit-tag form the spec names.
// Operators searching audit logs match on these strings exactly, so
// they must not drift.
func (v PoPVerdict) String() string {
	switch v {
	case PoPNoPriorToken:
		return "no_prior_token"
	case PoPAcceptCurrent:
		return "current_token"
	case PoPAcceptGraceRotationAck:
		return "grace_window_used_rotation_ack"
	case PoPAcceptGraceCrashRecovery:
		return "grace_window_used_crash_recovery"
	case PoPReject:
		return "rejected"
	default:
		return "unknown"
	}
}

// VerifyAgentPoP applies the Codex #6 proof-of-possession check
// against the active agent_tokens row for (tenant, device, hostname).
// The presentedToken is the plaintext bearer the agent sent in the
// X-Existing-Agent-Token header on a re-register; an empty string
// means the agent presented no token. Returns PoPNoPriorToken when
// no active row exists (the handler then decides whether to allow a
// first-time INSERT or refuse via legacy-bypass rules).
//
// Constant-time hash compare. previous_token_rotated_at is compared
// to time.Now() with a strict less-than-window bound; equal-to-window
// counts as expired so the grace can't be stretched by clock skew.
func VerifyAgentPoP(tenantID, deviceID, hostname, presentedToken string) PoPVerdict {
	if tenantID == "" {
		tenantID = "default"
	}
	var currentHash string
	var previousHash sql.NullString
	var previousRotatedAt sql.NullInt64
	err := db.DB.QueryRow(
		`SELECT token_hash, previous_token_hash, previous_token_rotated_at
		   FROM agent_tokens
		  WHERE tenant_id = ? AND device_id = ? AND hostname = ?
		    AND (superseded_at IS NULL OR superseded_at = 0)
		  ORDER BY created_at DESC
		  LIMIT 1`,
		tenantID, deviceID, hostname,
	).Scan(&currentHash, &previousHash, &previousRotatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PoPNoPriorToken
	}
	if err != nil {
		slog.Warn("VerifyAgentPoP query failed", "error", err)
		return PoPReject
	}
	if presentedToken == "" {
		return PoPReject
	}
	presentedHash := HashToken(presentedToken)
	if subtleConstantTimeEqual(presentedHash, currentHash) {
		return PoPAcceptCurrent
	}
	if !previousHash.Valid || !previousRotatedAt.Valid {
		return PoPReject
	}
	if !subtleConstantTimeEqual(presentedHash, previousHash.String) {
		return PoPReject
	}
	now := time.Now().Unix()
	if now-previousRotatedAt.Int64 >= int64(AgentPoPGraceWindow.Seconds()) {
		return PoPReject
	}
	// Inside grace window. Decide rotation_ack vs crash_recovery
	// based on whether the device has heartbeated since the rotation
	// landed. Heartbeat updates devices.last_seen via the new token;
	// if last_seen > previous_token_rotated_at, the new token has
	// been used → stale in-flight request from before rotation =
	// rotation_ack. Otherwise the new token has never been used,
	// the agent likely didn't persist it = crash_recovery.
	var lastSeen sql.NullInt64
	if err := db.DB.QueryRow(`SELECT last_seen FROM devices WHERE id = ?`, deviceID).Scan(&lastSeen); err != nil {
		slog.Warn("VerifyAgentPoP last_seen lookup failed", "device_id", deviceID, "error", err)
		// Fall back to rotation_ack on a read failure rather than
		// crash_recovery — the latter is the noisier alarm and a
		// transient DB error shouldn't escalate the audit signal.
		return PoPAcceptGraceRotationAck
	}
	if lastSeen.Valid && lastSeen.Int64 > previousRotatedAt.Int64 {
		return PoPAcceptGraceRotationAck
	}
	return PoPAcceptGraceCrashRecovery
}

// subtleConstantTimeEqual compares two equal-length hex hash strings
// in constant time. Both arguments are SHA-256 hex digests (64
// chars); a length mismatch returns false without leaking timing.
func subtleConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// ParseLegacyBypassCutoff reads VAPOR_REFUSE_LEGACY_BYPASS_AFTER and
// validates it parses as RFC3339. Called once at server startup so a
// misconfigured value aborts boot rather than silently weakening the
// PoP rollout controls.
//
// Returns:
//   - (zero, false, nil): env var unset; bypass remains enabled until
//     the operator decides to set it.
//   - (t, true, nil): valid RFC3339 timestamp; bypass disabled after t.
//   - (zero, false, err): env var set but malformed; server MUST refuse
//     to boot.
//
// Fail-closed at runtime: IsLegacyAgentEligibleForBypass also re-reads
// the env var (so operators can flip the cutoff without a restart) and
// treats a malformed value as "cutoff elapsed" (bypass denied). The
// startup gate makes the malformed case impossible to reach in a clean
// deployment; the runtime gate handles the case where the operator
// edits the env var live and typos it.
func ParseLegacyBypassCutoff() (time.Time, bool, error) {
	cutoff := os.Getenv("VAPOR_REFUSE_LEGACY_BYPASS_AFTER")
	if cutoff == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, cutoff)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("VAPOR_REFUSE_LEGACY_BYPASS_AFTER=%q did not parse as RFC3339: %w", cutoff, err)
	}
	return t, true, nil
}

// IsLegacyAgentEligibleForBypass returns true if the device has not
// yet consumed its one-time pre-Codex-#6 bypass and the operator has
// not set VAPOR_REFUSE_LEGACY_BYPASS_AFTER to a past timestamp.
// Reading the env var on each call is cheap (it's a single Getenv)
// and means the operator can flip the cutoff without restarting.
//
// A malformed VAPOR_REFUSE_LEGACY_BYPASS_AFTER at runtime denies the
// bypass (fail-closed). The startup gate (ParseLegacyBypassCutoff,
// called from server main) prevents the server from booting with a
// malformed value in the first place.
func IsLegacyAgentEligibleForBypass(deviceID string) bool {
	t, isSet, err := ParseLegacyBypassCutoff()
	if err != nil {
		slog.Error("VAPOR_REFUSE_LEGACY_BYPASS_AFTER malformed at runtime; denying bypass (fail-closed)", "error", err)
		return false
	}
	if isSet && time.Now().After(t) {
		return false
	}
	var used int
	if err := db.DB.QueryRow(`SELECT COALESCE(legacy_pop_bypass_used, 0) FROM devices WHERE id = ?`, deviceID).Scan(&used); err != nil {
		return false
	}
	return used == 0
}

// ActiveTokenIsLegacy reports whether the device's active agent_tokens
// row was inserted by pre-Codex-#6 code. The discriminator is
// previous_token_hash IS NULL: Codex-#6 rotations always write a
// non-NULL previous_token_hash (except on the very first registration,
// which still produces a NULL — but that path goes through
// PoPNoPriorToken, not PoPReject). When the verdict is PoPReject and
// the active row predates Codex-#6, the legacy bypass is the only way
// for the agent to recover without admin intervention, because the
// pre-Codex agent never persisted a bearer it could present as PoP.
//
// Returns false on any DB error or when no active row is found —
// fail-closed so the bypass is never granted on a partial read.
func ActiveTokenIsLegacy(tenantID, deviceID, hostname string) bool {
	if tenantID == "" {
		tenantID = "default"
	}
	var hasPrior sql.NullString
	err := db.DB.QueryRow(
		`SELECT previous_token_hash FROM agent_tokens
		  WHERE tenant_id = ? AND device_id = ? AND hostname = ?
		    AND (superseded_at IS NULL OR superseded_at = 0)
		  ORDER BY created_at DESC LIMIT 1`,
		tenantID, deviceID, hostname,
	).Scan(&hasPrior)
	if err != nil {
		return false
	}
	return !hasPrior.Valid
}

// MarkLegacyBypassConsumed flips devices.legacy_pop_bypass_used to 1
// for the device. Idempotent; subsequent calls are no-ops at the
// SQL level.
func MarkLegacyBypassConsumed(deviceID string) error {
	_, err := db.DB.Exec(`UPDATE devices SET legacy_pop_bypass_used = 1 WHERE id = ?`, deviceID)
	return err
}

// RegisterAgentToken stores an agent token in memory and persists it
// to the database. Any prior tokens for the same (tenant_id, device_id,
// hostname) are marked superseded with a short overlap window so an
// in-flight heartbeat carrying the old token doesn't 401-flap during
// rotation; once the window passes, both the in-memory cache prune and
// the AuthMiddleware reject the old tokens.
//
// The hash of the immediately-prior token (if any) is recorded on the
// new row as previous_token_hash + previous_token_rotated_at to back
// the Codex #6 proof-of-possession grace window. Single previous; not
// a chain (spec-locked).
func RegisterAgentToken(token, deviceID, hostname, tenantID string) {
	if tenantID == "" {
		tenantID = "default"
	}
	tokenHash := HashToken(token)
	now := time.Now().Unix()
	supersedeAt := now + int64(AgentTokenSupersedeWindow.Seconds())
	expiresAt := now + 90*24*60*60 // 90 days

	// Serialize the entire prior-hash read + new-row write + supersede
	// update sequence. TokenMu blocks concurrent re-registers within this
	// process; the surrounding DB transaction makes the three statements
	// atomic so a multi-node future doesn't reintroduce a torn-write.
	// Without the lock, two re-registers for the same (tenant, device,
	// hostname) could both read the same prior hash and each record it
	// as previous_token_hash, losing one agent's true previous state and
	// causing a spurious 409 on its next rotation inside the grace
	// window (Codex #6 report, item #5).
	TokenMu.Lock()
	defer TokenMu.Unlock()

	// In-memory cache update is unconditional: the cache is the
	// authoritative source for AgentAuthMiddleware, and tests +
	// pre-DB-init paths rely on it staying populated even if the
	// surrounding DB writes fail. The DB writes below are best-effort
	// persistence on top of the cache.
	for h, tok := range RegisteredTokens {
		if h == tokenHash {
			continue
		}
		if tok.TenantID == tenantID && tok.DeviceID == deviceID && tok.Hostname == hostname && tok.SupersededAt == 0 {
			tok.SupersededAt = supersedeAt
		}
	}
	RegisteredTokens[tokenHash] = &models.AgentToken{
		TokenHash: tokenHash,
		DeviceID:  deviceID,
		Hostname:  hostname,
		TenantID:  tenantID,
		ExpiresAt: expiresAt,
	}

	if db.DB == nil {
		return
	}

	tx, err := db.DB.BeginTx(context.Background(), nil)
	if err != nil {
		slog.Warn("could not begin agent token rotation tx", "error", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var priorHash string
	if err := tx.QueryRow(
		db.DB.Q(`SELECT token_hash FROM agent_tokens
		  WHERE tenant_id = ? AND device_id = ? AND hostname = ?
		    AND (superseded_at IS NULL OR superseded_at = 0)
		    AND token_hash <> ?
		  ORDER BY created_at DESC LIMIT 1`),
		tenantID, deviceID, hostname, tokenHash,
	).Scan(&priorHash); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("could not read prior token for rotation linkage", "error", err)
	}

	// previous_token_hash + previous_token_rotated_at are nullable;
	// pass *string / *int64 so the dbWrapper sends NULL on first
	// registration. The grace-window PoP check skips rows where
	// previous_token_rotated_at is NULL.
	var prevHashArg interface{}
	var prevRotatedAtArg interface{}
	if priorHash != "" {
		prevHashArg = priorHash
		prevRotatedAtArg = now
	}

	var upsertToken string
	if db.DB.Dialect == "postgres" {
		upsertToken = `INSERT INTO agent_tokens (token_hash, device_id, hostname, tenant_id, created_at, expires_at, superseded_at, previous_token_hash, previous_token_rotated_at) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)
			ON CONFLICT (token_hash) DO UPDATE SET device_id=EXCLUDED.device_id, hostname=EXCLUDED.hostname, tenant_id=EXCLUDED.tenant_id, created_at=EXCLUDED.created_at, expires_at=EXCLUDED.expires_at, superseded_at=0, previous_token_hash=EXCLUDED.previous_token_hash, previous_token_rotated_at=EXCLUDED.previous_token_rotated_at`
	} else {
		upsertToken = `INSERT OR REPLACE INTO agent_tokens (token_hash, device_id, hostname, tenant_id, created_at, expires_at, superseded_at, previous_token_hash, previous_token_rotated_at) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`
	}
	if _, err := tx.Exec(db.DB.Q(upsertToken), tokenHash, deviceID, hostname, tenantID, now, expiresAt, prevHashArg, prevRotatedAtArg); err != nil {
		slog.Warn("could not persist agent token", "error", err)
		return
	}

	// Persist the supersede mark for every prior token row keyed by
	// the same (tenant, device, hostname) tuple. Only flip rows whose
	// superseded_at is 0 so a token already on its way out doesn't
	// get its TTL extended by another rotation.
	if _, err := tx.Exec(
		db.DB.Q(`UPDATE agent_tokens SET superseded_at = ? WHERE tenant_id = ? AND device_id = ? AND hostname = ? AND token_hash <> ? AND (superseded_at IS NULL OR superseded_at = 0)`),
		supersedeAt, tenantID, deviceID, hostname, tokenHash,
	); err != nil {
		slog.Warn("could not supersede prior agent tokens", "error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		slog.Warn("could not commit agent token rotation", "error", err)
		return
	}
	committed = true
}

// LoadAgentTokens restores persisted agent tokens from the database into memory
// and starts a background goroutine to prune expired tokens hourly.
func LoadAgentTokens() {
	MigrateLegacyTokens()

	rows, err := db.DB.Query(`SELECT token_hash, device_id, hostname, COALESCE(tenant_id,'default'), expires_at, COALESCE(superseded_at, 0) FROM agent_tokens`)
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
		if err := rows.Scan(&tok.TokenHash, &tok.DeviceID, &tok.Hostname, &tok.TenantID, &tok.ExpiresAt, &tok.SupersededAt); err != nil {
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

// pruneExpiredTokens removes expired tokens AND tokens whose
// supersede grace window has elapsed. Runs every minute so the supersede
// window (default 60s) actually limits the population. Prior loop was
// hourly which made the supersede TTL meaningless.
func pruneExpiredTokens() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().Unix()
		TokenMu.Lock()
		var toDeleteHashes []string
		for hash, tok := range RegisteredTokens {
			if tok.ExpiresAt > 0 && now > tok.ExpiresAt {
				delete(RegisteredTokens, hash)
				toDeleteHashes = append(toDeleteHashes, hash)
				continue
			}
			if tok.SupersededAt > 0 && now > tok.SupersededAt {
				delete(RegisteredTokens, hash)
				toDeleteHashes = append(toDeleteHashes, hash)
			}
		}
		TokenMu.Unlock()
		if len(toDeleteHashes) > 0 {
			slog.Info("pruned agent tokens", "count", len(toDeleteHashes))
			// Best-effort DB cleanup. We don't hold TokenMu across the
			// DB call; if a row is re-registered between cache delete
			// and DB delete, the new row's superseded_at=0 INSERT will
			// have already replaced the old row's state (token_hash
			// is the PK, so collisions overwrite).
			for _, h := range toDeleteHashes {
				if _, err := db.DB.Exec(`DELETE FROM agent_tokens WHERE token_hash = ?`, h); err != nil {
					slog.Warn("agent_tokens prune DELETE failed", "hash", h, "error", err)
				}
			}
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

		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 12)
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
			// First-run bootstrap. Operator MUST capture this password
			// from stdout — it is hashed before storage and unrecoverable
			// later. CodeQL go/clear-text-logging is a true positive on
			// dataflow but writing to stdout for a one-shot human
			// operator is the design intent, not log persistence.
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
