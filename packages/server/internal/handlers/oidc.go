package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	httputilv "vaporrmm/server/internal/httputil"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// oidcOutboundClient is the HTTP client OIDC discovery / JWKS / token-
// exchange runs through. Vars (not functions) so an integration test
// can swap them for clients that allow loopback without changing
// production behaviour — the production binary always reads through
// the SafeOutboundClient that rejects private IPs at dial time.
//
// DO NOT expose these from outside the package. There is no env var
// or runtime flag that flips them — the only writer is the test code
// in this package's _test.go files.
var (
	oidcOutboundClient   = func(timeout time.Duration) *http.Client { return httputilv.SafeOutboundClient(timeout) }
	oidcIssuerValidator  = httputilv.RejectPrivateHost
	oidcTimeoutDiscovery = 15 * time.Second
)

// oidcSafeContext returns a context that pins go-oidc + golang.org/x/oauth2
// to the SSRF-safe HTTP client. Without this, the OIDC discovery + JWKS +
// token-exchange calls run on http.DefaultClient and can be redirected to
// cloud-metadata or LAN addresses.
func oidcSafeContext(parent context.Context) context.Context {
	c := oidcOutboundClient(oidcTimeoutDiscovery)
	ctx := oidc.ClientContext(parent, c)
	return context.WithValue(ctx, oauth2.HTTPClient, c)
}

// createOIDCSession persists a stateful session row matching the JWT
// the caller is about to set as a cookie. Extracted from the callback
// handler so a regression test can pin the schema requirements (the
// pre-fix INSERT omitted id + last_seen and silently broke SSO on
// Postgres). Any future change to the user_sessions schema must
// update both this function and TestOIDCCreateSessionPopulatesAllNotNull.
func createOIDCSession(jwt, userID, ip, userAgent string) error {
	sessionID := uuid.New().String()
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(jwt)))
	nowSec := time.Now().Unix()
	if _, err := db.DB.Exec(
		`INSERT INTO user_sessions (id, user_id, token_hash, ip_address, user_agent, created_at, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sessionID, userID, tokenHash, ip, userAgent, nowSec, nowSec,
	); err != nil {
		return err
	}
	return nil
}

// httpForwardedHostFromEnv lets operators override the redirect-URI
// host via FORWARDED_HOST when running behind a reverse proxy that
// terminates TLS. We deliberately do NOT honor X-Forwarded-Host header
// because a malicious upstream could rewrite it.
func httpForwardedHostFromEnv() string {
	return os.Getenv("FORWARDED_HOST")
}

const (
	oidcStateTTL   = 10 * time.Minute
	oidcStateBytes = 32
	maxOIDCIssuer  = 512
	maxOIDCClient  = 256
	maxOIDCSecret  = 1024
)

// randomURLSafeString returns a base64url-encoded n-byte random string.
// Used for state, nonce, and PKCE code_verifier.
func randomURLSafeString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge derives the S256 challenge for an oauth2 PKCE flow.
func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// validIssuerURL parses + sanity-checks an OIDC issuer URL. We require
// https in non-dev to avoid an MITM grabbing tokens, and reject URLs whose
// initial DNS resolution lands on a private / loopback / link-local address
// (the dial-time check in SafeOutboundClient is the actual SSRF defense, but
// rejecting at write-time gives the operator a clear 400 instead of letting
// the request silently fail at probe time).
func validIssuerURL(raw string) error {
	if len(raw) > maxOIDCIssuer {
		return errors.New("issuer too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("issuer must be http(s)")
	}
	if u.Host == "" {
		return errors.New("issuer missing host")
	}
	if err := oidcIssuerValidator(raw); err != nil {
		return err
	}
	return nil
}

// fetchOIDCConfig loads and decrypts the per-tenant OIDC config.
func fetchOIDCConfig(tenantID string) (issuer, clientID, clientSecret, defaultRole string, enabled bool, err error) {
	var enc string
	var en int
	err = db.DB.QueryRow(`SELECT issuer_url, client_id, client_secret_enc, default_role, enabled FROM tenant_oidc_configs WHERE tenant_id = ?`, tenantID).
		Scan(&issuer, &clientID, &enc, &defaultRole, &en)
	if err != nil {
		return
	}
	enabled = en == 1
	clientSecret, err = crypto.Decrypt(enc)
	return
}

func RegisterOIDCRoutes(app *fiber.App, publicAPI fiber.Router, api fiber.Router) {
	// Admin-side config CRUD lives on the admin chain.
	api.Get("/admin/oidc", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tenantID := callerTenantID(c)
		var issuer, clientID, defaultRole string
		var enabled int
		err := db.DB.QueryRow(`SELECT issuer_url, client_id, default_role, enabled FROM tenant_oidc_configs WHERE tenant_id = ?`, tenantID).
			Scan(&issuer, &clientID, &defaultRole, &enabled)
		if err == sql.ErrNoRows {
			return c.JSON(fiber.Map{"configured": false})
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "lookup failed"})
		}
		// NEVER return the client_secret. Configured=true tells the UI a
		// secret is present; admins re-paste to rotate.
		return c.JSON(fiber.Map{
			"configured":   true,
			"issuer_url":   issuer,
			"client_id":    clientID,
			"default_role": defaultRole,
			"enabled":      enabled == 1,
		})
	})

	api.Put("/admin/oidc", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			IssuerURL    string `json:"issuer_url"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			DefaultRole  string `json:"default_role"`
			Enabled      bool   `json:"enabled"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		if err := validIssuerURL(req.IssuerURL); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid issuer: " + err.Error()})
		}
		if req.ClientID == "" || len(req.ClientID) > maxOIDCClient {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "client_id required"})
		}
		if req.ClientSecret == "" || len(req.ClientSecret) > maxOIDCSecret {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "client_secret required"})
		}
		if req.DefaultRole != "user" && req.DefaultRole != "admin" {
			req.DefaultRole = "user"
		}
		// Probe the issuer once at write time so the admin sees a
		// failure synchronously. go-oidc fetches /.well-known config; we
		// pin the HTTP client to SafeOutboundClient so the probe (and the
		// secondary jwks_uri fetch chained from the discovery JSON) can't
		// reach RFC1918 / loopback / cloud-metadata addresses, even via a
		// 307 redirect.
		ctx, cancel := context.WithTimeout(oidcSafeContext(context.Background()), 10*time.Second)
		defer cancel()
		if _, err := oidc.NewProvider(ctx, req.IssuerURL); err != nil {
			// Don't echo err.Error() to the client — for an attacker
			// probing internal services it is a partial response oracle.
			slog.Warn("oidc probe failed", "tenant", callerTenantID(c), "error", err)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "issuer probe failed"})
		}
		enc, err := crypto.Encrypt(req.ClientSecret)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "encrypt failed"})
		}
		tenantID := callerTenantID(c)
		now := time.Now().Unix()
		enabled := 0
		if req.Enabled {
			enabled = 1
		}
		var stmt string
		if db.DB.Dialect == "postgres" {
			stmt = `INSERT INTO tenant_oidc_configs (tenant_id, issuer_url, client_id, client_secret_enc, default_role, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id) DO UPDATE SET issuer_url = EXCLUDED.issuer_url, client_id = EXCLUDED.client_id, client_secret_enc = EXCLUDED.client_secret_enc, default_role = EXCLUDED.default_role, enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at`
		} else {
			stmt = `INSERT OR REPLACE INTO tenant_oidc_configs (tenant_id, issuer_url, client_id, client_secret_enc, default_role, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
		}
		if _, err := db.DB.Exec(stmt, tenantID, req.IssuerURL, req.ClientID, enc, req.DefaultRole, enabled, now, now); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "save failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "oidc.update", "oidc", tenantID, "OIDC config saved", c.IP())
		return c.JSON(fiber.Map{"message": "saved"})
	})

	api.Delete("/admin/oidc", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tenantID := callerTenantID(c)
		if _, err := db.DB.Exec(`DELETE FROM tenant_oidc_configs WHERE tenant_id = ?`, tenantID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "oidc.delete", "oidc", tenantID, "OIDC config removed", c.IP())
		return c.JSON(fiber.Map{"message": "deleted"})
	})

	// Public OIDC initiate / callback. Tenant ID comes via query param
	// since the user isn't authenticated yet.
	publicAPI.Get("/auth/oidc/login", auth.RateLimiter(20, time.Minute), func(c *fiber.Ctx) error {
		tenantID := strings.TrimSpace(c.Query("tenant"))
		if tenantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("tenant required")
		}
		issuer, clientID, clientSecret, _, enabled, err := fetchOIDCConfig(tenantID)
		if err != nil || !enabled {
			return c.Status(fiber.StatusNotFound).SendString("OIDC not configured for this tenant")
		}
		ctx, cancel := context.WithTimeout(oidcSafeContext(context.Background()), 10*time.Second)
		defer cancel()
		provider, err := oidc.NewProvider(ctx, issuer)
		if err != nil {
			return c.Status(fiber.StatusBadGateway).SendString("issuer unreachable")
		}
		state, _ := randomURLSafeString(oidcStateBytes)
		nonce, _ := randomURLSafeString(oidcStateBytes)
		verifier, _ := randomURLSafeString(oidcStateBytes)
		challenge := pkceChallenge(verifier)

		// Build redirect URI from the request scheme + host. If the IdP
		// has a different registered redirect URI than what we generate
		// here it'll reject the auth request — that's the right failure
		// mode (loud, immediate). Don't honor X-Forwarded-Host because
		// an attacker who can spoof it could shift redirect_uri to
		// their own domain; operators behind a reverse proxy should set
		// the FORWARDED_HOST env var if they need a different host.
		host := c.Hostname()
		if forced := stripCtl(httpForwardedHostFromEnv()); forced != "" {
			host = forced
		}
		redirectURI := fmt.Sprintf("%s://%s/api/auth/oidc/callback", c.Protocol(), host)
		// Store ephemeral state for callback verification. 10-min TTL.
		expires := time.Now().Add(oidcStateTTL).Unix()
		if _, err := db.DB.Exec(`INSERT INTO oidc_states (state, tenant_id, nonce, code_verifier, redirect_uri, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			state, tenantID, nonce, verifier, redirectURI, expires, time.Now().Unix()); err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("state store failed")
		}
		oauth2Config := &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURI,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
		authURL := oauth2Config.AuthCodeURL(
			state,
			oauth2.SetAuthURLParam("nonce", nonce),
			oauth2.SetAuthURLParam("code_challenge", challenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)
		return c.Redirect(authURL, fiber.StatusFound)
	})

	publicAPI.Get("/auth/oidc/callback", auth.RateLimiter(20, time.Minute), func(c *fiber.Ctx) error {
		state := c.Query("state")
		code := c.Query("code")
		if state == "" || code == "" {
			return c.Status(fiber.StatusBadRequest).SendString("missing state/code")
		}
		// Single-use state: SELECT then DELETE.
		var (
			tenantID, nonce, verifier, redirectURI string
			expires                                int64
		)
		err := db.DB.QueryRow(`SELECT tenant_id, nonce, code_verifier, redirect_uri, expires_at FROM oidc_states WHERE state = ?`, state).
			Scan(&tenantID, &nonce, &verifier, &redirectURI, &expires)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("invalid state")
		}
		// Burn the state row immediately so a replay (if intercepted)
		// can't be reused. Done before token exchange to be defensive
		// even if the exchange fails — operator can retry from /login.
		_, _ = db.DB.Exec(`DELETE FROM oidc_states WHERE state = ?`, state)
		if time.Now().Unix() > expires {
			return c.Status(fiber.StatusBadRequest).SendString("state expired")
		}
		issuer, clientID, clientSecret, defaultRole, enabled, err := fetchOIDCConfig(tenantID)
		if err != nil || !enabled {
			return c.Status(fiber.StatusForbidden).SendString("OIDC not configured")
		}
		ctx, cancel := context.WithTimeout(oidcSafeContext(context.Background()), 15*time.Second)
		defer cancel()
		provider, err := oidc.NewProvider(ctx, issuer)
		if err != nil {
			return c.Status(fiber.StatusBadGateway).SendString("issuer unreachable")
		}
		oauth2Config := &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURI,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
		token, err := oauth2Config.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("token exchange failed")
		}
		rawIDToken, ok := token.Extra("id_token").(string)
		if !ok {
			return c.Status(fiber.StatusBadRequest).SendString("missing id_token")
		}
		verifierIDT := provider.Verifier(&oidc.Config{ClientID: clientID})
		idToken, err := verifierIDT.Verify(ctx, rawIDToken)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).SendString("id_token verify failed")
		}
		if idToken.Nonce != nonce {
			return c.Status(fiber.StatusUnauthorized).SendString("nonce mismatch")
		}
		// Codex #4: the email_verified claim must be present AND true.
		// Without it, an IdP account that lets the user pick their own
		// email claim (most IdPs do, by design) pivots straight to
		// "log in as anyone in the tenant whose email matches". The
		// claim is decoded as *bool so we can distinguish absent
		// (nil) from false; both reject, but the error message tells
		// the operator which it was.
		var claims struct {
			Email         string `json:"email"`
			EmailVerified *bool  `json:"email_verified"`
			Name          string `json:"name"`
			Sub           string `json:"sub"`
		}
		if err := idToken.Claims(&claims); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("claims parse failed")
		}
		if claims.Email == "" {
			return c.Status(fiber.StatusBadRequest).SendString("provider did not return email")
		}
		if claims.EmailVerified == nil {
			slog.Warn("oidc login rejected: id_token missing email_verified claim", "email", claims.Email)
			return c.Status(fiber.StatusUnauthorized).SendString("oidc: id_token missing email_verified claim; refusing to bind identity")
		}
		if !*claims.EmailVerified {
			slog.Warn("oidc login rejected: email_verified=false", "email", claims.Email)
			return c.Status(fiber.StatusUnauthorized).SendString("oidc: email_verified=false; refusing to bind identity")
		}
		// JIT provision: look up by email; create if missing.
		var (
			userID, role string
		)
		err = db.DB.QueryRow(`SELECT id, role FROM users WHERE tenant_id = ? AND email = ?`, tenantID, strings.ToLower(claims.Email)).Scan(&userID, &role)
		if err == sql.ErrNoRows {
			userID = uuid.New().String()
			role = defaultRole
			if claims.Name == "" {
				claims.Name = claims.Email
			}
			// password_hash empty — login via OIDC only. We require a
			// real bcrypt hash on local-pw login paths so empty hash
			// falls through to "invalid credentials" naturally.
			if _, err := db.DB.Exec(`INSERT INTO users (id, email, password_hash, name, role, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				userID, strings.ToLower(claims.Email), "", claims.Name, role, time.Now().Unix(), tenantID); err != nil {
				slog.Warn("oidc jit user insert failed", "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("user provision failed")
			}
		} else if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("user lookup failed")
		}
		_, _ = db.DB.Exec(`UPDATE users SET last_login = ? WHERE id = ?`, time.Now().Unix(), userID)

		// Issue local session JWT mirroring the existing password login.
		jwt, err := auth.GenerateJWT(userID, tenantID, role, 24)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("token issue failed")
		}
		if err := createOIDCSession(jwt, userID, c.IP(), c.Get("User-Agent")); err != nil {
			slog.Warn("oidc session insert failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).SendString("session create failed")
		}

		// Dual cookies: auth_token (httpOnly) + csrf_token (JS-readable).
		csrfToken, _ := randomURLSafeString(24)
		c.Cookie(&fiber.Cookie{Name: "auth_token", Value: jwt, HTTPOnly: true, Secure: c.Protocol() == "https", SameSite: "Strict", Path: "/", MaxAge: 24 * 3600})
		c.Cookie(&fiber.Cookie{Name: "csrf_token", Value: csrfToken, HTTPOnly: false, Secure: c.Protocol() == "https", SameSite: "Lax", Path: "/", MaxAge: 24 * 3600})
		events.AuditLogTenant(tenantID, userID, "auth.oidc_login", "user", userID, "OIDC login", c.IP())
		// Redirect to dashboard. SPA picks up the cookie on next request.
		return c.Redirect("/", fiber.StatusFound)
	})
}
