package auth

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"vaporrmm/models"
	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
)

func initTestDB(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	os.Setenv("DATABASE_PATH", tmpDir+"/test.db")
	if err := db.Init(); err != nil {
		t.Fatalf("db.Init failed: %v", err)
	}
	db.EnsureDefaultTenant()
	t.Cleanup(func() { db.DB.Close() })
}

func TestGenerateJWT_ValidateJWT(t *testing.T) {
	JWTSecret = "test-secret-key-that-is-long-enough"

	t.Run("valid token", func(t *testing.T) {
		token, err := GenerateJWT("user-123", "default", "admin", 1)
		if err != nil {
			t.Fatalf("GenerateJWT failed: %v", err)
		}
		if token == "" {
			t.Fatal("expected non-empty token")
		}

		userID, _, role, err := ValidateJWT(token)
		if err != nil {
			t.Fatalf("ValidateJWT failed: %v", err)
		}
		if userID != "user-123" {
			t.Errorf("userID = %q, want user-123", userID)
		}
		if role != "admin" {
			t.Errorf("role = %q, want admin", role)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		token, err := GenerateJWT("user-123", "default", "admin", -1)
		if err != nil {
			t.Fatalf("GenerateJWT failed: %v", err)
		}

		_, _, _, err = ValidateJWT(token)
		if err == nil {
			t.Fatal("expected error for expired token")
		}
		if !strings.Contains(err.Error(), "expired") {
			t.Errorf("expected expired error, got: %v", err)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		token, _ := GenerateJWT("user-123", "default", "admin", 1)
		token += "tampered"

		_, _, _, err := ValidateJWT(token)
		if err == nil {
			t.Fatal("expected error for tampered token")
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		_, _, _, err := ValidateJWT("not.a.jwt")
		if err == nil {
			t.Fatal("expected error for malformed token")
		}
	})
}

func TestAgentAuthMiddleware(t *testing.T) {
	initTestDB(t)
	JWTSecret = "test-secret-key-that-is-long-enough"
	RegisteredTokens = make(map[string]*models.AgentToken)
	HashToken = func(token string) string { return "hash-" + token }

	RegisterAgentToken("valid-token", "device-1", "host-a", "default")

	app := fiber.New()
	app.Use(AgentAuthMiddleware())
	app.Get("/test", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"device_id": c.Locals("device_id"),
			"hostname":  c.Locals("hostname"),
		})
	})

	t.Run("valid token", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer valid-token")

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("missing header", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/test", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		TokenMu.Lock()
		RegisteredTokens["hash-expired-token"] = &models.AgentToken{
			TokenHash: "hash-expired-token",
			DeviceID:  "device-2",
			Hostname:  "host-b",
			ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(),
		}
		TokenMu.Unlock()

		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer expired-token")

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})
}

func TestRateLimiter(t *testing.T) {
	app := fiber.New()
	app.Use(RateLimiter(3, time.Minute))
	app.Get("/test", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	// Use a consistent IP
	clientIP := "1.2.3.4"

	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Forwarded-For", clientIP)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
	}

	// 4th request should be rate limited
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Forwarded-For", clientIP)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request 4 failed: %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("request 4: status = %d, want 429", resp.StatusCode)
	}
}

func TestValidatePasswordStrength(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"valid", "Password1", true},
		{"too short", "Pass1", false},
		{"no upper", "password1", false},
		{"no lower", "PASSWORD1", false},
		{"no digit", "Password", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePasswordStrength(tc.pw)
			if tc.ok && err != nil {
				t.Errorf("expected ok, got error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}
