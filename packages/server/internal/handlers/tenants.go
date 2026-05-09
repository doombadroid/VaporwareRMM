package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// hashRegistrationSecret returns the SHA-256 hex digest used to store / look up registration secrets.
func hashRegistrationSecret(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

var tenantSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

type tenantOut struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Slug               string `json:"slug,omitempty"`
	Plan               string `json:"plan"`
	Status             string `json:"status"`
	HasRegistrationKey bool   `json:"has_registration_key"`
	MaxDevices         int    `json:"max_devices"`
	MaxUsers           int    `json:"max_users"`
	DeviceCount        int    `json:"device_count"`
	UserCount          int    `json:"user_count"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          *int64 `json:"updated_at,omitempty"`
}

func RegisterTenantRoutes(api fiber.Router) {
	admin := api.Group("/admin/tenants", auth.SuperAdminMiddleware())

	admin.Get("/", func(c *fiber.Ctx) error {
		// Two grouped count queries beat N+1 lookups even at small N (and matter at 100+).
		deviceCounts := map[string]int{}
		userCounts := map[string]int{}
		if rows, err := db.DB.Query(`SELECT tenant_id, COUNT(*) FROM devices GROUP BY tenant_id`); err == nil {
			for rows.Next() {
				var tid string
				var n int
				if err := rows.Scan(&tid, &n); err == nil {
					deviceCounts[tid] = n
				}
			}
			rows.Close()
		}
		if rows, err := db.DB.Query(`SELECT tenant_id, COUNT(*) FROM users GROUP BY tenant_id`); err == nil {
			for rows.Next() {
				var tid string
				var n int
				if err := rows.Scan(&tid, &n); err == nil {
					userCounts[tid] = n
				}
			}
			rows.Close()
		}

		rows, err := db.DB.Query(
			`SELECT id, name, COALESCE(slug,''), COALESCE(plan,'free'), COALESCE(status,'active'),
			        registration_secret, COALESCE(max_devices,0), COALESCE(max_users,0),
			        created_at, updated_at
			   FROM tenants ORDER BY created_at DESC`)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query tenants"})
		}
		defer rows.Close()
		out := []tenantOut{}
		for rows.Next() {
			var t tenantOut
			var regSecret sql.NullString
			var updatedAt sql.NullInt64
			if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status, &regSecret, &t.MaxDevices, &t.MaxUsers, &t.CreatedAt, &updatedAt); err != nil {
				slog.Warn("rows scan failed", "error", err)
				continue
			}
			t.HasRegistrationKey = regSecret.Valid && regSecret.String != ""
			if updatedAt.Valid {
				t.UpdatedAt = &updatedAt.Int64
			}
			t.DeviceCount = deviceCounts[t.ID]
			t.UserCount = userCounts[t.ID]
			out = append(out, t)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
		return c.JSON(fiber.Map{"tenants": out})
	})

	admin.Post("/", func(c *fiber.Ctx) error {
		var req struct {
			Name       string `json:"name"`
			Slug       string `json:"slug"`
			Plan       string `json:"plan"`
			MaxDevices int    `json:"max_devices"`
			MaxUsers   int    `json:"max_users"`
		}
		if err := c.BodyParser(&req); err != nil || req.Name == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
		}
		if strings.ContainsAny(req.Name, "\r\n") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name cannot contain newlines"})
		}
		if req.Slug == "" {
			req.Slug = autoSlug(req.Name)
		}
		if req.Slug != "" && !tenantSlugRe.MatchString(req.Slug) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "slug must be lowercase alphanumeric with hyphens (3-64 chars)"})
		}
		if req.Plan == "" {
			req.Plan = "free"
		}
		id := uuid.New().String()
		now := time.Now().Unix()
		if _, err := db.DB.Exec(
			`INSERT INTO tenants (id, name, slug, plan, status, max_devices, max_users, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, req.Name, req.Slug, req.Plan, "active", req.MaxDevices, req.MaxUsers, now, now,
		); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create tenant", "message": err.Error()})
		}
		adminID, _ := c.Locals("user_id").(string)
		middleware.InvalidateSlugCache()
		events.AuditLogTenant(id, adminID, "tenant.create", "tenant", id, fmt.Sprintf("created tenant %s", req.Name), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "name": req.Name, "slug": req.Slug, "plan": req.Plan, "status": "active"})
	})

	admin.Put("/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		var req struct {
			Name       *string `json:"name"`
			Plan       *string `json:"plan"`
			Status     *string `json:"status"`
			MaxDevices *int    `json:"max_devices"`
			MaxUsers   *int    `json:"max_users"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		fields := []string{"updated_at = ?"}
		args := []interface{}{time.Now().Unix()}
		if req.Name != nil {
			if strings.ContainsAny(*req.Name, "\r\n") {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name cannot contain newlines"})
			}
			fields = append(fields, "name = ?")
			args = append(args, *req.Name)
		}
		if req.Plan != nil {
			fields = append(fields, "plan = ?")
			args = append(args, *req.Plan)
		}
		if req.Status != nil {
			if *req.Status != "active" && *req.Status != "suspended" {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status must be active or suspended"})
			}
			if id == "default" && *req.Status == "suspended" {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot suspend default tenant"})
			}
			fields = append(fields, "status = ?")
			args = append(args, *req.Status)
			// Track when suspension started (for grace-period accounting).
			// On reactivation we clear the timestamp so a future suspension restarts the clock.
			if *req.Status == "suspended" {
				fields = append(fields, "suspended_at = ?")
				args = append(args, time.Now().Unix())
			} else {
				fields = append(fields, "suspended_at = NULL")
			}
		}
		if req.MaxDevices != nil {
			fields = append(fields, "max_devices = ?")
			args = append(args, *req.MaxDevices)
		}
		if req.MaxUsers != nil {
			fields = append(fields, "max_users = ?")
			args = append(args, *req.MaxUsers)
		}
		args = append(args, id)
		query := "UPDATE tenants SET " + joinFields(fields) + " WHERE id = ?"
		result, err := db.DB.Exec(query, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update tenant"})
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant not found"})
		}
		adminID, _ := c.Locals("user_id").(string)
		middleware.InvalidateSlugCache()
		// Mirror suspension into the AI kill-switch cache. The chokepoint
		// already refuses runs when tenants.status != 'active', but the cache
		// guarantees in-flight + freshly-arriving requests short-circuit
		// before any DB load. Reactivation clears the kill switch.
		if req.Status != nil {
			if *req.Status == "suspended" {
				_ = ai.SetKill("tenant:"+id, true, "tenant suspended", adminID)
			} else {
				_ = ai.SetKill("tenant:"+id, false, "tenant reactivated", adminID)
			}
		}
		events.AuditLogTenant(id, adminID, "tenant.update", "tenant", id, "updated tenant", c.IP())
		return c.JSON(fiber.Map{"message": "Tenant updated"})
	})

	admin.Delete("/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		if id == "default" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot delete default tenant"})
		}
		// Refuse delete if any tenant-tagged data still references this tenant.
		// Audit logs are preserved (they outlive the tenant for forensics).
		dataChecks := []struct {
			Table string
			Label string
		}{
			{"users", "user(s)"},
			{"devices", "device(s)"},
			{"agent_tokens", "agent token(s)"},
			{"scripts", "script(s)"},
			{"alert_rules", "alert rule(s)"},
			{"alert_settings", "alert settings"},
			{"webhooks", "webhook(s)"},
			{"branding", "branding row(s)"},
			{"tickets", "ticket(s)"},
			{"patches", "patch(es)"},
			{"file_transfers", "file transfer(s)"},
			{"device_commands", "device command(s)"},
			{"compliance_results", "compliance result(s)"},
			{"metrics_history", "metrics row(s)"},
		}
		for _, dc := range dataChecks {
			var n int
			if err := db.DB.QueryRow(`SELECT COUNT(*) FROM `+dc.Table+` WHERE tenant_id = ?`, id).Scan(&n); err == nil && n > 0 {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("tenant has %d %s; remove first", n, dc.Label)})
			}
		}
		result, err := db.DB.Exec(`DELETE FROM tenants WHERE id = ?`, id)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete tenant"})
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant not found"})
		}
		adminID, _ := c.Locals("user_id").(string)
		middleware.InvalidateSlugCache()
		events.AuditLogTenant(id, adminID, "tenant.delete", "tenant", id, "deleted tenant", c.IP())
		return c.JSON(fiber.Map{"message": "Tenant deleted"})
	})

	// Rotate per-tenant agent registration secret. Returns plaintext once.
	// We persist only the SHA-256 hash; the plaintext value is shown one time and then forgotten.
	admin.Post("/:id/registration-secret", func(c *fiber.Ctx) error {
		id := c.Params("id")
		secret, err := newRegistrationSecret()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate secret"})
		}
		result, err := db.DB.Exec(`UPDATE tenants SET registration_secret = ?, updated_at = ? WHERE id = ?`, hashRegistrationSecret(secret), time.Now().Unix(), id)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to store secret"})
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant not found"})
		}
		adminID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(id, adminID, "tenant.registration_secret_rotated", "tenant", id, "rotated registration secret", c.IP())
		// Don't let proxies/browsers cache the plaintext reveal
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Set("Pragma", "no-cache")
		serverURL := externalServerURL(c)
		return c.JSON(fiber.Map{
			"registration_secret": secret,
			"message":             "Save this secret — it will not be shown again",
			"install_commands":    buildInstallCommands(serverURL, secret),
			"server_url":          serverURL,
		})
	})

	// Start impersonating a tenant (super_admin only). Returns a new session
	// cookie with role=admin scoped to that tenant. The original identity is
	// preserved in the JWT so /auth/end-impersonation can reverse cleanly.
	admin.Post("/:id/impersonate", func(c *fiber.Ctx) error {
		targetTenant := c.Params("id")
		// Verify target tenant exists and is active
		var status string
		if err := db.DB.QueryRow(`SELECT status FROM tenants WHERE id = ?`, targetTenant).Scan(&status); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant not found"})
		}
		if status != "active" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Cannot impersonate a non-active tenant"})
		}
		callerID, _ := c.Locals("user_id").(string)
		callerTenant, _ := c.Locals("tenant_id").(string)
		callerRole, _ := c.Locals("user_role").(string)
		if callerTenant == "" {
			callerTenant = "default"
		}

		newToken, err := auth.GenerateImpersonationJWT(callerID, targetTenant, callerTenant, callerRole, 1)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
		}

		// Replace session row to track the new token
		newSessionID := uuid.New().String()
		newHash := fmt.Sprintf("%x", sha256.Sum256([]byte(newToken)))
		now := time.Now().Unix()
		if _, err := db.DB.Exec(
			`INSERT INTO user_sessions (id, user_id, token_hash, ip_address, user_agent, created_at, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			newSessionID, callerID, newHash, c.IP(), c.Get("User-Agent"), now, now,
		); err != nil {
			slog.Warn("could not record impersonation session", "error", err)
		}

		secure := auth.CookieSecure(c)
		c.Cookie(&fiber.Cookie{
			Name: "auth_token", Value: newToken,
			HTTPOnly: true, Secure: secure, SameSite: "Strict",
			MaxAge: 3600, Path: "/",
		})
		c.Cookie(&fiber.Cookie{
			Name: "csrf_token", Value: auth.GenerateCSRFToken(),
			HTTPOnly: false, Secure: secure, SameSite: "Strict",
			MaxAge: 3600, Path: "/",
		})
		events.AuditLogTenant(targetTenant, callerID, "tenant.impersonate.start", "tenant", targetTenant, fmt.Sprintf("super_admin %s started impersonating tenant %s", callerID, targetTenant), c.IP())
		events.AuditLogTenant(callerTenant, callerID, "tenant.impersonate.start", "tenant", targetTenant, fmt.Sprintf("started impersonating tenant %s", targetTenant), c.IP())
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
		return c.JSON(fiber.Map{"message": "Impersonation started", "tenant_id": targetTenant})
	})

	// End impersonation: must be called from a session token that has imp_for set.
	// Reads original identity from the JWT itself (signed by us — trusted).
	api.Post("/auth/end-impersonation", func(c *fiber.Ctx) error {
		token := c.Cookies("auth_token")
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "No session"})
		}
		impFor, _, _, ok := auth.ParseImpersonationClaims(token)
		if !ok {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Not currently impersonating"})
		}
		// Refuse to trust the JWT's snapshot of the original role/tenant.
		// If the user was demoted or moved between tenants while impersonating,
		// the claim is stale and would otherwise re-grant the original role.
		// Look up the live values from the DB instead.
		var currentRole, currentTenant string
		if err := db.DB.QueryRow(`SELECT role, COALESCE(tenant_id,'default') FROM users WHERE id = ?`, impFor).Scan(&currentRole, &currentTenant); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Original user no longer exists"})
		}
		if currentRole == "" {
			currentRole = "admin"
		}
		newToken, err := auth.GenerateJWT(impFor, currentTenant, currentRole, 24)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
		}

		// Revoke the impersonation session, create a new one for the restored identity
		oldHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		if _, err := db.DB.Exec(`DELETE FROM user_sessions WHERE token_hash = ?`, oldHash); err != nil {
			slog.Warn("could not revoke impersonation session", "error", err)
		}
		newSessionID := uuid.New().String()
		newHash := fmt.Sprintf("%x", sha256.Sum256([]byte(newToken)))
		now := time.Now().Unix()
		if _, err := db.DB.Exec(
			`INSERT INTO user_sessions (id, user_id, token_hash, ip_address, user_agent, created_at, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			newSessionID, impFor, newHash, c.IP(), c.Get("User-Agent"), now, now,
		); err != nil {
			slog.Warn("could not record restored session", "error", err)
		}

		secure := auth.CookieSecure(c)
		c.Cookie(&fiber.Cookie{
			Name: "auth_token", Value: newToken,
			HTTPOnly: true, Secure: secure, SameSite: "Strict",
			MaxAge: 86400, Path: "/",
		})
		c.Cookie(&fiber.Cookie{
			Name: "csrf_token", Value: auth.GenerateCSRFToken(),
			HTTPOnly: false, Secure: secure, SameSite: "Strict",
			MaxAge: 86400, Path: "/",
		})
		events.AuditLogTenant(currentTenant, impFor, "tenant.impersonate.end", "user", impFor, "ended impersonation", c.IP())
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
		return c.JSON(fiber.Map{"message": "Impersonation ended"})
	})

	// Tenant-admin variant: rotate / fetch the install command for the caller's own tenant.
	// Mirrors the super-admin endpoint so a tenant_admin can self-serve agent enrolment
	// without needing the platform owner to do it for them.
	api.Post("/tenants/me/registration-secret", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		secret, err := newRegistrationSecret()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate secret"})
		}
		result, err := db.DB.Exec(`UPDATE tenants SET registration_secret = ?, updated_at = ? WHERE id = ?`, hashRegistrationSecret(secret), time.Now().Unix(), tenantID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to store secret"})
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant not found"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "tenant.registration_secret_rotated", "tenant", tenantID, "rotated registration secret (self-serve)", c.IP())
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Set("Pragma", "no-cache")
		c.Set("X-Content-Type-Options", "nosniff")
		serverURL := externalServerURL(c)
		return c.JSON(fiber.Map{
			"registration_secret": secret,
			"install_commands":    buildInstallCommands(serverURL, secret),
			"server_url":          serverURL,
		})
	})
}

// externalServerURL returns the base URL the agent must dial back to.
// Prefers PUBLIC_URL; falls back to constructing from the request Host.
func externalServerURL(c *fiber.Ctx) string {
	if u := os.Getenv("PUBLIC_URL"); u != "" {
		return u
	}
	scheme := "http"
	if c.Protocol() == "https" || os.Getenv("SERVER_CERT") != "" {
		scheme = "https"
	}
	return scheme + "://" + c.Hostname()
}

// buildInstallCommands returns ready-to-paste install commands for each platform,
// with the registration secret already embedded as an environment variable.
//
// The Windows snippet does NOT pipe the bash installer through PowerShell
// (which would fail). Instead it downloads the native .exe binary, persists
// the bearer token + registration secret, and registers a Windows service.
func buildInstallCommands(serverURL, secret string) map[string]string {
	linux := fmt.Sprintf(
		"curl -fsSL %s/api/branding/agent-install?format=script | sudo REGISTRATION_SECRET='%s' bash -s -- --server %s",
		serverURL, secret, serverURL,
	)
	macos := linux // same install script

	// Windows: PowerShell-native flow. Run as Administrator. We embed the
	// registration secret + server URL as env vars in a JSON config the agent
	// reads on first start. The agent persists its bearer token after success.
	windows := fmt.Sprintf(`# Run in PowerShell as Administrator
$ErrorActionPreference = 'Stop'
$installDir = "$env:ProgramFiles\vaporrmm"
$dataDir = "$env:ProgramData\vaporrmm"
New-Item -ItemType Directory -Force -Path $installDir, $dataDir | Out-Null
Invoke-WebRequest -UseBasicParsing -Uri %q -OutFile "$installDir\agent.exe"
$token = -join ((1..64) | ForEach-Object { '{0:x}' -f (Get-Random -Maximum 16) })
@"
VAPOR_SERVER_URL=%s
VAPOR_AGENT_TOKEN=$token
REGISTRATION_SECRET=%s
"@ | Set-Content -Encoding ASCII "$dataDir\agent.env"
icacls "$dataDir\agent.env" /inheritance:r /grant:r "SYSTEM:F" "Administrators:F" | Out-Null
sc.exe create vaporrmm-agent binPath= "`+`"$installDir\agent.exe`+`"" start= auto DisplayName= "vaporRMM Agent" | Out-Null
sc.exe start vaporrmm-agent | Out-Null
Write-Host "vaporRMM agent installed and started."`,
		serverURL+"/download/agent-windows-amd64",
		serverURL,
		secret,
	)
	return map[string]string{
		"linux":   linux,
		"macos":   macos,
		"windows": windows,
	}
}

func joinFields(fields []string) string {
	out := ""
	for i, f := range fields {
		if i > 0 {
			out += ", "
		}
		out += f
	}
	return out
}

func newRegistrationSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "vrt_" + hex.EncodeToString(b), nil
}

// autoSlug derives a URL-safe slug from a tenant name (lowercase, hyphens, 3-64 chars).
func autoSlug(name string) string {
	var b []byte
	prevHyphen := false
	for i := 0; i < len(name); i++ {
		ch := name[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			b = append(b, ch)
			prevHyphen = false
		case ch >= 'A' && ch <= 'Z':
			b = append(b, ch+32)
			prevHyphen = false
		default:
			if !prevHyphen && len(b) > 0 {
				b = append(b, '-')
				prevHyphen = true
			}
		}
	}
	for len(b) > 0 && b[len(b)-1] == '-' {
		b = b[:len(b)-1]
	}
	if len(b) > 64 {
		b = b[:64]
	}
	if len(b) < 3 {
		return ""
	}
	return string(b)
}
