package main

import (
	"log"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Device struct {
	ID        uint   `gorm:"primaryKey"`
	MacAddress string  `gorm:"uniqueIndex"`
	Name       string
	Status     string `gorm:"default:'online'"`
	IPAddress  string
	LastSeen   string
}

type User struct {
	ID     uint   `gorm:"primaryKey"`
	Email  string `gorm:"uniqueIndex"`
	Name   string
	HashedPassword string
}

func main() {
	app := fiber.New()

	// Middleware
	app.Use(cors.New())

	// Database connection
	dbPath := os.Getenv("DATABASE_URL")
	if dbPath == "" {
		dbPath = "devices.db"
	}
	db, err := gorm.Open(sqlite.Open(dbPath))
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	db.AutoMigrate(&Device{}, &User{})

	// Routes
	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"message": " vaporRMM Server API",
			"status":  "running",
		})
	})

	app.Get("/api/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "healthy"})
	})

	log.Println("Starting server on :3001")
	app.Listen(":3001")
}