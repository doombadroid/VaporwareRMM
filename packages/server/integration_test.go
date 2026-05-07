package main

import (
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
)

// setupTestServer creates a test server with an in-memory SQLite database.
func setupTestServer(t *testing.T) *fiber.App {
	os.Setenv("DATABASE_PATH", ":memory:")
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("DATABASE_PATH")
	defer os.Unsetenv("JWT_SECRET")

	// Initialize database
	if err := db.Init(); err != nil {
		t.Fatalf("Failed to initialize test database: %v", err)
	}

	// Load agent tokens
	auth.LoadAgentTokens()

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	// Register routes
	app.Get("/health", func(c *fiber.Ctx) error {
		if err := db.DB.Ping(); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(models.StatusResponse{
				Status:  "unhealthy",
				Version: "test",
			})
		}
		return c.JSON(models.StatusResponse{
			Status:  "ok",
			Version: "test",
		})
	})

	return app
}

func TestIntegrationHealthEndpoint(t *testing.T) {
	app := setupTestServer(t)
	defer app.Shutdown()

	req, err := http.NewRequest("GET", "/health", nil)
	require.NoError(t, err)

	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Contains(t, string(body), "ok")
}

func TestIntegrationDatabaseConnection(t *testing.T) {
	app := setupTestServer(t)
	defer app.Shutdown()

	// Verify database is accessible
	var count int
	err := db.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 0)
}

func TestIntegrationFileTransferDB(t *testing.T) {
	app := setupTestServer(t)
	defer app.Shutdown()
	defer db.DB.Close()

	// Seed a device
	_, err := db.DB.Exec(
		`INSERT INTO devices (id, hostname, ip_address, mac_address, os_name, os_version, status, last_seen, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"ft-test-device", "ft-host", "10.0.0.1", "aa:bb:cc:dd:ee:ff",
		"linux", "22.04", "online", 0, 0,
	)
	require.NoError(t, err)

	// Insert a file transfer directly
	_, err = db.DB.Exec(
		`INSERT INTO file_transfers (id, device_id, type, file_name, file_path, status, progress, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"ft-1", "ft-test-device", "upload", "test.txt", "/tmp/test.txt", "pending", 0, 0,
	)
	require.NoError(t, err)

	// Verify it exists
	var count int
	err = db.DB.QueryRow(`SELECT COUNT(*) FROM file_transfers WHERE device_id = ?`, "ft-test-device").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify columns
	var id, dtype, fileName string
	err = db.DB.QueryRow(
		`SELECT id, type, file_name FROM file_transfers WHERE id = ?`, "ft-1",
	).Scan(&id, &dtype, &fileName)
	require.NoError(t, err)
	assert.Equal(t, "upload", dtype)
	assert.Equal(t, "test.txt", fileName)
}

func TestIntegrationAlertSettingsDB(t *testing.T) {
	app := setupTestServer(t)
	defer app.Shutdown()
	defer db.DB.Close()

	// Insert alert settings
	_, err := db.DB.Exec(
		`INSERT INTO alert_settings (id, smtp_host, smtp_port, smtp_user, smtp_password, smtp_from, smtp_tls, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"default", "smtp.example.com", 587, "user", "pass", "alerts@example.com", 1, 1, 0, 0,
	)
	require.NoError(t, err)

	// Verify
	var host string
	var port int
	var enabled int
	err = db.DB.QueryRow(
		`SELECT smtp_host, smtp_port, enabled FROM alert_settings WHERE id = 'default'`,
	).Scan(&host, &port, &enabled)
	require.NoError(t, err)
	assert.Equal(t, "smtp.example.com", host)
	assert.Equal(t, 587, port)
	assert.Equal(t, 1, enabled)

	// Insert alert rule
	_, err = db.DB.Exec(
		`INSERT INTO alert_rules (id, name, event_type, severity, enabled, email_recipients, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"rule-1", "Device Offline", "device.offline", "high", 1, "admin@example.com", 0, 0,
	)
	require.NoError(t, err)

	var ruleCount int
	err = db.DB.QueryRow(`SELECT COUNT(*) FROM alert_rules WHERE event_type = ?`, "device.offline").Scan(&ruleCount)
	require.NoError(t, err)
	assert.Equal(t, 1, ruleCount)
}
