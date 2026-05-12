package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/tailscale"

	"github.com/gofiber/fiber/v2"
)

// Phase 1 of Tailscale integration (issue #18). All endpoints in
// this file are super-admin-only — the credential they manage is
// fleet-compromise-level and the connection itself is a singleton
// global resource. Tenant admins see only a stripped-down read-only
// indicator via GET /api/tailscale/connection (commit 6).
//
// Phase 2 (out of scope for this commit) will add a separate
// POST /api/agent/tailscale/preauth that the install script calls
// to mint per-install short-lived keys. That endpoint reads the
// credential through the same decrypt path as GET /connection.

// tailscaleClientFactory lets tests inject a fake. Production
// returns the real wrapper.
var tailscaleClientFactory = func(clientID, clientSecret string) tailscaleAPI {
	return tailscale.NewClient(clientID, clientSecret)
}

// tailscaleAPI is the subset of *tailscale.Client the handler uses,
// extracted so the tests can satisfy it without standing up an
// httptest.Server for every handler-level assertion.
type tailscaleAPI interface {
	Authenticate(ctx context.Context) error
	ListTailnets(ctx context.Context) ([]tailscale.Tailnet, error)
	ValidateAuthKeyScope(ctx context.Context, tailnet string) error
	ValidateDeviceListScope(ctx context.Context, tailnet string) error
	ListDevices(ctx context.Context, tailnet string) ([]tailscale.Device, error)
}

// RegisterTailscaleRoutes wires the Phase-1 endpoints under
// /api/v1/tailscale. All super-admin-gated except
// GET /connection which downgrades its response shape for
// tenant admins (transparency indicator — commit 6 wires the UI
// to it).
func RegisterTailscaleRoutes(api fiber.Router) {
	api.Post("/tailscale/validate", auth.AdminMiddleware(), requireSuperAdmin, validateTailscaleCredential)
	api.Post("/tailscale/connect", auth.AdminMiddleware(), requireSuperAdmin, connectTailscale)
	api.Get("/tailscale/connection", auth.AdminMiddleware(), getTailscaleConnection)
	api.Put("/tailscale/connection", auth.AdminMiddleware(), requireSuperAdmin, rotateTailscaleConnection)
	api.Delete("/tailscale/connection", auth.AdminMiddleware(), requireSuperAdmin, disconnectTailscale)
	api.Get("/tailscale/devices", auth.AdminMiddleware(), requireSuperAdmin, listTailscaleDevices)
}

func requireSuperAdmin(c *fiber.Ctx) error {
	role, _ := c.Locals("user_role").(string)
	if !auth.IsSuperAdmin(role) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Tailscale management requires super-admin",
			"code":  403,
		})
	}
	return c.Next()
}

// validateTailscaleCredential runs the three-checkmark flow without
// persisting anything. The setup wizard fires this on the "Validate"
// button click; the response shape is exactly what the UI renders.
func validateTailscaleCredential(c *fiber.Ctx) error {
	var req struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Tailnet      string `json:"tailnet,omitempty"` // optional; auto-detected if absent
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
	}
	if req.ClientID == "" || req.ClientSecret == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "client_id and client_secret are required"})
	}

	cl := tailscaleClientFactory(req.ClientID, req.ClientSecret)
	ctx := c.UserContext()
	checks := map[string]string{
		"authentication":    "pending",
		"auth_key_scope":    "pending",
		"device_list_scope": "pending",
	}
	errorsMap := map[string]string{}
	tailnets := []tailscale.Tailnet{}

	if err := cl.Authenticate(ctx); err != nil {
		checks["authentication"] = "failed"
		errorsMap["authentication"] = classifyTailscaleError(err,
			"Verify the OAuth client_id / client_secret at https://login.tailscale.com/admin/settings/oauth")
		return c.JSON(fiber.Map{"checks": checks, "errors": errorsMap, "tailnets": tailnets})
	}
	checks["authentication"] = "ok"

	// Auto-detect tailnet if caller didn't pass one.
	if req.Tailnet == "" {
		tn, err := cl.ListTailnets(ctx)
		if err != nil {
			errorsMap["tailnets"] = classifyTailscaleError(err,
				"Could not enumerate tailnets — confirm the OAuth client is bound to a tailnet")
			return c.JSON(fiber.Map{"checks": checks, "errors": errorsMap, "tailnets": tailnets})
		}
		tailnets = tn
		if len(tn) == 1 {
			req.Tailnet = tn[0].Name
		}
	}
	if req.Tailnet == "" {
		// Multiple tailnets and caller didn't pick one. Surface
		// the list and let the wizard prompt.
		return c.JSON(fiber.Map{"checks": checks, "errors": errorsMap, "tailnets": tailnets})
	}

	if err := cl.ValidateAuthKeyScope(ctx, req.Tailnet); err != nil {
		checks["auth_key_scope"] = "failed"
		errorsMap["auth_key_scope"] = classifyTailscaleError(err,
			"Grant the OAuth client the 'auth_keys' (write) scope at https://login.tailscale.com/admin/settings/oauth")
	} else {
		checks["auth_key_scope"] = "ok"
	}

	if err := cl.ValidateDeviceListScope(ctx, req.Tailnet); err != nil {
		checks["device_list_scope"] = "failed"
		errorsMap["device_list_scope"] = classifyTailscaleError(err,
			"Grant the OAuth client the 'devices' (read) scope at https://login.tailscale.com/admin/settings/oauth")
	} else {
		checks["device_list_scope"] = "ok"
	}

	if len(tailnets) == 0 {
		tailnets = []tailscale.Tailnet{{Name: req.Tailnet}}
	}
	return c.JSON(fiber.Map{
		"checks":   checks,
		"errors":   errorsMap,
		"tailnets": tailnets,
	})
}

// connectTailscale persists the credential after validating. Refuses
// if a connection already exists (operator must rotate or disconnect
// first).
func connectTailscale(c *fiber.Ctx) error {
	if err := crypto.MustBeEnabled(); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "Encryption is required to store Tailscale credentials. Set SECRETS_ENCRYPTION_KEY.",
		})
	}
	var req struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Tailnet      string `json:"tailnet"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
	}
	if req.ClientID == "" || req.ClientSecret == "" || req.Tailnet == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "client_id, client_secret, and tailnet are required"})
	}

	var existing string
	if err := db.DB.QueryRow(`SELECT id FROM tailscale_connection WHERE id = 'singleton'`).Scan(&existing); err == nil && existing != "" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "A Tailscale connection already exists. Use PUT /api/v1/tailscale/connection to rotate, or DELETE to disconnect first.",
			"code":  409,
		})
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("tailscale: existence check failed", "error", err)
	}

	if err := runValidationChecks(c.UserContext(), req.ClientID, req.ClientSecret, req.Tailnet); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	encID, err := crypto.Encrypt(req.ClientID)
	if err != nil {
		slog.Error("tailscale: encrypt client_id", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to encrypt credential"})
	}
	encSecret, err := crypto.Encrypt(req.ClientSecret)
	if err != nil {
		slog.Error("tailscale: encrypt client_secret", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to encrypt credential"})
	}

	now := time.Now().Unix()
	userID, _ := c.Locals("user_id").(string)
	if _, err := db.DB.Exec(
		`INSERT INTO tailscale_connection (id, oauth_client_id_encrypted, oauth_client_secret_encrypted, tailnet, tailnet_display_name, connected_at, connected_by_user_id, last_validated_at) VALUES ('singleton', ?, ?, ?, ?, ?, ?, ?)`,
		encID, encSecret, req.Tailnet, "", now, userID, now,
	); err != nil {
		slog.Error("tailscale: persist", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to persist connection"})
	}

	tenantID, _ := c.Locals("tenant_id").(string)
	events.AuditLogTenant(tenantID, userID, "tailscale.connected", "tailscale_connection", "singleton",
		fmt.Sprintf("connected to tailnet %s", req.Tailnet), c.IP())

	return c.JSON(fiber.Map{
		"tailnet":      req.Tailnet,
		"connected_at": now,
	})
}

// getTailscaleConnection returns the connection metadata. Super-
// admin sees full info; tenant admin sees the minimal indicator
// shape (commit 6 wires this to the UI).
func getTailscaleConnection(c *fiber.Ctx) error {
	role, _ := c.Locals("user_role").(string)
	isSuper := auth.IsSuperAdmin(role)

	var tailnet, displayName, connectedBy string
	var connectedAt, lastValidated, rotated sql.NullInt64
	var lastValidationError sql.NullString
	err := db.DB.QueryRow(
		`SELECT tailnet, COALESCE(tailnet_display_name, ''), connected_at, COALESCE(connected_by_user_id, ''), last_validated_at, last_validation_error, rotated_at FROM tailscale_connection WHERE id = 'singleton'`,
	).Scan(&tailnet, &displayName, &connectedAt, &connectedBy, &lastValidated, &lastValidationError, &rotated)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(fiber.Map{"connected": false})
	}
	if err != nil {
		slog.Warn("tailscale: get connection", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to read connection"})
	}

	display := displayName
	if display == "" {
		display = tailnet
	}
	if !isSuper {
		// Tenant-admin shape: just enough for the "Network:
		// Tailscale (connected to X)" indicator.
		return c.JSON(fiber.Map{
			"connected":            true,
			"tailnet_display_name": display,
		})
	}
	out := fiber.Map{
		"connected":            true,
		"tailnet":              tailnet,
		"tailnet_display_name": display,
		"connected_at":         connectedAt.Int64,
		"connected_by_user_id": connectedBy,
	}
	if lastValidated.Valid {
		out["last_validated_at"] = lastValidated.Int64
	}
	if lastValidationError.Valid && lastValidationError.String != "" {
		out["last_validation_error"] = lastValidationError.String
	}
	if rotated.Valid {
		out["rotated_at"] = rotated.Int64
	}
	return c.JSON(out)
}

// rotateTailscaleConnection swaps the credential in place. Refuses
// to rotate to a credential bound to a different tailnet — that's a
// disconnect + reconnect operation, not a rotation.
func rotateTailscaleConnection(c *fiber.Ctx) error {
	if err := crypto.MustBeEnabled(); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "Encryption is required to store Tailscale credentials.",
		})
	}
	var req struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
	}
	if req.ClientID == "" || req.ClientSecret == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "client_id and client_secret are required"})
	}

	var existingTailnet string
	if err := db.DB.QueryRow(`SELECT tailnet FROM tailscale_connection WHERE id = 'singleton'`).Scan(&existingTailnet); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "No connection to rotate. Use POST /connect first."})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to read existing connection"})
	}

	// Auth + auto-detect new credential's tailnet.
	cl := tailscaleClientFactory(req.ClientID, req.ClientSecret)
	ctx := c.UserContext()
	if err := cl.Authenticate(ctx); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": classifyTailscaleError(err, "Rotation refused: new credential failed authentication"),
		})
	}
	tns, err := cl.ListTailnets(ctx)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": classifyTailscaleError(err, "Rotation refused: could not enumerate new credential's tailnets"),
		})
	}
	matchedTailnet := ""
	for _, t := range tns {
		if t.Name == existingTailnet {
			matchedTailnet = t.Name
			break
		}
	}
	if matchedTailnet == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("Rotation refused: new credential owns tailnet(s) %s but existing connection is on tailnet %s. Disconnect and reconnect to change tailnets, which will require re-onboarding all devices.",
				tailnetsList(tns), existingTailnet),
		})
	}
	if err := cl.ValidateAuthKeyScope(ctx, matchedTailnet); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": classifyTailscaleError(err, "auth_keys scope missing on new credential")})
	}
	if err := cl.ValidateDeviceListScope(ctx, matchedTailnet); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": classifyTailscaleError(err, "devices scope missing on new credential")})
	}

	encID, err := crypto.Encrypt(req.ClientID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to encrypt new credential"})
	}
	encSecret, err := crypto.Encrypt(req.ClientSecret)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to encrypt new credential"})
	}

	now := time.Now().Unix()
	if _, err := db.DB.Exec(
		`UPDATE tailscale_connection SET oauth_client_id_encrypted = ?, oauth_client_secret_encrypted = ?, rotated_at = ?, last_validated_at = ?, last_validation_error = NULL WHERE id = 'singleton'`,
		encID, encSecret, now, now,
	); err != nil {
		slog.Error("tailscale: rotate UPDATE", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to swap credential"})
	}

	tenantID, _ := c.Locals("tenant_id").(string)
	userID, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tenantID, userID, "tailscale.rotated", "tailscale_connection", "singleton",
		fmt.Sprintf("rotated credential for tailnet %s", existingTailnet), c.IP())

	return c.JSON(fiber.Map{"rotated_at": now, "tailnet": existingTailnet})
}

func disconnectTailscale(c *fiber.Ctx) error {
	var existingTailnet string
	if err := db.DB.QueryRow(`SELECT tailnet FROM tailscale_connection WHERE id = 'singleton'`).Scan(&existingTailnet); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(fiber.Map{"disconnected": true})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to read connection"})
	}
	if _, err := db.DB.Exec(`DELETE FROM tailscale_connection WHERE id = 'singleton'`); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete connection"})
	}
	tenantID, _ := c.Locals("tenant_id").(string)
	userID, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tenantID, userID, "tailscale.disconnected", "tailscale_connection", "singleton",
		fmt.Sprintf("disconnected from tailnet %s", existingTailnet), c.IP())
	return c.JSON(fiber.Map{"disconnected": true})
}

func listTailscaleDevices(c *fiber.Ctx) error {
	clientID, clientSecret, tailnet, ok, err := loadTailscaleCredential()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Not connected to Tailscale"})
	}
	cl := tailscaleClientFactory(clientID, clientSecret)
	devs, err := cl.ListDevices(c.UserContext(), tailnet)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": classifyTailscaleError(err, "Failed to list devices from Tailscale"),
		})
	}
	return c.JSON(fiber.Map{"devices": devs, "tailnet": tailnet})
}

// loadTailscaleCredential reads + decrypts the stored credential.
// Returns ok=false if no connection exists.
func loadTailscaleCredential() (clientID, clientSecret, tailnet string, ok bool, err error) {
	var encID, encSecret, tn string
	scanErr := db.DB.QueryRow(
		`SELECT oauth_client_id_encrypted, oauth_client_secret_encrypted, tailnet FROM tailscale_connection WHERE id = 'singleton'`,
	).Scan(&encID, &encSecret, &tn)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return "", "", "", false, nil
	}
	if scanErr != nil {
		return "", "", "", false, fmt.Errorf("read tailscale connection: %w", scanErr)
	}
	clientID, err = crypto.Decrypt(encID)
	if err != nil {
		return "", "", "", false, fmt.Errorf("decrypt client_id: %w", err)
	}
	clientSecret, err = crypto.Decrypt(encSecret)
	if err != nil {
		return "", "", "", false, fmt.Errorf("decrypt client_secret: %w", err)
	}
	return clientID, clientSecret, tn, true, nil
}

func runValidationChecks(ctx context.Context, clientID, clientSecret, tailnet string) error {
	cl := tailscaleClientFactory(clientID, clientSecret)
	if err := cl.Authenticate(ctx); err != nil {
		return fmt.Errorf("authentication: %s", classifyTailscaleError(err, ""))
	}
	if err := cl.ValidateAuthKeyScope(ctx, tailnet); err != nil {
		return fmt.Errorf("auth_keys scope: %s", classifyTailscaleError(err, ""))
	}
	if err := cl.ValidateDeviceListScope(ctx, tailnet); err != nil {
		return fmt.Errorf("devices scope: %s", classifyTailscaleError(err, ""))
	}
	return nil
}

func classifyTailscaleError(err error, fallback string) string {
	switch {
	case errors.Is(err, tailscale.ErrTailscaleUnreachable):
		return "Tailscale control plane unreachable. Check connectivity and retry."
	case errors.Is(err, tailscale.ErrTailscaleAuthFailed):
		return "Authentication failed. Verify the OAuth client_id / client_secret at https://login.tailscale.com/admin/settings/oauth"
	case errors.Is(err, tailscale.ErrTailscaleScopeMissingAuthKeys):
		return "OAuth client missing the 'auth_keys' (write) scope. Edit it at https://login.tailscale.com/admin/settings/oauth"
	case errors.Is(err, tailscale.ErrTailscaleScopeMissingDeviceList):
		return "OAuth client missing the 'devices' (read) scope. Edit it at https://login.tailscale.com/admin/settings/oauth"
	case errors.Is(err, tailscale.ErrTailscaleRateLimited):
		var rl *tailscale.RateLimitedError
		if errors.As(err, &rl) {
			return fmt.Sprintf("Tailscale rate limit hit. Retry after %d seconds.", rl.RetryAfterSeconds)
		}
		return "Tailscale rate limit hit. Retry shortly."
	}
	if fallback != "" {
		return fallback + ": " + err.Error()
	}
	return err.Error()
}

func tailnetsList(tns []tailscale.Tailnet) string {
	names := make([]string, 0, len(tns))
	for _, t := range tns {
		names = append(names, t.Name)
	}
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += url.PathEscape(n)
	}
	return out
}
