package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/google/uuid"
	"github.com/gofiber/fiber/v2"
)

// Device represents a managed device
type Device struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Hostname        string `json:"hostname"`
	IPAddress       string `json:"ip_address"`
	MacAddress      string `json:"mac_address"`
	OSName          string `json:"os_name"`
	OSVersion       string `json:"os_version"`
	KernelVersion   string `json:"kernel_version"`
	AgentVersion    string `json:"agent_version"`
	Status          string `json:"status"` // online, offline
	LastSeen        int64  `json:"last_seen"`
	CreatedAt       int64  `json:"created_at"`
	PublicKey       string `json:"public_key,omitempty"`
	UserData        string `json:"user_data,omitempty"`
	SystemUUID      string `json:"system_uuid,omitempty"`
	SerialNumber    string `json:"serial_number,omitempty"`
	Manufacturer    string `json:"manufacturer,omitempty"`
	Model           string `json:"model,omitempty"`
	CPU             string `json:"cpu,omitempty"`
	Memory          int64  `json:"memory,omitempty"` // in bytes
	DiskSize        int64  `json:"disk_size,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
}

// StatusResponse for health checks
type StatusResponse struct {
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
	Version   string `json:"version"`
}

var db *sql.DB

func initDB() error {
	var err error
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "./data/vapor_rmm.db"
	}
	
	os.MkdirAll("./data", 0755)
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}

	createTableSQL := `
	CREATE TABLE IF NOT EXISTS devices (
		id TEXT PRIMARY KEY,
		name TEXT,
		hostname TEXT,
		ip_address TEXT,
		mac_address TEXT,
		os_name TEXT,
		os_version TEXT,
		kernel_version TEXT,
		agent_version TEXT,
		status TEXT DEFAULT 'offline',
		last_seen INTEGER,
		created_at INTEGER,
		public_key TEXT,
		user_data TEXT,
		system_uuid TEXT,
		serial_number TEXT,
		manufacturer TEXT,
		model TEXT,
		cpu TEXT,
		memory INTEGER,
		disk_size INTEGER,
		timezone TEXT
	);`

	_, err = db.Exec(createTableSQL)
	return err
}

// Helper function for dynamic UPDATE queries
func joinStrings(strings []string, sep string) string {
	if len(strings) == 0 {
		return ""
	}
	result := strings[0]
	for i := 1; i < len(strings); i++ {
		result += sep + strings[i]
	}
	return result
}

func main() {
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	app := fiber.New()

	// Health check endpoint
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(StatusResponse{
			Status:  "ok",
			Version: "1.0.0",
		})
	})

	// Get all devices
	app.Get("/api/devices", func(c *fiber.Ctx) error {
		rows, err := db.Query("SELECT id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version, status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model, cpu, memory, disk_size, timezone FROM devices")
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(map[string]string{"error": "Failed to query devices"})
		}
		defer rows.Close()

		var devices []Device
		for rows.Next() {
			var d Device
			err := rows.Scan(
				&d.ID, &d.Name, &d.Hostname, &d.IPAddress, &d.MacAddress,
				&d.OSName, &d.OSVersion, &d.KernelVersion, &d.AgentVersion,
				&d.Status, &d.LastSeen, &d.CreatedAt, &d.PublicKey, &d.UserData,
				&d.SystemUUID, &d.SerialNumber, &d.Manufacturer, &d.Model,
				&d.CPU, &d.Memory, &d.DiskSize, &d.Timezone,
			)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(map[string]string{"error": "Failed to scan device"})
			}
			devices = append(devices, d)
		}

		return c.JSON(devices)
	})

	// Get device by ID
	app.Get("/api/devices/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		var d Device
		err := db.QueryRow("SELECT id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version, status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model, cpu, memory, disk_size, timezone FROM devices WHERE id = ?", id).Scan(
			&d.ID, &d.Name, &d.Hostname, &d.IPAddress, &d.MacAddress,
			&d.OSName, &d.OSVersion, &d.KernelVersion, &d.AgentVersion,
			&d.Status, &d.LastSeen, &d.CreatedAt, &d.PublicKey, &d.UserData,
			&d.SystemUUID, &d.SerialNumber, &d.Manufacturer, &d.Model,
			&d.CPU, &d.Memory, &d.DiskSize, &d.Timezone,
		)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}
		return c.JSON(d)
	})

	// Register new device (agent registration)
	app.Post("/api/devices", func(c *fiber.Ctx) error {
		var device Device
		if err := c.BodyParser(&device); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{"error": "Invalid request body"})
		}

		device.ID = uuid.New().String()
		device.CreatedAt = time.Now().Unix()
		device.LastSeen = time.Now().Unix()
		device.Status = "online"

		_, err := db.Exec(
			`INSERT INTO devices (id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version, status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model, cpu, memory, disk_size, timezone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			device.ID, device.Name, device.Hostname, device.IPAddress, device.MacAddress,
			device.OSName, device.OSVersion, device.KernelVersion, device.AgentVersion,
			device.Status, device.LastSeen, device.CreatedAt, device.PublicKey, device.UserData,
			device.SystemUUID, device.SerialNumber, device.Manufacturer, device.Model,
			device.CPU, device.Memory, device.DiskSize, device.Timezone,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(map[string]string{"error": "Failed to insert device"})
		}

		return c.JSON(device)
	})

	// Update device status
	app.Put("/api/devices/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		
		var update Device
		if err := c.BodyParser(&update); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{"error": "Invalid request body"})
		}

		device := Device{LastSeen: time.Now().Unix(), Status: "online"}
		
		fields := []string{"last_seen = ?", "status = ?"}
		args := []interface{}{device.LastSeen, device.Status}
		
		if update.Name != "" {
			fields = append(fields, "name = ?")
			args = append(args, update.Name)
		}
		if update.Hostname != "" {
			fields = append(fields, "hostname = ?")
			args = append(args, update.Hostname)
		}
		if update.IPAddress != "" {
			fields = append(fields, "ip_address = ?")
			args = append(args, update.IPAddress)
		}

		args = append(args, id)

		query := fmt.Sprintf("UPDATE devices SET %s WHERE id = ?", joinStrings(fields, ", "))
		
		result, err := db.Exec(query, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(map[string]string{"error": "Failed to update device"})
		}
		
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}

		// Fetch updated device
		var d Device
		err = db.QueryRow("SELECT id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version, status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model, cpu, memory, disk_size, timezone FROM devices WHERE id = ?", id).Scan(
			&d.ID, &d.Name, &d.Hostname, &d.IPAddress, &d.MacAddress,
			&d.OSName, &d.OSVersion, &d.KernelVersion, &d.AgentVersion,
			&d.Status, &d.LastSeen, &d.CreatedAt, &d.PublicKey, &d.UserData,
			&d.SystemUUID, &d.SerialNumber, &d.Manufacturer, &d.Model,
			&d.CPU, &d.Memory, &d.DiskSize, &d.Timezone,
		)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}
		
		return c.JSON(d)
	})

	// Delete device
	app.Delete("/api/devices/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		
		result, err := db.Exec("DELETE FROM devices WHERE id = ?", id)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(map[string]string{"error": "Failed to delete device"})
		}
		
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}
		
		return c.JSON(map[string]string{"message": "Device deleted successfully"})
	})

	// Agent heartbeat endpoint
	app.Post("/api/devices/:id/heartbeat", func(c *fiber.Ctx) error {
		id := c.Params("id")
		
		result, err := db.Exec("UPDATE devices SET last_seen = ?, status = ? WHERE id = ?", time.Now().Unix(), "online", id)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(map[string]string{"error": "Failed to update heartbeat"})
		}
		
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}

		var d Device
		err = db.QueryRow("SELECT id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version, status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model, cpu, memory, disk_size, timezone FROM devices WHERE id = ?", id).Scan(
			&d.ID, &d.Name, &d.Hostname, &d.IPAddress, &d.MacAddress,
			&d.OSName, &d.OSVersion, &d.KernelVersion, &d.AgentVersion,
			&d.Status, &d.LastSeen, &d.CreatedAt, &d.PublicKey, &d.UserData,
			&d.SystemUUID, &d.SerialNumber, &d.Manufacturer, &d.Model,
			&d.CPU, &d.Memory, &d.DiskSize, &d.Timezone,
		)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}
		
		return c.JSON(d)
	})

	// Send command to device
	app.Post("/api/devices/:id/command", func(c *fiber.Ctx) error {
		id := c.Params("id")

		var cmdReq struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		}
		if err := c.BodyParser(&cmdReq); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{"error": "Invalid request body"})
		}

		// Get device to find its hostname
		var d Device
		err := db.QueryRow("SELECT id, hostname FROM devices WHERE id = ?", id).Scan(&d.ID, &d.Hostname)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}

		// Create command record in database
		cmdID := uuid.New().String()
		cmdData, _ := json.Marshal(map[string]interface{}{
			"id":        cmdID,
			"type":      cmdReq.Type,
			"payload":   map[string]string{"command": cmdReq.Command},
			"created_at": time.Now(),
		})

		_, err = db.Exec(
			`INSERT INTO device_commands (id, device_id, type, payload, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			cmdID, id, cmdReq.Type, string(cmdData), "pending", time.Now().Unix(),
		)
		if err != nil {
			// Try creating table if doesn't exist
			db.Exec(`CREATE TABLE IF NOT EXISTS device_commands (
				id TEXT PRIMARY KEY,
				device_id TEXT,
				type TEXT,
				payload TEXT,
				status TEXT,
				output TEXT,
				created_at INTEGER,
				finished_at INTEGER
			)`)
			db.Exec(
				`INSERT INTO device_commands (id, device_id, type, payload, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				cmdID, id, cmdReq.Type, string(cmdData), "pending", time.Now().Unix(),
			)
		}

		// Try to send command directly to agent if online
		if d.Hostname != "" {
			go func() {
				resp, err := http.Post(
					fmt.Sprintf("http://localhost:47991/agent/run"),
					"application/json",
					bytes.NewBuffer(cmdData),
				)
				if err == nil && resp.StatusCode == 200 {
					defer resp.Body.Close()
					db.Exec(`UPDATE device_commands SET status = ?, finished_at = ? WHERE id = ?`, "completed", time.Now().Unix(), cmdID)
				}
			}()
		}

		return c.JSON(map[string]interface{}{
			"message":    "Command sent",
			"command_id": cmdID,
		})
	})

	// Get pending commands for device
	app.Get("/agent/:hostname/commands", func(c *fiber.Ctx) error {
		hostname := c.Params("hostname")

		// Get device ID by hostname
		var d Device
		err := db.QueryRow("SELECT id FROM devices WHERE hostname = ?", hostname).Scan(&d.ID)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}

		rows, err := db.Query(`SELECT payload FROM device_commands WHERE device_id = ? AND status = 'pending' ORDER BY created_at ASC`, d.ID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(map[string]string{"error": "Failed to query commands"})
		}
		defer rows.Close()

		var commands []map[string]interface{}
		for rows.Next() {
			var payload string
			err := rows.Scan(&payload)
			if err == nil {
				var cmd map[string]interface{}
				json.Unmarshal([]byte(payload), &cmd)
				commands = append(commands, cmd)
			}
		}

		return c.JSON(commands)
	})

	app.Listen(":3001")
}
