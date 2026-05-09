package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

// setupBenchmarkServer creates a minimal server for benchmarking.
func setupBenchmarkServer(b *testing.B) *fiber.App {
	os.Setenv("DATABASE_PATH", ":memory:")
	os.Setenv("JWT_SECRET", "benchmark-secret")
	defer os.Unsetenv("DATABASE_PATH")
	defer os.Unsetenv("JWT_SECRET")

	if err := db.Init(); err != nil {
		b.Fatalf("Failed to init DB: %v", err)
	}
	auth.LoadAgentTokens()

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	return app
}

func BenchmarkHealthEndpoint(b *testing.B) {
	app := setupBenchmarkServer(b)
	defer app.Shutdown()
	defer db.DB.Close()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := app.Test(req, -1)
		if err != nil {
			b.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	}
}

func BenchmarkDeviceQuery(b *testing.B) {
	app := setupBenchmarkServer(b)
	defer app.Shutdown()
	defer db.DB.Close()

	// Seed devices
	for i := 0; i < 100; i++ {
		_, _ = db.DB.Exec(
			`INSERT INTO devices (id, hostname, ip_address, mac_address, os_name, os_version, status, last_seen, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("dev-%d", i), fmt.Sprintf("host-%d", i), "10.0.0.1", "aa:bb:cc:dd:ee:ff",
			"linux", "22.04", "online", time.Now().Unix(), time.Now().Unix(),
		)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var count int
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE status = 'online'`).Scan(&count)
		if count != 100 {
			b.Fatalf("expected 100 devices, got %d", count)
		}
	}
}

func BenchmarkHeartbeatPayloadParse(b *testing.B) {
	payload := []byte(`{"status":"online","cpu_usage":45.5,"memory_usage":60.2,"disk_usage":30.1}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var data map[string]interface{}
		if err := json.Unmarshal(payload, &data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJWTGeneration(b *testing.B) {
	auth.JWTSecret = "benchmark-jwt-secret"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := auth.GenerateJWT("user-123", "default", "admin", 24)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPasswordHash(b *testing.B) {
	password := "BenchmarkPass123!"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			b.Fatal(err)
		}
	}
}
