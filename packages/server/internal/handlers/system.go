package handlers

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/middleware"
	"vaporrmm/server/internal/redis"
	"vaporrmm/server/internal/utils"
)

func RegisterSystemRoutes(app *fiber.App, cfg Config, openAPISpec []byte) {
	// Prometheus metrics endpoint — requires METRICS_API_KEY bearer or JWT admin session
	app.Get("/metrics", func(c *fiber.Ctx) error {
		if key := os.Getenv("METRICS_API_KEY"); key != "" {
			if c.Get("Authorization") != "Bearer "+key {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
			}
		} else {
			token := c.Cookies("auth_token")
			if token == "" {
				if h := c.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
					token = strings.TrimPrefix(h, "Bearer ")
				}
			}
			if token == "" {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
			}
			_, _, role, err := auth.ValidateJWT(token)
			// Per-tenant metrics carry tenant_id labels; restrict to super_admin
			// or METRICS_API_KEY. A tenant-admin scraping /metrics could otherwise
			// enumerate every other tenant's device + user counts.
			if err != nil || !auth.IsSuperAdmin(role) {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
			}
		}
		c.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var buf bytes.Buffer
		promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}).ServeHTTP(
			&utils.ResponseWriter{Body: &buf, HTTPHeader: make(http.Header)},
			&http.Request{Method: "GET", URL: &url.URL{Path: "/metrics"}},
		)
		return c.SendString(buf.String())
	})

	// Caddy on-demand TLS "ask" endpoint.
	// Caddy hits this with ?domain=foo.example.com before issuing a new cert.
	// We answer 200 only when the host's subdomain matches an active tenant slug,
	// preventing arbitrary cert issuance for spoofed domains.
	//
	// Intentionally NOT proxied through the public Caddyfile (apex block) so
	// external attackers can't enumerate tenant slugs. Caddy reaches it via
	// the internal docker network at http://server:8080/caddy/ask.
	app.Get("/caddy/ask", func(c *fiber.Ctx) error {
		slug := middleware.ExtractSubdomainSlug(c.Query("domain"))
		if middleware.SlugIsActive(slug) {
			return c.SendStatus(fiber.StatusOK)
		}
		return c.SendStatus(fiber.StatusNotFound)
	})

	// Health check
	app.Get("/health", func(c *fiber.Ctx) error {
		if err := db.DB.Ping(); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(models.StatusResponse{
				Status:  "unhealthy",
				Version: cfg.BuildVersion,
			})
		}
		return c.JSON(models.StatusResponse{
			Status:  "ok",
			Version: cfg.BuildVersion,
		})
	})

	// WebSocket with auth validation — cookie only (query param would be logged by proxies)
	app.Use("/ws", func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		// Same-origin check. The auth cookie is SameSite=Strict, so a cross-site
		// page can't *send* it on a normal request — but the WebSocket handshake
		// is governed by Origin alone, and SameSite enforcement on WS is patchy
		// across browsers. Verifying Origin against CORS_ORIGINS closes the gap.
		origin := c.Get("Origin")
		if origin != "" {
			allowed := false
			raw := os.Getenv("CORS_ORIGINS")
			if raw == "" {
				raw = "http://localhost:3000"
			}
			for _, o := range strings.Split(raw, ",") {
				if strings.TrimSpace(o) == origin {
					allowed = true
					break
				}
			}
			if !allowed {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "WebSocket origin not allowed"})
			}
		}
		token := c.Cookies("auth_token")
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "WebSocket auth required"})
		}
		userID, tenantID, role, err := auth.ValidateJWT(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid WebSocket token"})
		}

		// Stateful session check — mirrors AuthMiddleware; rejects revoked sessions
		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		var sessionUserID string
		if redis.IsEnabled() {
			if cached, err := redis.Client.Get(redis.Ctx, "session:"+tokenHash).Result(); err == nil && cached != "" {
				sessionUserID = cached
			}
		}
		if sessionUserID == "" {
			if err := db.DB.QueryRow(`SELECT user_id FROM user_sessions WHERE token_hash = ?`, tokenHash).Scan(&sessionUserID); err != nil {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Session revoked"})
			}
		}
		if sessionUserID != userID {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Session mismatch"})
		}

		// Suspended / deleted tenants cannot establish WebSocket. super_admin bypasses.
		if !auth.IsSuperAdmin(role) && !auth.TenantAllowed(tenantID) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Tenant inactive"})
		}

		c.Locals("ws_user_id", userID)
		c.Locals("ws_role", role)
		c.Locals("ws_tenant_id", tenantID)
		return c.Next()
	})
	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		userID, _ := c.Locals("ws_user_id").(string)
		role, _ := c.Locals("ws_role").(string)
		tenantID, _ := c.Locals("ws_tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		info := &events.WSClientInfo{UserID: userID, TenantID: tenantID, Role: role}
		events.WSMu.Lock()
		events.WSClients[c] = info
		events.WSMu.Unlock()
		defer func() {
			events.WSMu.Lock()
			delete(events.WSClients, c)
			events.WSMu.Unlock()
			c.Close()
		}()
		c.WriteJSON(map[string]interface{}{"type": "connected", "message": "WebSocket connected"})
		for {
			_, _, err := c.ReadMessage()
			if err != nil {
				break
			}
		}
	}))

	// API version
	app.Get("/api/version", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"version": "v1", "build": cfg.BuildVersion, "api_base": "/api/v1"})
	})

	// OpenAPI spec
	app.Get("/api/openapi.json", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "application/json")
		return c.Send(openAPISpec)
	})

	// Swagger redirect
	app.Get("/swagger", func(c *fiber.Ctx) error {
		return c.Redirect("/api/openapi.json")
	})
}
