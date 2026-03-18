package main

import (
	"fmt"
	"log"

	"github.com/gofiber/fiber/v2"
)

// Health check endpoint
func HealthCheck(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "healthy",
		"message": "Vapor RMM API is running",
	})
}

// GetDevices returns all registered devices
func GetDevicesHandler(c *fiber.Ctx) error {
	var devices []Device
	err := db.Find(&devices).Error
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch devices"})
	}
	return c.JSON(devices)
}

// GetDevice returns a specific device by ID
func GetDeviceHandler(c *fiber.Ctx) error {
	id, _ := c.ParamsInt("id")
	var device Device
	err := db.First(&device, id).Error
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Device not found"})
	}
	return c.JSON(device)
}

// RegisterDevice registers a new device from agent data
func RegisterDevice(c *fiber.Ctx) error {
	var input map[string]interface{}
	if err := c.BodyParser(&input); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	device, err := UpdateDeviceFromAgent(input)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to register device: %v", err)})
	}

	broadcastCh <- BroadcastMsg{
		Action:    "register",
		Data:      device,
		Timestamp: time.Now().Format(time.RFC3339),
	}
	log.Printf("Device registered: %s (%s)", device.Name, device.Hostname)
	return c.JSON(device)
}