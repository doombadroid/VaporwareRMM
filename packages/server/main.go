package main

import (
	"fmt"
	"os"

	"github.com/gofiber/fiber/v2"
)

// Device represents a managed device
type Device struct {
	ID              string `json:"id" gorm:"primaryKey"`
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
	bootTime        int64  `json:"-"`
}

// StatusResponse for health checks
type StatusResponse struct {
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
	Version   string `json:"version"`
}

func main() {
	app := fiber.New()

	// In-memory store (use database in production)
	devices := make(map[string]Device)

	// Health check endpoint
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(StatusResponse{
			Status:  "ok",
			Version: "1.0.0",
		})
	})

	// Get all devices
	app.Get("/api/devices", func(c *fiber.Ctx) error {
		deviceList := make([]Device, 0, len(devices))
		for _, d := range devices {
			deviceList = append(deviceList, d)
		}
		return c.JSON(deviceList)
	})

	// Get device by ID
	app.Get("/api/devices/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		if device, ok := devices[id]; ok {
			return c.JSON(device)
		}
		return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
	})

	// Register new device (agent registration)
	app.Post("/api/devices", func(c *fiber.Ctx) error {
		var device Device
		if err := c.BodyParser(&device); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{"error": "Invalid request body"})
		}

		device.ID = generateID()
		device.CreatedAt = 0 // Will be set by database in production
		device.LastSeen = 0
		device.Status = "online"

		devices[device.ID] = device
		return c.JSON(device)
	})

	// Update device status
	app.Put("/api/devices/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		if _, ok := devices[id]; !ok {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}

		var update Device
		if err := c.BodyParser(&update); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{"error": "Invalid request body"})
		}

		device := devices[id]
		device.LastSeen = 0 // Will be set by database in production
		device.Status = "online"

		if update.Name != "" {
			device.Name = update.Name
		}
		if update.Hostname != "" {
			device.Hostname = update.Hostname
		}
		if update.IPAddress != "" {
			device.IPAddress = update.IPAddress
		}

		devices[id] = device
		return c.JSON(device)
	})

	// Delete device
	app.Delete("/api/devices/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		if _, ok := devices[id]; !ok {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}
		delete(devices, id)
		return c.JSON(map[string]string{"message": "Device deleted successfully"})
	})

	// Agent heartbeat endpoint
	app.Post("/api/devices/:id/heartbeat", func(c *fiber.Ctx) error {
		id := c.Params("id")
		device, ok := devices[id]
		if !ok {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}

		device.LastSeen = 0 // Will be set by database in production
		device.Status = "online"
		devices[id] = device

		return c.JSON(device)
	})

	// Execute command on device
	app.Post("/api/devices/:id/execute", func(c *fiber.Ctx) error {
		id := c.Params("id")
		device, ok := devices[id]
		if !ok {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "Device not found"})
		}

		var cmd struct {
			Command string `json:"command"`
		}
		if err := c.BodyParser(&cmd); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{"error": "Invalid request body"})
		}

		// In production, this would send command via WebSocket/agent
		fmt.Printf("Command to execute on %s: %s\n", device.Hostname, cmd.Command)

		return c.JSON(map[string]interface{}{
			"device_id":  id,
			"command":    cmd.Command,
			"status":     "queued",
			"message":    "Command queued for execution",
		})
	})

	// Webhook endpoint
	app.Post("/api/webhooks/:type", func(c *fiber.Ctx) error {
		webhookType := c.Params("type")
		var payload map[string]interface{}
		if err := c.BodyParser(&payload); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{"error": "Invalid request body"})
		}

		fmt.Printf("Received webhook (%s): %v\n", webhookType, payload)

		return c.JSON(map[string]string{"status": "received"})
	})

	fmt.Println("Vapor RMM Server starting on :3001")
	if err := app.Listen(":3001"); err != nil {
		panic(err)
	}
}

func generateID() string {
	// Simple ID generation - in production use UUID or snowflake
	return fmt.Sprintf("dev_%d", os.Getpid())
}