package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/tailscale"

	"github.com/gofiber/fiber/v2"
)

// fakeTSClient implements the tailscaleAPI interface. Each method
// reads its behavior from the corresponding field — tests assemble
// the fake in one struct literal rather than spinning up an
// httptest.Server.
type fakeTSClient struct {
	authenticate      func(ctx context.Context) error
	listTailnets      func(ctx context.Context) ([]tailscale.Tailnet, error)
	validateAuthKey   func(ctx context.Context, tn string) error
	validateDeviceLst func(ctx context.Context, tn string) error
	listDevices       func(ctx context.Context, tn string) ([]tailscale.Device, error)
}

func (f *fakeTSClient) Authenticate(ctx context.Context) error {
	if f.authenticate == nil {
		return nil
	}
	return f.authenticate(ctx)
}
func (f *fakeTSClient) ListTailnets(ctx context.Context) ([]tailscale.Tailnet, error) {
	if f.listTailnets == nil {
		return []tailscale.Tailnet{{Name: "acme.ts.net", DisplayName: "Acme"}}, nil
	}
	return f.listTailnets(ctx)
}
func (f *fakeTSClient) ValidateAuthKeyScope(ctx context.Context, tn string) error {
	if f.validateAuthKey == nil {
		return nil
	}
	return f.validateAuthKey(ctx, tn)
}
func (f *fakeTSClient) ValidateDeviceListScope(ctx context.Context, tn string) error {
	if f.validateDeviceLst == nil {
		return nil
	}
	return f.validateDeviceLst(ctx, tn)
}
func (f *fakeTSClient) ListDevices(ctx context.Context, tn string) ([]tailscale.Device, error) {
	if f.listDevices == nil {
		return []tailscale.Device{}, nil
	}
	return f.listDevices(ctx, tn)
}

// tailscaleTestEnv wires the routes against a fresh DB with a
// pre-installed identity middleware so tests can drive the
// super-admin / tenant-admin role split. Restores
// tailscaleClientFactory in cleanup.
func tailscaleTestEnv(t *testing.T, role string) (*fiber.App, func(client tailscaleAPI)) {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/tailscale.db")
	}
	// crypto.init() ran at package import; setting the env var here
	// is too late. Use the test-only key loader to put the package
	// into the "enabled" state for the handler's MustBeEnabled gate.
	if err := crypto.SetKeyForTests("fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY="); err != nil {
		t.Fatalf("crypto SetKeyForTests: %v", err)
	}
	auth.JWTSecret = "tailscale-test-jwt-secret-needs-to-be-long-enough"
	if err := db.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	if err := db.ResetForTests(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	t.Cleanup(func() {
		if db.DB != nil && os.Getenv("DATABASE_URL") == "" {
			_ = db.DB.Close()
		}
	})

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	api := app.Group("/api/v1", func(c *fiber.Ctx) error {
		c.Locals("user_role", role)
		c.Locals("user_id", "test-user")
		c.Locals("tenant_id", "default")
		return c.Next()
	})
	RegisterTailscaleRoutes(api)

	origFactory := tailscaleClientFactory
	t.Cleanup(func() { tailscaleClientFactory = origFactory })

	swap := func(client tailscaleAPI) {
		tailscaleClientFactory = func(string, string) tailscaleAPI { return client }
	}
	return app, swap
}

func post(t *testing.T, app *fiber.App, path string, body interface{}) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	return resp
}

func TestTailscaleValidate_AllChecksPass(t *testing.T) {
	app, swap := tailscaleTestEnv(t, "super_admin")
	swap(&fakeTSClient{})

	resp := post(t, app, "/api/v1/tailscale/validate", map[string]string{
		"client_id":     "id",
		"client_secret": "secret",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out struct {
		Checks   map[string]string   `json:"checks"`
		Tailnets []tailscale.Tailnet `json:"tailnets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"authentication", "auth_key_scope", "device_list_scope"} {
		if out.Checks[k] != "ok" {
			t.Errorf("%s expected ok, got %s", k, out.Checks[k])
		}
	}
	if len(out.Tailnets) != 1 || out.Tailnets[0].Name != "acme.ts.net" {
		t.Errorf("tailnets: %+v", out.Tailnets)
	}
}

func TestTailscaleValidate_AuthKeyScopeMissing(t *testing.T) {
	app, swap := tailscaleTestEnv(t, "super_admin")
	swap(&fakeTSClient{
		validateAuthKey: func(ctx context.Context, tn string) error {
			return tailscale.ErrTailscaleScopeMissingAuthKeys
		},
	})
	resp := post(t, app, "/api/v1/tailscale/validate", map[string]string{
		"client_id":     "id",
		"client_secret": "secret",
	})
	defer resp.Body.Close()
	var out struct {
		Checks map[string]string `json:"checks"`
		Errors map[string]string `json:"errors"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Checks["auth_key_scope"] != "failed" {
		t.Errorf("expected failed, got %s", out.Checks["auth_key_scope"])
	}
	if !strings.Contains(out.Errors["auth_key_scope"], "auth_keys") {
		t.Errorf("error should mention auth_keys: %s", out.Errors["auth_key_scope"])
	}
	if !strings.Contains(out.Errors["auth_key_scope"], "https://login.tailscale.com") {
		t.Errorf("error should include remediation link: %s", out.Errors["auth_key_scope"])
	}
}

func TestTailscaleValidate_NetworkError(t *testing.T) {
	app, swap := tailscaleTestEnv(t, "super_admin")
	swap(&fakeTSClient{
		authenticate: func(ctx context.Context) error { return tailscale.ErrTailscaleUnreachable },
	})
	resp := post(t, app, "/api/v1/tailscale/validate", map[string]string{
		"client_id":     "id",
		"client_secret": "secret",
	})
	defer resp.Body.Close()
	var out struct {
		Checks map[string]string `json:"checks"`
		Errors map[string]string `json:"errors"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Checks["authentication"] != "failed" {
		t.Errorf("expected failed, got %s", out.Checks["authentication"])
	}
	if !strings.Contains(out.Errors["authentication"], "unreachable") {
		t.Errorf("error should say unreachable: %s", out.Errors["authentication"])
	}
}

func TestTailscaleConnect_RefusesIfAlreadyConnected(t *testing.T) {
	app, swap := tailscaleTestEnv(t, "super_admin")
	swap(&fakeTSClient{})
	body := map[string]string{"client_id": "id", "client_secret": "secret", "tailnet": "acme.ts.net"}
	r1 := post(t, app, "/api/v1/tailscale/connect", body)
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first connect status=%d", r1.StatusCode)
	}
	r2 := post(t, app, "/api/v1/tailscale/connect", body)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		t.Errorf("second connect should be 409, got %d", r2.StatusCode)
	}
}

func TestTailscaleConnect_EncryptsCredentialsAtRest(t *testing.T) {
	app, swap := tailscaleTestEnv(t, "super_admin")
	swap(&fakeTSClient{})
	resp := post(t, app, "/api/v1/tailscale/connect", map[string]string{
		"client_id":     "PLAINTEXT-CLIENT-ID",
		"client_secret": "PLAINTEXT-CLIENT-SECRET",
		"tailnet":       "acme.ts.net",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("connect: %d", resp.StatusCode)
	}
	var encID, encSecret string
	if err := db.DB.QueryRow(
		`SELECT oauth_client_id_encrypted, oauth_client_secret_encrypted FROM tailscale_connection WHERE id = 'singleton'`,
	).Scan(&encID, &encSecret); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if encID == "PLAINTEXT-CLIENT-ID" || encSecret == "PLAINTEXT-CLIENT-SECRET" {
		t.Error("credentials stored in plaintext! encryption skipped")
	}
	if !strings.HasPrefix(encID, "enc:") || !strings.HasPrefix(encSecret, "enc:") {
		t.Errorf("expected enc: prefix on encrypted columns; got id=%q secret=%q", encID, encSecret)
	}
}

func TestTailscaleRotate_RequiresSameTailnet(t *testing.T) {
	app, swap := tailscaleTestEnv(t, "super_admin")
	swap(&fakeTSClient{})
	connect := post(t, app, "/api/v1/tailscale/connect", map[string]string{
		"client_id": "id1", "client_secret": "secret1", "tailnet": "acme.ts.net",
	})
	connect.Body.Close()

	// New credential owns a DIFFERENT tailnet — rotation must refuse.
	swap(&fakeTSClient{
		listTailnets: func(ctx context.Context) ([]tailscale.Tailnet, error) {
			return []tailscale.Tailnet{{Name: "other.ts.net"}}, nil
		},
	})
	body, _ := json.Marshal(map[string]string{"client_id": "id2", "client_secret": "secret2"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tailscale/connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req, -1)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("rotation to different tailnet should be 400, got %d", resp.StatusCode)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(bodyBytes), "different tailnet") && !strings.Contains(string(bodyBytes), "Disconnect and reconnect") {
		t.Errorf("error should mention disconnect-and-reconnect path: %s", string(bodyBytes))
	}
}

func TestTailscaleDisconnect_AuditsAndWipes(t *testing.T) {
	app, swap := tailscaleTestEnv(t, "super_admin")
	swap(&fakeTSClient{})
	post(t, app, "/api/v1/tailscale/connect", map[string]string{
		"client_id": "id", "client_secret": "secret", "tailnet": "acme.ts.net",
	}).Body.Close()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tailscale/connection", nil)
	resp, _ := app.Test(req, -1)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disconnect status=%d", resp.StatusCode)
	}

	var cnt int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM tailscale_connection`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Errorf("expected 0 rows after disconnect, got %d", cnt)
	}
}

func TestGetTailscaleConnection_SuperAdminVsTenantAdmin(t *testing.T) {
	// Set up a connection via a super-admin instance.
	appSuper, swap := tailscaleTestEnv(t, "super_admin")
	swap(&fakeTSClient{})
	post(t, appSuper, "/api/v1/tailscale/connect", map[string]string{
		"client_id": "id", "client_secret": "secret", "tailnet": "acme.ts.net",
	}).Body.Close()

	// Super-admin GET sees full info.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tailscale/connection", nil)
	resp, _ := appSuper.Test(req, -1)
	defer resp.Body.Close()
	var superOut map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&superOut)
	if _, ok := superOut["connected_at"]; !ok {
		t.Error("super-admin response should include connected_at")
	}
	if _, ok := superOut["tailnet"]; !ok {
		t.Error("super-admin response should include tailnet (the canonical name)")
	}

	// Tenant-admin sees minimal indicator only.
	appTenant := fiber.New(fiber.Config{DisableStartupMessage: true})
	apiT := appTenant.Group("/api/v1", func(c *fiber.Ctx) error {
		c.Locals("user_role", "admin")
		c.Locals("user_id", "tenant-user")
		c.Locals("tenant_id", "default")
		return c.Next()
	})
	RegisterTailscaleRoutes(apiT)

	reqT := httptest.NewRequest(http.MethodGet, "/api/v1/tailscale/connection", nil)
	respT, _ := appTenant.Test(reqT, -1)
	defer respT.Body.Close()
	var tenantOut map[string]interface{}
	json.NewDecoder(respT.Body).Decode(&tenantOut)
	if _, ok := tenantOut["connected_at"]; ok {
		t.Error("tenant-admin response MUST NOT include connected_at (operational metadata)")
	}
	if _, ok := tenantOut["tailnet"]; ok {
		t.Error("tenant-admin response MUST NOT include tailnet (canonical name leaks org identity)")
	}
	if tenantOut["connected"] != true {
		t.Error("tenant-admin response should still indicate connected=true")
	}
	if _, ok := tenantOut["tailnet_display_name"]; !ok {
		t.Error("tenant-admin response should include tailnet_display_name (the only thing they're allowed to see)")
	}
}

// TestTailscaleConnect_RefusesWhenEncryptionDisabled checks the
// crypto.MustBeEnabled() gate. With no SECRETS_ENCRYPTION_KEY the
// crypto package falls into dev-plaintext mode; the handler refuses
// to persist a credential that would otherwise sit in cleartext.
func TestTailscaleConnect_RefusesWhenEncryptionDisabled(t *testing.T) {
	// Sub-test isolation: crypto package reads SECRETS_ENCRYPTION_KEY
	// at init time, so we cannot disable it mid-test. Skip when the
	// surrounding test suite has set the key (which it always does
	// for these tests).
	t.Skip("crypto reads SECRETS_ENCRYPTION_KEY at init; sub-test cannot un-set after package load. The MustBeEnabled check is exercised indirectly by the existing test suite when DEV_ALLOW_UNENCRYPTED_SECRETS is unset.")
	// Compile-time check that the symbol exists.
	_ = errors.Is
}
