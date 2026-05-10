package handlers

import (
	"database/sql"
	"log/slog"

	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
)

func RegisterNetworkRoutes(api fiber.Router) {
	// Topology snapshot for the /network page. Returns one row per
	// device with its Tailscale state (installed, connected, ip, peers,
	// backend_state) plus core identity. Tenant-scoped via tenantFilter.
	// Capped at 1000 rows; pagination can land later if any tenant
	// crosses that.
	api.Get("/network/topology", func(c *fiber.Ctx) error {
		tf, tArgs := tenantFilter(c)
		where := ""
		args := []interface{}{}
		if tf != "" {
			where = " WHERE tenant_id = ?"
			args = append(args, tArgs...)
		}
		rows, err := db.DB.Query(`
			SELECT id, hostname, COALESCE(ip_address,''), COALESCE(status,''), COALESCE(last_seen,0),
				COALESCE(tailscale_installed,0), COALESCE(tailscale_connected,0),
				COALESCE(tailscale_ip,''), COALESCE(tailscale_hostname,''),
				COALESCE(tailscale_peers,0), COALESCE(tailscale_backend_state,'')
			  FROM devices`+where+` ORDER BY hostname ASC LIMIT 1000`, args...)
		if err != nil {
			slog.Warn("network topology query failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query topology"})
		}
		defer rows.Close()

		type node struct {
			ID                    string `json:"id"`
			Hostname              string `json:"hostname"`
			IPAddress             string `json:"ip_address"`
			Status                string `json:"status"`
			LastSeen              int64  `json:"last_seen"`
			TailscaleInstalled    bool   `json:"tailscale_installed"`
			TailscaleConnected    bool   `json:"tailscale_connected"`
			TailscaleIP           string `json:"tailscale_ip,omitempty"`
			TailscaleHostname     string `json:"tailscale_hostname,omitempty"`
			TailscalePeers        int    `json:"tailscale_peers"`
			TailscaleBackendState string `json:"tailscale_backend_state,omitempty"`
		}
		nodes := []node{}
		var connectedCount, installedCount int
		for rows.Next() {
			var n node
			var tsInstalled, tsConnected sql.NullInt64
			var tsPeers sql.NullInt64
			if err := rows.Scan(&n.ID, &n.Hostname, &n.IPAddress, &n.Status, &n.LastSeen,
				&tsInstalled, &tsConnected, &n.TailscaleIP, &n.TailscaleHostname, &tsPeers, &n.TailscaleBackendState); err != nil {
				slog.Warn("network topology scan failed", "error", err)
				continue
			}
			n.TailscaleInstalled = tsInstalled.Int64 == 1
			n.TailscaleConnected = tsConnected.Int64 == 1
			n.TailscalePeers = int(tsPeers.Int64)
			if n.TailscaleInstalled {
				installedCount++
			}
			if n.TailscaleConnected {
				connectedCount++
			}
			nodes = append(nodes, n)
		}
		if err := rows.Err(); err != nil {
			slog.Warn("network topology iteration error", "error", err)
		}
		return c.JSON(fiber.Map{
			"nodes":               nodes,
			"total":               len(nodes),
			"tailscale_installed": installedCount,
			"tailscale_connected": connectedCount,
		})
	})
}
