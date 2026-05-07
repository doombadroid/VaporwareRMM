package handlers

import (
	"bytes"
	"net/http"
	"net/url"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/utils"
)

func RegisterSystemRoutes(app *fiber.App, cfg Config, openAPISpec []byte) {
	// Prometheus metrics endpoint
	app.Get("/metrics", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var buf bytes.Buffer
		promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}).ServeHTTP(
			&utils.ResponseWriter{Body: &buf, HTTPHeader: make(http.Header)},
			&http.Request{Method: "GET", URL: &url.URL{Path: "/metrics"}},
		)
		return c.SendString(buf.String())
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

	// WebSocket with auth validation
	app.Use("/ws", func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		// Validate auth token from query param or cookie
		token := c.Query("token")
		if token == "" {
			token = c.Cookies("auth_token")
		}
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "WebSocket auth required"})
		}
		_, _, err := auth.ValidateJWT(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid WebSocket token"})
		}
		return c.Next()
	})
	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		events.WSMu.Lock()
		events.WSClients[c] = true
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
