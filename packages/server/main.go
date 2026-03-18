package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Device struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Name      string `json:"name"`
	Hostname  string `json:"hostname"`
	IPAddress string `json:"ip_address"`
	Status    string `json:"status"`
	LastSeen  string `json:"last_seen"`
	Uptime    int64  `json:"uptime"`
	CPU       string `json:"cpu"`
	Memory    uint64 `json:"memory"`
	Disk      uint64 `json:"disk"`
	GPUs      string `json:"gpus"`
	Network   string `json:"network"`
	Drives    string `json:"drives"`
}

type AgentStatus struct {
	Action    string      `json:"action"`
	Data      interface{} `json:"data"`
	Timestamp string      `json:"timestamp"`
}

var db *gorm.DB

// WebSocket broadcast channel
type BroadcastMsg struct {
	Action    string      `json:"action"`
	Data      interface{} `json:"data"`
	Timestamp string      `json:"timestamp"`
}

var (
	wsClients   = make(map[*websocket.Conn]bool)
	wsMu        sync.RWMutex
	broadcastCh chan BroadcastMsg
)

func main() {
	var err error
	db, err = gorm.Open(sqlite.Open("devices.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Auto migrate the schema
	err = db.AutoMigrate(&Device{})
	if err != nil {
		log.Fatal("Failed to migrate database:", err)
	}

	app := fiber.New()

	// CORS middleware
	app.Use(cors.New())

	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"message": "Vapor RMM API",
			"version": "1.0.0",
		})
	})

	app.Get("/api/devices", GetDevices)
	app.Post("/api/devices", CreateDevice)
	app.Get("/api/devices/:id", GetDevice)
	app.Put("/api/devices/:id", UpdateDevice)
	app.Delete("/api/devices/:id", DeleteDevice)

	// Register device from agent
	app.Post("/api/devices/register", RegisterDeviceFromAgent)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}

	// Start WebSocket broadcast goroutine
	broadcastCh = make(chan BroadcastMsg, 10)
	go broadcastLoop()

	// HTTP endpoint for broadcasts (from agents)
	app.Post("/api/status", func(c *fiber.Ctx) error {
		status := AgentStatus{}
		if err := c.BodyParser(&status); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
		}
		broadcastCh <- BroadcastMsg{
			Action:    status.Action,
			Data:      status.Data,
			Timestamp: "now",
		}
		return c.JSON(fiber.Map{"status": "received"})
	})

	// WebSocket route for real-time updates
	app.Get("/ws", websocket.New(wsHandler))

	log.Printf("Server starting on port %s\n", port)
	log.Fatal(app.Listen(fmt.Sprintf(":%s", port)))
}

// wsHandler handles WebSocket connections from dashboard clients
func wsHandler(c *websocket.Conn) {
	defer c.Close()

	wsMu.Lock()
	wsClients[c] = true
	wsMu.Unlock()

	log.Println("New WebSocket client connected:", c.IP())

	ctx := context.Background()

	// Keep connection alive with periodic pings
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Send ping to client
			if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
				log.Printf("Failed to send ping: %v", err)
				wsMu.Lock()
				delete(wsClients, c)
				wsMu.Unlock()
				return
			}
		}
	}
}

// RegisterDeviceFromAgent registers or updates a device from agent data
func RegisterDeviceFromAgent(c *fiber.Ctx) error {
	var deviceData map[string]interface{}
	if err := c.BodyParser(&deviceData); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	device, err := UpdateDeviceFromAgent(deviceData)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to update device: %v", err)})
	}

	broadcastCh <- BroadcastMsg{
		Action:    "register",
		Data:      device,
		Timestamp: "now",
	}
	return c.JSON(device)
}

func GetDevices(c *fiber.Ctx) error {
	var devices []Device
	err := db.Find(&devices).Error
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch devices"})
	}
	return c.JSON(devices)
}

func CreateDevice(c *fiber.Ctx) error {
	device := new(Device)
	if err := c.BodyParser(device); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}
	err := db.Create(device).Error
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to create device"})
	}
	broadcastCh <- BroadcastMsg{
		Action:    "create",
		Data:      device,
		Timestamp: "now",
	}
	return c.JSON(device)
}

func GetDevice(c *fiber.Ctx) error {
	id, _ := c.ParamsInt("id")
	var device Device
	err := db.First(&device, id).Error
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Device not found"})
	}
	return c.JSON(device)
}

func UpdateDevice(c *fiber.Ctx) error {
	id, _ := c.ParamsInt("id")
	var device Device
	err := db.First(&device, id).Error
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Device not found"})
	}
	if err := c.BodyParser(&device); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}
	err = db.Save(&device).Error
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to update device"})
	}
	broadcastCh <- BroadcastMsg{
		Action:    "update",
		Data:      device,
		Timestamp: "now",
	}
	return c.JSON(device)
}

func DeleteDevice(c *fiber.Ctx) error {
	id, _ := c.ParamsInt("id")
	err := db.Delete(&Device{}, id).Error
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to delete device"})
	}
	broadcastCh <- BroadcastMsg{
		Action:    "delete",
		Data:      map[string]uint{"id": uint(id)},
		Timestamp: "now",
	}
	return c.SendString("Device deleted successfully")
}

// broadcastLoop sends updates to all WebSocket clients
func broadcastLoop() {
	for msg := range broadcastCh {
		payload, err := json.Marshal(msg)
		if err != nil {
			log.Printf("Failed to marshal payload: %v", err)
			continue
		}

		wsMu.RLock()
		clients := make(map[*websocket.Conn]bool)
		for k := range wsClients {
			clients[k] = true
		}
		wsMu.RUnlock()

		for client := range clients {
			if err := client.Write(context.Background(), websocket.MessageText, payload); err != nil {
				log.Printf("Failed to broadcast to client: %v", err)
				wsMu.Lock()
				delete(wsClients, client)
				wsMu.Unlock()
			}
		}
	}
}

// UpdateDeviceFromAgent handles device updates from the agent
func UpdateDeviceFromAgent(deviceData map[string]interface{}) (*Device, error) {
	var device Device

	// Check if device exists by hostname
	if hostname, ok := deviceData["hostname"].(string); ok {
		result := db.Where("hostname = ?", hostname).First(&device)
		if result.RowsAffected == 0 {
			db.Create(&device)
		}
	}

	for k, v := range deviceData {
		switch k {
		case "name":
			device.Name = fmt.Sprintf("%v", v)
		case "hostname":
			device.Hostname = fmt.Sprintf("%v", v)
		case "ip_address":
			device.IPAddress = fmt.Sprintf("%v", v)
		case "status":
			device.Status = fmt.Sprintf("%v", v)
		case "last_seen":
			device.LastSeen = fmt.Sprintf("%v", v)
		case "uptime":
			if val, ok := v.(float64); ok {
				device.Uptime = int64(val)
			}
		case "cpu":
			device.CPU = fmt.Sprintf("%v", v)
		case "memory":
			if val, ok := v.(float64); ok {
				device.Memory = uint64(val)
			}
		case "disk":
			if val, ok := v.(float64); ok {
				device.Disk = uint64(val)
			}
		case "gpus":
			device.GPUs = fmt.Sprintf("%v", v)
		case "network":
			device.Network = fmt.Sprintf("%v", v)
		case "drives":
			device.Drives = fmt.Sprintf("%v", v)
		}
	}

	if device.ID == 0 {
		err := db.Create(&device).Error
		return &device, err
	}
	err := db.Save(&device).Error
	return &device, err
}