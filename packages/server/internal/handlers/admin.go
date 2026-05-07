package handlers

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
)

func RegisterAdminRoutes(api fiber.Router) {
	api.Post("/admin/db-migrate", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		if err := db.RunMigrations(db.DB.Dialect); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"message": "Migrations completed successfully"})
	})

	api.Post("/admin/db-backup", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		if db.DB.Dialect == "postgres" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Use pg_dump for PostgreSQL backups"})
		}
		backupDir := os.Getenv("BACKUP_DIR")
		if backupDir == "" {
			backupDir = "./backups"
		}
		if err := os.MkdirAll(backupDir, 0750); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create backup directory"})
		}
		timestamp := time.Now().UTC().Format("20060102_150405")
		backupPath := fmt.Sprintf("%s/vaporrmm_backup_%s.db", backupDir, timestamp)
		if err := db.BackupSQLite(backupPath); err != nil {
			slog.Error("db backup failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Backup failed"})
		}
		return c.JSON(fiber.Map{"message": "Backup created", "path": backupPath})
	})
}
