package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "embed"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/ai/capabilities"
	_ "vaporrmm/server/internal/ai/playbooks"
	_ "vaporrmm/server/internal/ai/providers"
	_ "vaporrmm/server/internal/ai/rag"
	_ "vaporrmm/server/internal/ai/sysfeatures"
	_ "vaporrmm/server/internal/ai/tools"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/handlers"
	"vaporrmm/server/internal/metrics"
	"vaporrmm/server/internal/middleware"
	"vaporrmm/server/internal/redis"
	"vaporrmm/server/internal/utils"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

//go:embed openapi.json
var openAPISpec []byte

var (
	buildVersion = "dev"
)

const (
	defaultServerPort       = 8080
	defaultAgentWSPort      = 47991
	defaultOfflineThreshold = 120
	defaultBodyLimit        = 4 * 1024 * 1024
	defaultReadTimeout      = 30 * time.Second
	defaultWriteTimeout     = 30 * time.Second
	defaultIdleTimeout      = 120 * time.Second
	defaultCookieMaxAge     = 86400
	defaultAgentPort        = 47991
	defaultSunshinePort     = 47990
	maxDevicesLimit         = 500
	maxCommandLimit         = 200
	maxAuditLimit           = 1000
	metricsRetentionDefault = 86400
	defaultTickerInterval   = 60 * time.Second
	defaultHSTSMaxAge       = 31536000
)

func main() {
	// Load configuration from environment / Docker secrets
	auth.JWTSecret = utils.ReadSecret("JWT_SECRET", "JWT_SECRET_FILE")
	if auth.JWTSecret == "" {
		// Dev convenience: generate a strong ephemeral key so single-process
		// runs work without env setup. Sessions die on restart, which is the
		// right tradeoff (no false sense of persistence).
		auth.JWTSecret = utils.GenerateSecureKey()
		slog.Warn("JWT_SECRET not set, using generated ephemeral key (sessions will not survive restart)")
	}
	// HS256 over a short secret is brute-forceable. 32 bytes = 256 bits is the
	// floor for matching the SHA-256 output. Operators who set a short secret
	// (e.g. "secret", "changeme") would otherwise have token forgery on tap.
	if len(auth.JWTSecret) < 32 {
		slog.Error("JWT_SECRET must be at least 32 characters (current length insufficient for HS256). Generate one with: openssl rand -base64 48")
		os.Exit(1)
	}

	utils.ServerPort = defaultServerPort
	if p := os.Getenv("SERVER_PORT"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			utils.ServerPort = parsed
		}
	}

	utils.AgentWSPort = defaultAgentWSPort
	if p := os.Getenv("AGENT_WS_PORT"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			utils.AgentWSPort = parsed
		}
	}

	moonlightWebURL := os.Getenv("MOONLIGHT_WEB_URL")
	sunshineVersion := os.Getenv("SUNSHINE_VERSION")
	if sunshineVersion == "" {
		sunshineVersion = "v2025.628.4510"
	}
	// SUNSHINE_VERSION is interpolated into shell commands sent to agents.
	// Restrict to a strict charset so a typo or hostile env can't inject RCE.
	if !regexp.MustCompile(`^v?[0-9A-Za-z._-]{1,32}$`).MatchString(sunshineVersion) {
		slog.Error("SUNSHINE_VERSION must be alphanumeric + . _ - (max 32 chars); refusing to start", "value", sunshineVersion)
		os.Exit(1)
	}

	if err := db.Init(); err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.DB.Close()

	db.EnsureDefaultTenant()

	redis.Init()
	defer redis.Close()

	events.StartWSRedisSubscriber()

	auth.LoadAgentTokens()
	auth.CreateDefaultAdmin()

	// Boot the AI kill-switch watcher (no-op when DB dialect is SQLite).
	// Provider implementations self-register via init() in the side-effect
	// import below; we kick off the kill-switch sync loop here so the cache
	// is warm by the time the first AI request lands.
	aiCtx, aiCancel := context.WithCancel(context.Background())
	defer aiCancel()
	ai.StartKillSwitchWatcher(aiCtx)
	// Stage 3: rollback orchestrator polls every 30s for action-tier
	// outcomes. No-op if the AI tab is hidden (no capabilities at act_low+).
	capabilities.StartRollbackOrchestrator(aiCtx)

	app := fiber.New(fiber.Config{
		BodyLimit:             defaultBodyLimit,
		ReadTimeout:           defaultReadTimeout,
		WriteTimeout:          defaultWriteTimeout,
		IdleTimeout:           defaultIdleTimeout,
		DisableStartupMessage: false,
	})

	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${method} ${path} ${latency}\n",
	}))

	// Request ID / trace context middleware
	app.Use(func(c *fiber.Ctx) error {
		traceID := c.Get("X-Trace-ID")
		if traceID == "" {
			traceID = utils.GenerateSecureKey()[:16]
		}
		c.Set("X-Trace-ID", traceID)
		c.Locals("trace_id", traceID)
		return c.Next()
	})

	// Security headers middleware
	app.Use(func(c *fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Content-Security-Policy", "default-src 'self'")
		// CORS responses depend on the request Origin (we credential-allow a
		// small allowlist), so any cache layer between us and the browser must
		// key on it. Without Vary: Origin a CDN can serve a response that was
		// authorised for origin A back to origin B.
		c.Vary("Origin")
		return c.Next()
	})

	// Resolve tenant from Host subdomain (hint only — JWT remains source of truth)
	app.Use(middleware.ResolveTenantFromHost())

	// HTTPS redirect + HSTS when TLS_CERT is set
	if os.Getenv("SERVER_CERT") != "" {
		app.Use(func(c *fiber.Ctx) error {
			if c.Protocol() == "http" {
				return c.Redirect("https://"+c.Hostname()+c.OriginalURL(), fiber.StatusMovedPermanently)
			}
			c.Set("Strict-Transport-Security", fmt.Sprintf("max-age=%d; includeSubDomains; preload", defaultHSTSMaxAge))
			return c.Next()
		})
	}

	// CORS
	corsOrigins := os.Getenv("CORS_ORIGINS")
	if corsOrigins == "" {
		corsOrigins = "http://localhost:3000"
	}
	app.Use(cors.New(cors.Config{
		AllowOrigins:     corsOrigins,
		AllowMethods:     "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders:     "Origin,Content-Type,Authorization,X-CSRF-Token",
		AllowCredentials: true,
	}))

	// Prometheus metrics middleware
	app.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Response().StatusCode())
		path := c.Route().Path
		if path == "" {
			path = c.Path()
		}
		metrics.HTTPRequestsTotal.WithLabelValues(c.Method(), path, status).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(c.Method(), path).Observe(duration)
		return err
	})

	cfg := handlers.Config{
		BuildVersion:            buildVersion,
		DefaultOfflineThreshold: defaultOfflineThreshold,
		DefaultAgentWSPort:      defaultAgentWSPort,
		DefaultSunshinePort:     defaultSunshinePort,
		DefaultCookieMaxAge:     defaultCookieMaxAge,
		MaxDevicesLimit:         maxDevicesLimit,
		MaxCommandLimit:         maxCommandLimit,
		MaxAuditLimit:           maxAuditLimit,
		MoonlightWebURL:         moonlightWebURL,
		SunshineVersion:         sunshineVersion,
	}

	// System routes
	handlers.RegisterSystemRoutes(app, cfg, openAPISpec)

	// Agent routes
	handlers.RegisterAgentRoutes(app, cfg)

	// Public API group
	publicAPI := app.Group("/api", auth.RateLimiter(60, time.Minute))

	// API v1 routes
	api := app.Group("/api/v1", auth.AuthMiddleware(), auth.CSRFMiddleware())

	// Backward compatibility: redirect legacy /api/* paths to /api/v1/*
	// Uses 308 (Permanent Redirect) to preserve HTTP method (POST, PUT, etc.)
	// Public endpoints (/api/auth/*, /api/branding/*) are excluded because they
	// must remain accessible without CSRF / auth middleware.
	app.Use(func(c *fiber.Ctx) error {
		path := c.Path()
		if !strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/api/v1/") {
			return c.Next()
		}
		if path == "/api/version" || path == "/api/openapi.json" ||
			strings.HasPrefix(path, "/api/auth/") {
			return c.Next()
		}
		// /api/branding/* has a public GET (host-tenant-resolved) AND a
		// /api/v1/branding/ authenticated PUT. Only the GET should bypass
		// the redirect; otherwise PUT/PATCH/DELETE 405 against the
		// public read-only handler.
		if strings.HasPrefix(path, "/api/branding/") && c.Method() == fiber.MethodGet {
			return c.Next()
		}
		return c.Redirect("/api/v1"+strings.TrimPrefix(path, "/api"), fiber.StatusPermanentRedirect)
	})

	// Auth routes
	handlers.RegisterAuthRoutes(publicAPI, api, cfg)

	// TOTP routes
	handlers.RegisterTOTPRoutes(publicAPI, api, cfg)

	// Tenant management (super-admin only)
	handlers.RegisterTenantRoutes(api)

	// User invites (tenant-admin self-serve + super_admin cross-tenant)
	handlers.RegisterInviteRoutes(publicAPI, api)

	// Self-serve tenant signup (gated by SIGNUP_OPEN or SIGNUP_INVITE_CODE)
	handlers.RegisterSignupRoutes(publicAPI)

	// Tenant data export + super_admin purge (right-to-erasure)
	handlers.RegisterTenantExportRoutes(api)

	// Operational readiness probes (Tailscale CLI, Sunshine releases, Moonlight web)
	handlers.RegisterIntegrationProbes(api)

	// AI surface (providers, routing, capabilities, runs, kill switches).
	// All routes inside dialect-gated; SQLite deployments get 503 from this group.
	handlers.RegisterAIRoutes(api)

	// Branding routes
	handlers.RegisterBrandingRoutes(app, api)

	// Device routes
	devices := api.Group("/devices")
	handlers.RegisterDeviceRoutes(api, devices, cfg)

	// Dashboard routes
	handlers.RegisterDashboardRoutes(api, cfg)

	// Script routes
	handlers.RegisterScriptRoutes(api, cfg)

	// Compliance routes
	handlers.RegisterComplianceRoutes(api, cfg)

	// Webhook routes
	handlers.RegisterWebhookRoutes(api)

	// Audit routes
	handlers.RegisterAuditRoutes(api, cfg)

	// Alert routes
	handlers.RegisterAlertRoutes(api)

	// Admin routes
	handlers.RegisterAdminRoutes(api)

	// Ticket routes
	handlers.RegisterTicketRoutes(api, cfg)

	// Fleet-wide patch list (per-device CRUD lives in RegisterDeviceRoutes)
	handlers.RegisterPatchRoutes(api)

	// Network topology snapshot (Tailscale state per device)
	handlers.RegisterNetworkRoutes(api)

	// ============================================================
	// Background goroutines
	// ============================================================
	offlineSec := defaultOfflineThreshold
	if o := os.Getenv("OFFLINE_THRESHOLD_SECONDS"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed > 0 {
			offlineSec = parsed
		}
	}
	slog.Info("offline threshold configured", "seconds", offlineSec)

	offlineDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(defaultTickerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				threshold := time.Now().Unix() - int64(offlineSec)
				// Capture transition candidates first, then UPDATE only those
				// IDs (with last_seen + status guards to drop devices that
				// heartbeated between SELECT and UPDATE). Emit alerts only for
				// rows the UPDATE actually changed — prior pattern picked N
				// already-offline rows and re-emitted alerts every tick.
				type offlineCandidate struct{ id, hostname, ownerID, tid string }
				selRows, err := db.DB.Query(`SELECT id, hostname, COALESCE(user_id,''), COALESCE(tenant_id,'default') FROM devices WHERE last_seen < ? AND status != 'offline' LIMIT 500`, threshold)
				if err != nil {
					slog.Warn("offline candidate query failed", "error", err)
					continue
				}
				var candidates []offlineCandidate
				for selRows.Next() {
					var c offlineCandidate
					if err := selRows.Scan(&c.id, &c.hostname, &c.ownerID, &c.tid); err == nil {
						candidates = append(candidates, c)
					}
				}
				selRows.Close()
				if len(candidates) == 0 {
					continue
				}
				ids := make([]interface{}, 0, len(candidates)+1)
				placeholders := make([]string, 0, len(candidates))
				for _, c := range candidates {
					ids = append(ids, c.id)
					placeholders = append(placeholders, "?")
				}
				ids = append(ids, threshold)
				res, err := db.DB.Exec(`UPDATE devices SET status = 'offline' WHERE id IN (`+strings.Join(placeholders, ",")+`) AND last_seen < ? AND status != 'offline'`, ids...)
				if err != nil {
					slog.Warn("offline transition update failed", "error", err)
					continue
				}
				updated, _ := res.RowsAffected()
				if updated == 0 {
					continue
				}
				slog.Info("marked devices offline", "count", updated)
				// Verify per-row that the device is still offline before emitting.
				// Skips candidates that heartbeated between SELECT and UPDATE.
				for _, c := range candidates {
					var stillOffline string
					if err := db.DB.QueryRow(`SELECT status FROM devices WHERE id = ?`, c.id).Scan(&stillOffline); err != nil || stillOffline != "offline" {
						continue
					}
					ts := time.Now().Unix()
					events.TriggerWebhooks(c.tid, "device.offline", map[string]interface{}{"device_id": c.id, "hostname": c.hostname, "timestamp": ts})
					events.WSBroadcastFiltered(c.tid, c.ownerID, map[string]interface{}{"type": "device.offline", "device_id": c.id, "hostname": c.hostname, "timestamp": ts})
					handlers.EmitAlert(c.tid, c.id, "offline", "warning", fmt.Sprintf("%s went offline", c.hostname))
				}
			case <-offlineDone:
				return
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(defaultTickerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var total, online int64
				_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&total)
				_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE status = 'online'`).Scan(&online)
				metrics.RegisteredDevicesGauge.Set(float64(total))
				metrics.ActiveDevicesGauge.Set(float64(online))
				if db.DB != nil && db.DB.DB != nil {
					stats := db.DB.DB.Stats()
					metrics.DBOpenConnsGauge.Set(float64(stats.OpenConnections))
					metrics.DBInUseConnsGauge.Set(float64(stats.InUse))
					metrics.DBIdleConnsGauge.Set(float64(stats.Idle))
				}

				// Per-tenant gauges. Reset all and re-populate so deleted tenants drop out.
				metrics.DevicesByTenant.Reset()
				metrics.OnlineDevicesByTenant.Reset()
				metrics.UsersByTenant.Reset()
				if rows, err := db.DB.Query(`SELECT tenant_id, COUNT(*) FROM devices GROUP BY tenant_id`); err == nil {
					for rows.Next() {
						var tid string
						var n float64
						if err := rows.Scan(&tid, &n); err == nil && tid != "" {
							metrics.DevicesByTenant.WithLabelValues(tid).Set(n)
						}
					}
					rows.Close()
				}
				if rows, err := db.DB.Query(`SELECT tenant_id, COUNT(*) FROM devices WHERE status = 'online' GROUP BY tenant_id`); err == nil {
					for rows.Next() {
						var tid string
						var n float64
						if err := rows.Scan(&tid, &n); err == nil && tid != "" {
							metrics.OnlineDevicesByTenant.WithLabelValues(tid).Set(n)
						}
					}
					rows.Close()
				}
				if rows, err := db.DB.Query(`SELECT tenant_id, COUNT(*) FROM users GROUP BY tenant_id`); err == nil {
					for rows.Next() {
						var tid string
						var n float64
						if err := rows.Scan(&tid, &n); err == nil && tid != "" {
							metrics.UsersByTenant.WithLabelValues(tid).Set(n)
						}
					}
					rows.Close()
				}
				var active, suspended float64
				_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tenants WHERE status = 'active'`).Scan(&active)
				_ = db.DB.QueryRow(`SELECT COUNT(*) FROM tenants WHERE status = 'suspended'`).Scan(&suspended)
				metrics.TenantsActive.Set(active)
				metrics.TenantsSuspended.Set(suspended)
			case <-offlineDone:
				return
			}
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-quit
		slog.Info("shutting down server...")
		close(offlineDone)
		if err := app.Shutdown(); err != nil {
			slog.Error("server shutdown error", "error", err)
		}
	}()

	// Start server
	slog.Info("starting server", "port", utils.ServerPort)
	if err := app.Listen(fmt.Sprintf(":%d", utils.ServerPort)); err != nil {
		slog.Error("failed to start server", "error", err)
	}
}
