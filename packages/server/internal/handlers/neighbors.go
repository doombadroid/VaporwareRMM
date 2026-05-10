package handlers

import (
	"log/slog"
	"net"
	"strings"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const (
	maxNeighborsPerAgent = 1024
	maxNeighborsHostname = 253
)

type agentNeighborEntry struct {
	IP       string `json:"ip"`
	MAC      string `json:"mac,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Iface    string `json:"iface,omitempty"`
}

// validIPv4OrV6 narrows what an agent can store. Reject literal-loopback
// + zero IPs to keep the table useful.
func validNeighborIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	if ip.IsUnspecified() || ip.IsLoopback() {
		return false
	}
	return true
}

// validMAC accepts colon- or dash-delimited MAC addresses in any case.
// Returns false for "(incomplete)" entries the kernel sometimes shows.
func validMAC(s string) bool {
	if s == "" {
		return true
	}
	_, err := net.ParseMAC(s)
	return err == nil
}

func RegisterNeighborRoutes(app *fiber.App, api fiber.Router) {
	// Agent post — token-bound device check.
	app.Post("/agent/neighbors/:id", auth.AgentAuthMiddleware(), func(c *fiber.Ctx) error {
		urlDeviceID := c.Params("id")
		boundDeviceID, _ := c.Locals("device_id").(string)
		if urlDeviceID == "" || boundDeviceID == "" || urlDeviceID != boundDeviceID {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "device id mismatch"})
		}
		var req struct {
			Neighbors []agentNeighborEntry `json:"neighbors"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		if len(req.Neighbors) > maxNeighborsPerAgent {
			req.Neighbors = req.Neighbors[:maxNeighborsPerAgent]
		}
		var tenantID string
		if err := db.DB.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM devices WHERE id = ?`, boundDeviceID).Scan(&tenantID); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}

		tx, err := db.DB.Begin()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "tx begin failed"})
		}
		defer tx.Rollback()
		if _, err := tx.Exec(db.DB.Q(`DELETE FROM neighbor_observations WHERE device_id = ?`), boundDeviceID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "clear failed"})
		}
		ins := db.DB.Q(`INSERT INTO neighbor_observations (id, device_id, tenant_id, ip, mac, hostname, iface, observed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
		now := time.Now().Unix()
		inserted := 0
		for _, n := range req.Neighbors {
			if !validNeighborIP(n.IP) {
				continue
			}
			if !validMAC(n.MAC) {
				n.MAC = ""
			}
			if len(n.Hostname) > maxNeighborsHostname {
				n.Hostname = n.Hostname[:maxNeighborsHostname]
			}
			if len(n.Iface) > 64 {
				n.Iface = n.Iface[:64]
			}
			if _, err := tx.Exec(ins, uuid.New().String(), boundDeviceID, tenantID, n.IP, strings.ToLower(n.MAC), n.Hostname, n.Iface, now); err != nil {
				slog.Warn("neighbor insert failed", "error", err)
				continue
			}
			inserted++
		}
		if err := tx.Commit(); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "commit failed"})
		}
		return c.JSON(fiber.Map{"received": len(req.Neighbors), "inserted": inserted})
	})

	// User-side aggregation: list "unmanaged neighbors" — IPs the fleet
	// observed that don't belong to any registered device. Tenant-scoped.
	api.Get("/network/neighbors", func(c *fiber.Ctx) error {
		tf, tArgs := tenantFilter(c)
		// Query: every observed IP minus IPs that match a known device.
		// Rough heuristic — we trust devices.ip_address as the "known"
		// list. JOIN tenant-pinned to keep super_admin output sensible.
		whereTenant := ""
		args := []interface{}{}
		if tf != "" {
			whereTenant = " AND n.tenant_id = ?"
			args = append(args, tArgs...)
		}
		rows, err := db.DB.Query(`
			SELECT n.ip, COALESCE(MAX(n.mac), ''), COALESCE(MAX(n.hostname),''), COUNT(DISTINCT n.device_id) AS observers, MAX(n.observed_at)
			  FROM neighbor_observations n
			  LEFT JOIN devices d ON d.ip_address = n.ip AND d.tenant_id = n.tenant_id
			  WHERE d.id IS NULL`+whereTenant+`
			  GROUP BY n.ip
			  ORDER BY observers DESC, n.ip ASC
			  LIMIT 500`, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query neighbors"})
		}
		defer rows.Close()
		type row struct {
			IP         string `json:"ip"`
			MAC        string `json:"mac,omitempty"`
			Hostname   string `json:"hostname,omitempty"`
			Observers  int    `json:"observers"`
			LastSeenAt int64  `json:"last_seen_at"`
		}
		out := []row{}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.IP, &r.MAC, &r.Hostname, &r.Observers, &r.LastSeenAt); err == nil {
				out = append(out, r)
			}
		}
		return c.JSON(fiber.Map{"neighbors": out})
	})
}
