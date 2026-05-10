package handlers

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
	"vaporrmm/server/internal/httputil"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// certMonitorMu serializes the cert worker. Single-process semantics —
// multi-server fan-out is Stage 16.
var certMonitorMu sync.Mutex

const (
	certProbeTimeout       = 10 * time.Second
	maxCertMonitorURL      = 1024
	maxCertMonitorTenant   = 200
	maxCertMonitorPerCheck = 100
)

// parseCertHost cleans a user-supplied URL into (host, port). Accepts
// `https://example.com[:port]` or `host[:port]`; defaults to 443.
func parseCertHost(raw string) (host string, port string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("empty url")
	}
	// Tolerate user pasting either form.
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if u.Scheme != "https" {
		return "", "", fmt.Errorf("scheme must be https")
	}
	host = u.Hostname()
	port = u.Port()
	if host == "" {
		return "", "", fmt.Errorf("missing host")
	}
	if port == "" {
		port = "443"
	}
	return host, port, nil
}

// resolveAndPickIP resolves `host` once, validates each candidate IP
// against the SSRF policy, and returns the first allowed IP. Returns
// the IP literal so the caller can dial directly — eliminates the
// DNS-rebinding TOCTOU between an "OK at validate time" and "private
// at dial time" round trip.
//
// internalAllowed=true skips the private-range check (loopback /
// metadata blocks still apply — those are never legitimate).
func resolveAndPickIP(host string, internalAllowed bool) (net.IP, error) {
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no DNS records for %s", host)
	}
	for _, ip := range ips {
		// Loopback / unspecified / metadata always blocked.
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			return nil, fmt.Errorf("address blocked (%s): loopback / metadata", ip.String())
		}
		if !internalAllowed && (ip.IsPrivate() || ip.IsLinkLocalMulticast()) {
			return nil, fmt.Errorf("address blocked (%s): private — set internal_allowed=true to override", ip.String())
		}
		if ip4 := ip.To4(); ip4 != nil {
			if ip4[0] == 100 && ip4[1] == 100 && ip4[2] == 100 && ip4[3] == 200 {
				return nil, fmt.Errorf("address blocked (%s): cloud metadata", ip.String())
			}
			if !internalAllowed && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] < 128 {
				return nil, fmt.Errorf("address blocked (%s): CGNAT", ip.String())
			}
		}
	}
	// Pick the first IP. All candidates already passed validation —
	// returning a different one across runs would just shift which
	// public address is probed, not the allowed/denied bit.
	return ips[0], nil
}

// probeCert dials directly to the resolved IP literal (dnsName is the
// SNI / cert-verify hostname). This prevents DNS rebinding between the
// SSRF validation and the actual TLS handshake.
func probeCert(ip net.IP, port, dnsName string) (notAfter int64, status string, err error) {
	dialer := &net.Dialer{Timeout: certProbeTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(ip.String(), port), &tls.Config{
		ServerName: dnsName,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return 0, "error", err
	}
	defer conn.Close()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return 0, "error", fmt.Errorf("no peer certificate")
	}
	leaf := state.PeerCertificates[0]
	expiry := leaf.NotAfter.Unix()
	now := time.Now().Unix()
	switch {
	case now > expiry:
		return expiry, "expired", nil
	default:
		return expiry, "ok", nil
	}
}

// RegisterCertMonitorRoutes wires CRUD + manual probe trigger.
func RegisterCertMonitorRoutes(api fiber.Router) {
	api.Get("/cert-monitors", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tf, tArgs := tenantFilter(c)
		clause := ""
		args := []interface{}{}
		if tf != "" {
			clause = " WHERE tenant_id = ?"
			args = append(args, tArgs...)
		}
		rows, err := db.DB.Query(`SELECT id, url, alert_threshold_days, internal_allowed, COALESCE(last_checked_at,0), COALESCE(last_expiry_at,0), COALESCE(last_status,''), COALESCE(last_error,''), created_at FROM cert_monitors`+clause+` ORDER BY created_at DESC LIMIT ?`,
			append(args, maxCertMonitorTenant)...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to query"})
		}
		defer rows.Close()
		type m struct {
			ID                 string `json:"id"`
			URL                string `json:"url"`
			AlertThresholdDays int    `json:"alert_threshold_days"`
			InternalAllowed    bool   `json:"internal_allowed"`
			LastCheckedAt      int64  `json:"last_checked_at,omitempty"`
			LastExpiryAt       int64  `json:"last_expiry_at,omitempty"`
			LastStatus         string `json:"last_status,omitempty"`
			LastError          string `json:"last_error,omitempty"`
			CreatedAt          int64  `json:"created_at"`
		}
		out := []m{}
		for rows.Next() {
			var r m
			var ia int
			if err := rows.Scan(&r.ID, &r.URL, &r.AlertThresholdDays, &ia, &r.LastCheckedAt, &r.LastExpiryAt, &r.LastStatus, &r.LastError, &r.CreatedAt); err == nil {
				r.InternalAllowed = ia == 1
				out = append(out, r)
			}
		}
		return c.JSON(fiber.Map{"monitors": out})
	})

	api.Post("/cert-monitors", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req struct {
			URL                string `json:"url"`
			AlertThresholdDays int    `json:"alert_threshold_days"`
			InternalAllowed    bool   `json:"internal_allowed"`
		}
		if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.URL) == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "url required"})
		}
		if len(req.URL) > maxCertMonitorURL {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "url too long"})
		}
		host, _, err := parseCertHost(req.URL)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid url: " + err.Error()})
		}
		// Public targets: extra defense-in-depth via the existing
		// RejectPrivateHost so even if internal_allowed is later flipped
		// on, the original write was a public host.
		if !req.InternalAllowed {
			if err := httputil.RejectPrivateHost("https://" + host); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
			}
		}
		if req.AlertThresholdDays <= 0 || req.AlertThresholdDays > 365 {
			req.AlertThresholdDays = 14
		}
		ia := 0
		if req.InternalAllowed {
			ia = 1
		}
		id := uuid.New().String()
		tenantID := callerTenantID(c)
		if _, err := db.DB.Exec(`INSERT INTO cert_monitors (id, tenant_id, url, alert_threshold_days, internal_allowed, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, tenantID, req.URL, req.AlertThresholdDays, ia, time.Now().Unix()); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "insert failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "cert_monitor.create", "cert_monitor", id, fmt.Sprintf("monitor %s", req.URL), c.IP())
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
	})

	api.Delete("/cert-monitors/:id", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var ok int
		if err := db.DB.QueryRow(`SELECT 1 FROM cert_monitors WHERE id = ?`+tf, args...).Scan(&ok); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		if _, err := db.DB.Exec(`DELETE FROM cert_monitors WHERE id = ?`, id); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(callerTenantID(c), userID, "cert_monitor.delete", "cert_monitor", id, "deleted", c.IP())
		return c.JSON(fiber.Map{"message": "deleted"})
	})

	api.Post("/cert-monitors/:id/check", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{id}, tArgs...)
		var url string
		var ia int
		if err := db.DB.QueryRow(`SELECT url, internal_allowed FROM cert_monitors WHERE id = ?`+tf, args...).Scan(&url, &ia); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
		}
		probeOne(id, url, ia == 1, callerTenantID(c))
		return c.JSON(fiber.Map{"message": "checked"})
	})
}

// CertMonitorWorkerOnce probes every enabled monitor whose last check
// is older than ~1 hour. Cap per tick keeps a tenant with thousands of
// monitors from blocking the goroutine; remaining ones run next tick.
func CertMonitorWorkerOnce() {
	certMonitorMu.Lock()
	defer certMonitorMu.Unlock()

	cutoff := time.Now().Add(-1 * time.Hour).Unix()
	rows, err := db.DB.Query(`SELECT id, tenant_id, url, internal_allowed FROM cert_monitors WHERE COALESCE(last_checked_at,0) < ? ORDER BY last_checked_at ASC LIMIT ?`,
		cutoff, maxCertMonitorPerCheck)
	if err != nil {
		slog.Warn("cert monitor query failed", "error", err)
		return
	}
	defer rows.Close()
	type job struct {
		id, tenantID, url string
		internal          bool
	}
	var jobs []job
	for rows.Next() {
		var j job
		var ia int
		if err := rows.Scan(&j.id, &j.tenantID, &j.url, &ia); err == nil {
			j.internal = ia == 1
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	for _, j := range jobs {
		probeOne(j.id, j.url, j.internal, j.tenantID)
	}
}

// probeOne runs a single probe and updates the monitor row + emits an
// alert when the cert is within the configured threshold or expired.
func probeOne(id, rawURL string, internal bool, tenantID string) {
	host, port, err := parseCertHost(rawURL)
	if err != nil {
		updateCertResult(id, "", 0, "error", err.Error())
		return
	}
	ip, err := resolveAndPickIP(host, internal)
	if err != nil {
		updateCertResult(id, "", 0, "error", err.Error())
		return
	}
	expiry, status, err := probeCert(ip, port, host)
	if err != nil {
		updateCertResult(id, status, 0, status, err.Error())
		return
	}
	updateCertResult(id, "", expiry, status, "")

	// Alert path. Re-read threshold to reflect any concurrent admin
	// edit since the worker started this batch.
	var threshold int
	_ = db.DB.QueryRow(`SELECT alert_threshold_days FROM cert_monitors WHERE id = ?`, id).Scan(&threshold)
	daysLeft := (expiry - time.Now().Unix()) / 86400
	if status == "expired" {
		EmitAlert(tenantID, "", "cert_expired", "critical", fmt.Sprintf("cert at %s expired", rawURL))
		return
	}
	if daysLeft <= int64(threshold) {
		EmitAlert(tenantID, "", "cert_expiring_soon", severityForDays(daysLeft, threshold),
			fmt.Sprintf("cert at %s expires in %d day(s)", rawURL, daysLeft))
	}
}

func severityForDays(daysLeft int64, threshold int) string {
	if daysLeft <= 1 {
		return "critical"
	}
	if daysLeft <= int64(threshold)/2 {
		return "warning"
	}
	return "info"
}

func updateCertResult(id, _ /*reserved*/ string, expiry int64, status, errMsg string) {
	now := time.Now().Unix()
	var expiryArg interface{}
	if expiry > 0 {
		expiryArg = expiry
	}
	if _, err := db.DB.Exec(`UPDATE cert_monitors SET last_checked_at = ?, last_expiry_at = ?, last_status = ?, last_error = ? WHERE id = ?`,
		now, expiryArg, status, errMsg, id); err != nil {
		slog.Warn("cert monitor update failed", "error", err, "id", id)
	}
}

// _ keeps sql.NullInt64 import alive when the file is otherwise unused.
var _ = sql.NullInt64{}
