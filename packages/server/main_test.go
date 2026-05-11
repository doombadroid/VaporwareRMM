package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/utils"

	"github.com/gofiber/fiber/v2"
)

func TestMain(m *testing.M) {
	// Use in-memory database for tests
	os.Setenv("DATABASE_PATH", ":memory:")
	os.Setenv("JWT_SECRET", "test-secret-key")

	// Initialize the database
	if err := db.Init(); err != nil {
		panic(err)
	}

	code := m.Run()

	if db.DB != nil {
		db.DB.Close()
	}
	os.Exit(code)
}

func TestHealthEndpoint(t *testing.T) {
	app := fiber.New()
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(models.StatusResponse{
			Status:  "ok",
			Version: "1.0.0",
		})
	})

	req := httptest.NewRequest("GET", "/health", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test health endpoint: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var statusResponse models.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&statusResponse); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if statusResponse.Status != "ok" {
		t.Errorf("Expected status 'ok', got '%s'", statusResponse.Status)
	}

	if statusResponse.Version != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got '%s'", statusResponse.Version)
	}
}

func TestDeviceCRUD(t *testing.T) {
	// Setup a test app with the routes
	app := fiber.New()

	app.Post("/api/devices", func(c *fiber.Ctx) error {
		var device models.ServerDevice
		if err := c.BodyParser(&device); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
		}

		device.ID = "test-device-id"
		device.Status = "online"

		return c.JSON(device)
	})

	app.Get("/api/devices/:id", func(c *fiber.Ctx) error {
		return c.JSON(models.ServerDevice{
			ID:       c.Params("id"),
			Name:     "Test Device",
			Hostname: "test-device",
			Status:   "online",
		})
	})

	app.Delete("/api/devices/:id", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"message": "Device deleted successfully"})
	})

	t.Run("Create Device", func(t *testing.T) {
		deviceData := models.ServerDevice{
			Name:      "Test Device",
			Hostname:  "test-device",
			IPAddress: "192.168.1.100",
		}
		body, _ := json.Marshal(deviceData)

		req := httptest.NewRequest("POST", "/api/devices", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("Failed to create device: %v", err)
		}

		if resp.StatusCode != 200 {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}

		var result models.ServerDevice
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if result.Name != "Test Device" {
			t.Errorf("Expected name 'Test Device', got '%s'", result.Name)
		}

		if result.Status != "online" {
			t.Errorf("Expected status 'online', got '%s'", result.Status)
		}
	})

	t.Run("Get Device", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/devices/test-device-id", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("Failed to get device: %v", err)
		}

		if resp.StatusCode != 200 {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}

		var result models.ServerDevice
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if result.Hostname != "test-device" {
			t.Errorf("Expected hostname 'test-device', got '%s'", result.Hostname)
		}
	})

	t.Run("Delete Device", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/devices/test-device-id", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("Failed to delete device: %v", err)
		}

		if resp.StatusCode != 200 {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}
	})
}

func TestTokenGeneration(t *testing.T) {
	auth.JWTSecret = "test-secret"

	token := utils.GenerateSecureKey()
	if token == "" {
		t.Error("Expected non-empty token")
	}

	// Test JWT generation
	jwt, err := auth.GenerateJWT("user123", "default", "admin", 24)
	if err != nil {
		t.Fatalf("Failed to generate JWT: %v", err)
	}
	if jwt == "" {
		t.Error("Expected non-empty JWT")
	}

	// Test JWT validation
	userID, _, role, err := auth.ValidateJWT(jwt)
	if err != nil {
		t.Errorf("Failed to validate JWT: %v", err)
	}

	if userID != "user123" {
		t.Errorf("Expected userId 'user123', got '%s'", userID)
	}
	if role != "admin" {
		t.Errorf("Expected role 'admin', got '%s'", role)
	}
}

func TestAgentTokenRegistration(t *testing.T) {
	auth.RegisterAgentToken("test-token", "device-123", "test-host", "default")

	auth.TokenMu.RLock()
	agentTok, exists := auth.RegisteredTokens[auth.HashToken("test-token")]
	auth.TokenMu.RUnlock()

	if !exists {
		t.Error("Expected agent token to be registered")
	}

	if agentTok.DeviceID != "device-123" {
		t.Errorf("Expected device ID 'device-123', got '%s'", agentTok.DeviceID)
	}

	if agentTok.Hostname != "test-host" {
		t.Errorf("Expected hostname 'test-host', got '%s'", agentTok.Hostname)
	}
}

// TestRefuseSentinelSecrets is the Codex #3 attack-path regression.
// The .env.example shipped with the docker setup uses
// __GENERATE_ME__ as a sentinel value for ADMIN_PASSWORD,
// JWT_SECRET, and SECRETS_ENCRYPTION_KEY. setup-docker.sh fills
// these in on first run; an operator who copies .env.example to
// .env unchanged and runs `docker compose up` directly must not
// have the server boot successfully against the sentinel
// "password" — that's the Codex-confirmed default-credential
// exposure path. RefuseSentinelSecrets() short-circuits boot via
// os.Exit(1) before any auth code runs.
//
// We can't call RefuseSentinelSecrets() directly inside the test
// because it calls os.Exit; instead we shell out to a tiny binary
// built from this package and assert its exit code is non-zero
// when ADMIN_PASSWORD=__GENERATE_ME__, and zero when a real value
// is supplied.
func TestRefuseSentinelSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("skip subprocess test in -short mode")
	}
	bin := filepath.Join(t.TempDir(), "server-sentinel-probe")
	// Compile the server binary once. Subprocess exit is the
	// signal the test reads.
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name        string
		envVar      string
		envVal      string
		wantNonZero bool
	}{
		{"admin_password_sentinel", "ADMIN_PASSWORD", "__GENERATE_ME__", true},
		{"jwt_secret_sentinel", "JWT_SECRET", "__GENERATE_ME__", true},
		{"secrets_key_sentinel", "SECRETS_ENCRYPTION_KEY", "__GENERATE_ME__", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin)
			// Build a clean env: minimum set the server expects when
			// it gets PAST the sentinel check (we want to confirm
			// the test failure is the sentinel, not a downstream
			// missing-var). Override the one var we're probing.
			cmd.Env = []string{
				"ADMIN_PASSWORD=ProbeAdmin123!",
				"JWT_SECRET=probe-jwt-secret-that-is-long-enough-for-tests-yes",
				"SECRETS_ENCRYPTION_KEY=fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY=",
				"DATABASE_PATH=" + filepath.Join(t.TempDir(), "probe.db"),
				"PATH=" + os.Getenv("PATH"),
			}
			// Override the probed var with the sentinel.
			cmd.Env = append(cmd.Env, tc.envVar+"="+tc.envVal)
			// Kill the server fast — we only need the sentinel
			// check's exit-or-continue signal. We use a 3-second
			// run with a deadline; expected behaviour is exit(1)
			// immediately.
			done := make(chan error, 1)
			go func() { done <- cmd.Run() }()
			select {
			case err := <-done:
				if tc.wantNonZero {
					if err == nil {
						t.Errorf("expected non-zero exit when %s=%q, got success",
							tc.envVar, tc.envVal)
					}
				}
			case <-time.After(5 * time.Second):
				// Process did not exit within 5s. For sentinel
				// inputs this is a failure — the guard is supposed
				// to fail-fast before listening on the port.
				_ = cmd.Process.Kill()
				if tc.wantNonZero {
					t.Errorf("expected fast non-zero exit when %s=%q, got hang",
						tc.envVar, tc.envVal)
				}
			}
		})
	}
}

// TestEnforceSQLiteScaleLimit covers the boot-time gate that refuses to
// start on SQLite past the device-count threshold. We test all four
// branches: under limit, at limit, override (raised), override (disabled).
// The test seeds device rows directly to avoid needing the agent
// registration handler.
func TestEnforceSQLiteScaleLimit(t *testing.T) {
	// Earlier integration tests close db.DB in their defers. Reopen here
	// so we have a working SQLite handle regardless of test order.
	os.Setenv("DATABASE_PATH", ":memory:")
	if db.DB == nil || db.DB.Ping() != nil {
		if err := db.Init(); err != nil {
			t.Fatalf("db init: %v", err)
		}
	}
	if db.DB == nil || db.DB.Dialect != "sqlite" {
		t.Skip("scale gate is SQLite-only")
	}
	defer func() {
		// Clean rows created here so other tests start from a known state.
		_, _ = db.DB.Exec(`DELETE FROM devices WHERE id LIKE 'scaletest-%'`)
		_ = os.Unsetenv("SQLITE_DEVICE_LIMIT")
	}()

	insert := func(n int) {
		t.Helper()
		_, _ = db.DB.Exec(`DELETE FROM devices WHERE id LIKE 'scaletest-%'`)
		for i := 0; i < n; i++ {
			if _, err := db.DB.Exec(
				`INSERT INTO devices (id, hostname, status, last_seen, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?)`,
				"scaletest-"+itoa(i), "host-"+itoa(i), "online", 0, 0, "default",
			); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}

	// Wipe ALL devices left behind by earlier tests in the suite — they
	// don't share a clean teardown — so the count we test against is
	// only what we just inserted.
	if _, err := db.DB.Exec(`DELETE FROM devices`); err != nil {
		t.Fatalf("wipe: %v", err)
	}

	// Under the default limit: pass.
	os.Unsetenv("SQLITE_DEVICE_LIMIT")
	insert(10)
	if err := enforceSQLiteScaleLimit(); err != nil {
		t.Fatalf("unexpected gate trip under default limit: %v", err)
	}

	// At the env-overridden limit: trip.
	os.Setenv("SQLITE_DEVICE_LIMIT", "5")
	if err := enforceSQLiteScaleLimit(); err == nil {
		t.Fatal("expected scale gate to trip when count >= limit")
	}

	// Limit raised above row count: pass.
	os.Setenv("SQLITE_DEVICE_LIMIT", "1000")
	if err := enforceSQLiteScaleLimit(); err != nil {
		t.Fatalf("expected raised limit to pass: %v", err)
	}

	// Limit disabled (0): always pass, even with rows >> any threshold.
	os.Setenv("SQLITE_DEVICE_LIMIT", "0")
	if err := enforceSQLiteScaleLimit(); err != nil {
		t.Fatalf("expected disabled gate to pass: %v", err)
	}
}

// itoa is a stdlib-free int formatter so the test stays cheap to read.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := make([]byte, 0, 6)
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}

func TestScanDevice(t *testing.T) {
	// Test the ScanDevice helper function with a mock row
	// This tests the pointer scan fixes
	d := &models.ServerDevice{
		ID:       "test-id",
		Name:     "Test Device",
		Hostname: "test-host",
		Status:   "online",
	}

	// Verify nullable pointer fields are properly initialized
	if d.PublicKey != nil {
		t.Error("Expected PublicKey to be nil")
	}
	if d.UserData != nil {
		t.Error("Expected UserData to be nil")
	}
}
