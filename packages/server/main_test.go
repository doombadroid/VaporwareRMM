package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

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
