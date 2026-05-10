package handlers

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// SNMPv3-only scope. v1/v2c communities are plaintext over the wire and
// not safe for any modern fleet. We require auth+priv (authPriv level)
// so an unencrypted snmpwalk can't reveal observations.
var allowedSNMPAuthProtocols = map[string]bool{
	"SHA": true, "SHA256": true, "SHA512": true,
}
var allowedSNMPPrivProtocols = map[string]bool{
	"AES": true, "AES256": true,
}

const (
	maxSNMPName       = 128
	maxSNMPHost       = 253
	maxSNMPUsername   = 64
	maxSNMPOIDs       = 32
	maxSNMPOIDLen     = 256
	minSNMPPoll       = 30      // seconds
	maxSNMPPoll       = 86_400  // seconds (1 day)
	maxSNMPSecretLen  = 256
	maxSNMPListLimit  = 500
)

// validHostOrIP accepts a hostname or literal IP. Both internal IPs
// (RFC1918) and public are allowed because SNMP targets are often on the
// LAN — the *agent* polls them, not the public-facing server, so
// ordinary SSRF rules don't apply.
func validHostOrIP(s string) bool {
	if s == "" || len(s) > maxSNMPHost {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '-':
		default:
			return false
		}
	}
	return true
}

// validOID accepts dotted-decimal OIDs only.
func validOID(s string) bool {
	if s == "" || len(s) > maxSNMPOIDLen {
		return false
	}
	if s[0] != '.' && (s[0] < '0' || s[0] > '9') {
		return false
	}
	for _, r := range s {
		if r != '.' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func RegisterSNMPRoutes(api fiber.Router) {
	api.Get("/snmp-targets", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tf, tArgs := tenantFilter(c)
		clause := ""
		args := []interface{}{}
		if tf != "" {
			clause = " WHERE tenant_id = ?"
			args = append(args, tArgs...)
		}
		rows, err := db.DB.Query(`SELECT id, name, host, port, v3_username, v3_auth_protocol, v3_priv_protocol, oids, poll_interval_seconds, enabled, COALESCE(last_polled_at,0), COALESCE(last_error,''), created_at FROM snmp_targets`+clause+` ORDER BY created_at DESC LIMIT ?`,
			append(args, maxSNMPListLimit)...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query"})
		}
		defer rows.Close()
		// Note: NEVER include v3_auth_pass_enc / v3_priv_pass_enc in the
		// response. Even encrypted, a leaked column gives an attacker who
		// also has the secrets-encryption key the cleartext password.
		type t struct {
			ID                  string `json:"id"`
			Name                string `json:"name"`
			Host                string `json:"host"`
			Port                int    `json:"port"`
			V3Username          string `json:"v3_username"`
			V3AuthProtocol      string `json:"v3_auth_protocol"`
			V3PrivProtocol      string `json:"v3_priv_protocol"`
			OIDs                string `json:"oids"`
			PollIntervalSeconds int    `json:"poll_interval_seconds"`
			Enabled             bool   `json:"enabled"`
			LastPolledAt        int64  `json:"last_polled_at,omitempty"`
			LastError           string `json:"last_error,omitempty"`
			CreatedAt           int64  `json:"created_at"`
		}
		out := []t{}
		for rows.Next() {
			var r t
			var enabled int
			if err := rows.Scan(&r.ID, &r.Name, &r.Host, &r.Port, &r.V3Username, &r.V3AuthProtocol, &r.V3PrivProtocol, &r.OIDs, &r.PollIntervalSeconds, &enabled, &r.LastPolledAt, &r.LastError, &r.CreatedAt); err == nil {
				r.Enabled = enabled == 1
				out = append(out, r)
			}
		}
		return c.JSON(fiber.Map{"targets": out})
	})

	api.Post("/snmp-targets", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			Name                string   `json:"name"`
			Host                string   `json:"host"`
			Port                int      `json:"port"`
			V3Username          string   `json:"v3_username"`
			V3AuthProtocol      string   `json:"v3_auth_protocol"`
			V3AuthPass          string   `json:"v3_auth_pass"`
			V3PrivProtocol      string   `json:"v3_priv_protocol"`
			V3PrivPass          string   `json:"v3_priv_pass"`
			OIDs                []string `json:"oids"`
			PollIntervalSeconds int      `json:"poll_interval_seconds"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		req.Name = strings.TrimSpace(req.Name)
		req.Host = strings.TrimSpace(req.Host)
		if req.Name == "" || req.Host == "" || req.V3Username == "" || req.V3AuthPass == "" || req.V3PrivPass == "" || len(req.OIDs) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, host, v3 user/pass, and oids required"})
		}
		if len(req.Name) > maxSNMPName {
			req.Name = req.Name[:maxSNMPName]
		}
		if !validHostOrIP(req.Host) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid host"})
		}
		if req.Port <= 0 || req.Port > 65535 {
			req.Port = 161
		}
		if len(req.V3Username) > maxSNMPUsername {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "v3_username too long"})
		}
		if len(req.V3AuthPass) > maxSNMPSecretLen || len(req.V3PrivPass) > maxSNMPSecretLen {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "secret too long"})
		}
		if !allowedSNMPAuthProtocols[req.V3AuthProtocol] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "v3_auth_protocol must be SHA / SHA256 / SHA512"})
		}
		if !allowedSNMPPrivProtocols[req.V3PrivProtocol] {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "v3_priv_protocol must be AES / AES256"})
		}
		if len(req.OIDs) > maxSNMPOIDs {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "too many oids"})
		}
		for _, o := range req.OIDs {
			if !validOID(o) {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid oid: " + o})
			}
		}
		if req.PollIntervalSeconds < minSNMPPoll || req.PollIntervalSeconds > maxSNMPPoll {
			req.PollIntervalSeconds = 300
		}
		authEnc, err := crypto.Encrypt(req.V3AuthPass)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "encrypt failed"})
		}
		privEnc, err := crypto.Encrypt(req.V3PrivPass)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "encrypt failed"})
		}
		id := uuid.New().String()
		tenantID := callerTenantID(c)
		if _, err := db.DB.Exec(`INSERT INTO snmp_targets (id, tenant_id, name, host, port, v3_username, v3_auth_protocol, v3_auth_pass_enc, v3_priv_protocol, v3_priv_pass_enc, oids, poll_interval_seconds, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, tenantID, req.Name, req.Host, req.Port, req.V3Username, req.V3AuthProtocol, authEnc, req.V3PrivProtocol, privEnc, strings.Join(req.OIDs, ","), req.PollIntervalSeconds, 1, time.Now().Unix()); err != nil {
			slog.Warn("snmp target insert failed", "error", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "insert failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "snmp_target.create", "snmp_target", id, fmt.Sprintf("created snmp target %s (%s)", req.Name, req.Host), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
	})

	api.Delete("/snmp-targets/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM snmp_targets WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		if _, err := db.DB.Exec(`DELETE FROM snmp_targets WHERE id = ?`, id); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "snmp_target.delete", "snmp_target", id, "deleted", c.IP())
		return c.JSON(fiber.Map{"message": "deleted"})
	})

	// Latest observations per target — for the dashboard timeline.
	api.Get("/snmp-targets/:id/observations", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM snmp_targets WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		rows, err := db.DB.Query(`SELECT oid, value, observed_at FROM snmp_observations WHERE target_id = ? ORDER BY observed_at DESC LIMIT 200`, id)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "query failed"})
		}
		defer rows.Close()
		type obs struct {
			OID        string `json:"oid"`
			Value      string `json:"value"`
			ObservedAt int64  `json:"observed_at"`
		}
		out := []obs{}
		for rows.Next() {
			var o obs
			if err := rows.Scan(&o.OID, &o.Value, &o.ObservedAt); err == nil {
				out = append(out, o)
			}
		}
		return c.JSON(fiber.Map{"observations": out})
	})
}

// TODO(stage-13-followup): wire actual SNMPv3 polling. Plan:
// - Agent fetches assigned targets via GET /agent/snmp-targets/:id
//   (token-bound), gets back target config minus encrypted secrets,
//   plus a one-shot decrypt-on-the-wire blob signed by JWT_SECRET.
// - Agent polls via gosnmp (https://github.com/gosnmp/gosnmp), POSTs
//   results to /agent/snmp-observations/:id which appends rows.
//
// For now, snmp_targets is a configuration-only surface — operators can
// declare what they'd like polled, but no actual SNMP traffic flows.
// EmitAlert paths are wired so when polling lands they'll fire on
// threshold breaches without further server changes.
