package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	_ "github.com/mattn/go-sqlite3"
)

// Device represents a managed device
type Device struct {
	ID              int64     `json:"id" db:"id"`
	Name            string    `json:"name" db:"name"`
	IPAddress       string    `json:"ip_address" db:"ip_address"`
	Status          string    `json:"status" db:"status"`
	LastSeen        time.Time `json:"last_seen" db:"last_seen"`
	OS              string    `json:"os" db:"os"`
	Model           string    `json:"model" db:"model"`
	SerialNumber    string    `json:"serial_number" db:"serial_number"`
	AgentVersion    string    `json:"agent_version" db:"agent_version"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// AgentStatus represents the agent connection status
type AgentStatus struct {
	Status     string `json:"status"`
	Timestamp  int64  `json:"timestamp"`
	Connection string `json:"connection"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	app := fiber.New(fiber.Config{
		AppName:        "Vapor RMM Server",
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    60 * time.Second,
	})

	// Middleware
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: []string{"Origin", "Content-Type", "Authorization"},
	}))

	// Initialize database
	db, err := initDB()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Create tables if they don't exist
	createTables(db)

	// Routes
	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"message": "Vapor RMM API",
			"version": "1.0.0",
		})
	})

	// Device routes
	app.Get("/api/devices", getDevices(db))
	app.Post("/api/devices", createDevice(db))
	app.Get("/api/devices/:id", getDevice(db))
	app.Put("/api/devices/:id", updateDevice(db))
	app.Delete("/api/devices/:id", deleteDevice(db))

	// Agent status endpoints (called by agents)
	app.Post("/api/agents/register", registerAgent(db))
	app.Post("/api/agents/status", updateAgentStatus(db))
	app.Get("/api/agents/ping/:id", pingAgent)

	// Start server
	log.Printf("Starting Vapor RMM Server on port %s", port)
	if err := app.Listen(fmt.Sprintf(":%s", port)); err != nil {
		log.Fatal(err)
	}
}

func initDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "./vapor_rmm.db")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

func createTables(db *sql.DB) {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS devices (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			ip_address TEXT,
			status TEXT DEFAULT 'offline',
			last_seen DATETIME,
			os TEXT,
			model TEXT,
			serial_number TEXT UNIQUE,
			agent_version TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS agent_status (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			device_id INTEGER,
			status TEXT DEFAULT 'offline',
			connection_type TEXT,
			last_ping DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (device_id) REFERENCES devices(id)
		)`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			log.Printf("Warning: Failed to create table: %v", err)
		}
	}
}

func getDevices(db *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var devices []Device
		rows, err := db.Query("SELECT id, name, ip_address, status, last_seen, os, model, serial_number, agent_version, created_at, updated_at FROM devices ORDER BY created_at DESC")
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch devices"})
		}
		defer rows.Close()

		for rows.Next() {
			var d Device
			err := rows.Scan(&d.ID, &d.Name, &d.IPAddress, &d.Status, &d.LastSeen, &d.OS, &d.Model, &d.SerialNumber, &d.AgentVersion, &d.CreatedAt, &d.UpdatedAt)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to scan device"})
			}
			devices = append(devices, d)
		}

		return c.JSON(devices)
	}
}

func createDevice(db *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var device Device
		if err := c.BodyParser(&device); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}

		result, err := db.Exec(
			"INSERT INTO devices (name, ip_address, status, os, model, serial_number, agent_version) VALUES (?, ?, ?, ?, ?, ?, ?)",
			device.Name, device.IPAddress, device.Status, device.OS, device.Model, device.SerialNumber, device.AgentVersion,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create device"})
		}

		id, _ := result.LastInsertId()
		device.ID = id
		device.CreatedAt = time.Now()

		return c.JSON(device)
	}
}

func getDevice(db *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Params("id")

		var device Device
		err := db.QueryRow(
			"SELECT id, name, ip_address, status, last_seen, os, model, serial_number, agent_version, created_at, updated_at FROM devices WHERE id = ?",
			id,
		).Scan(&device.ID, &device.Name, &device.IPAddress, &device.Status, &device.LastSeen, &device.OS, &device.Model, &device.SerialNumber, &device.AgentVersion, &device.CreatedAt, &device.UpdatedAt)

		if err == sql.ErrNoRows {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch device"})
		}

		return c.JSON(device)
	}
}

func updateDevice(db *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Params("id")
		var device Device
		if err := c.BodyParser(&device); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}

		result, err := db.Exec(
			"UPDATE devices SET name = ?, ip_address = ?, os = ?, model = ? WHERE id = ?",
			device.Name, device.IPAddress, device.OS, device.Model, id,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update device"})
		}

		affected, _ := result.RowsAffected()
		if affected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		return c.JSON(device)
	}
}

func deleteDevice(db *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Params("id")

		result, err := db.Exec("DELETE FROM devices WHERE id = ?", id)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete device"})
		}

		affected, _ := result.RowsAffected()
		if affected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		return c.JSON(fiber.Map{"message": "Device deleted successfully"})
	}
}

func registerAgent(db *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var device Device
		if err := c.BodyParser(&device); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}

		// Check if agent already exists
		var existingID int64
		err := db.QueryRow("SELECT id FROM devices WHERE serial_number = ?", device.SerialNumber).Scan(&existingID)

		if err == sql.ErrNoRows {
			// New registration
			result, err := db.Exec(
				"INSERT INTO devices (name, ip_address, status, os, model, serial_number, agent_version) VALUES (?, ?, 'online', ?, ?, ?, ?)",
				device.Name, c.IP(), device.OS, device.Model, device.SerialNumber, device.AgentVersion,
			)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to register agent"})
			}
			id, _ := result.LastInsertId()
			device.ID = id
		} else if err == nil {
			// Update existing device
			db.Exec(
				"UPDATE devices SET name = ?, ip_address = ?, os = ?, model = ?, agent_version = ?, status = 'online', last_seen = CURRENT_TIMESTAMP WHERE serial_number = ?",
				device.Name, c.IP(), device.OS, device.Model, device.AgentVersion, device.SerialNumber,
			)
			device.ID = existingID
		} else {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Database error"})
		}

		return c.JSON(fiber.Map{
			"message":     "Agent registered successfully",
			"device_id":   device.ID,
			"registered":  err == sql.ErrNoRows,
		})
	}
}

func updateAgentStatus(db *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var status AgentStatus
		if err := c.BodyParser(&status); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
		}

		deviceID := c.Query("device_id")
		if deviceID == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "device_id query parameter required"})
		}

		_, err := db.Exec(
			"UPDATE devices SET status = ?, last_seen = CURRENT_TIMESTAMP WHERE id = ?",
			status.Status, deviceID,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update agent status"})
		}

		return c.JSON(fiber.Map{"message": "Status updated successfully"})
	}
}

func pingAgent(c *fiber.Ctx) error {
	deviceID := c.Params("id")
	return c.JSON(fiber.Map{
		"device_id": deviceID,
		"status":    "pong",
		"timestamp": time.Now().Unix(),
	})
}

// WebSocket support for real-time notifications
func setupWebSocket(app *fiber.App) {
	// WebSocket routes would be added here using fiber/websocket middleware
}