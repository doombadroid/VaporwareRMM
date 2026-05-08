package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"
)

// Tables we export. agent_tokens is included (hashes only — never the
// plaintext bearer) so a restored tenant can prove which agents were enrolled.
// metrics_history is intentionally excluded (it's regenerable + huge).
//
// To add a tenant-tagged table to the export, list its name and the SELECT
// columns here. We rely on tenant_id being the filter column for everything.
var exportTables = []struct {
	Table   string
	Columns string
}{
	{"users", "id, email, name, role, created_at, last_login"},
	{"devices", "id, name, hostname, ip_address, mac_address, os_name, os_version, kernel_version, agent_version, status, last_seen, created_at, public_key, user_data, system_uuid, serial_number, manufacturer, model, cpu, memory, disk_size, timezone, agent_port, agent_ip, tags, sunshine_installed, sunshine_running, sunshine_port, tailscale_installed, tailscale_connected, tailscale_ip, tailscale_hostname, tailscale_peers, tailscale_backend_state, user_id"},
	{"agent_tokens", "token_hash, device_id, hostname, created_at, expires_at"},
	{"scripts", "id, name, description, content, platform, created_at, updated_at"},
	{"alert_rules", "id, name, event_type, severity, enabled, email_recipients, webhook_url, created_at, updated_at"},
	// alert_settings: we deliberately omit the encrypted smtp_password from the export.
	{"alert_settings", "id, smtp_host, smtp_port, smtp_user, smtp_from, smtp_tls, enabled, created_at, updated_at"},
	// webhooks: we deliberately omit the encrypted secret.
	{"webhooks", "id, url, events, enabled, created_at"},
	{"branding", "id, app_name, icon_url, company_name, primary_color"},
	{"tickets", "id, title, description, status, priority, device_id, assigned_to, created_at, updated_at, due_date, category"},
	{"patches", "id, device_id, title, description, severity, status, installed_at, created_at"},
	{"file_transfers", "id, device_id, type, file_name, file_path, status, progress, created_at, completed_at"},
	{"device_commands", "id, device_id, type, payload, status, output, created_at, finished_at"},
	{"compliance_results", "id, device_id, check_type, status, details, severity, created_at"},
	{"audit_logs", "id, user_id, action, resource_type, resource_id, details, ip_address, created_at"},
}

func RegisterTenantExportRoutes(api fiber.Router) {
	// Tenant-admin (or super_admin) downloads their own tenant's data.
	api.Get("/tenants/me/export", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		tenantID, _ := c.Locals("tenant_id").(string)
		if tenantID == "" {
			tenantID = "default"
		}
		return writeTenantExport(c, tenantID)
	})

	// Super_admin downloads any tenant's data.
	api.Get("/admin/tenants/:id/export", auth.SuperAdminMiddleware(), func(c *fiber.Ctx) error {
		return writeTenantExport(c, c.Params("id"))
	})

	// Super_admin: full purge (right-to-erasure). Deletes ALL tenant-tagged data.
	// Audit logs are preserved by default for forensics; pass ?include_audit=1
	// to delete those too. Refuses to purge 'default'.
	api.Delete("/admin/tenants/:id/purge", auth.SuperAdminMiddleware(), func(c *fiber.Ctx) error {
		id := c.Params("id")
		if id == "default" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot purge default tenant"})
		}
		var status string
		if err := db.DB.QueryRow(`SELECT status FROM tenants WHERE id = ?`, id).Scan(&status); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant not found"})
		}
		includeAudit := c.Query("include_audit") == "1"

		// Order matters when foreign keys exist; we don't have FKs declared
		// but we still go child-tables-first to keep the operation reasoned.
		// User-keyed tables (sessions, totp, password_resets, invites) get
		// their own pass via subquery on users.tenant_id BEFORE we delete users.
		userKeyedTables := []string{
			"user_sessions", "user_totp", "user_totp_backup_codes", "password_resets",
		}
		var totalRows int64
		for _, t := range userKeyedTables {
			res, err := db.DB.Exec(`DELETE FROM `+t+` WHERE user_id IN (SELECT id FROM users WHERE tenant_id = ?)`, id)
			if err != nil {
				slog.Warn("purge user-keyed step failed", "table", t, "tenant_id", id, "error", err)
				continue
			}
			if n, _ := res.RowsAffected(); n > 0 {
				totalRows += n
			}
		}
		// user_invites has tenant_id directly
		if res, err := db.DB.Exec(`DELETE FROM user_invites WHERE tenant_id = ?`, id); err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				totalRows += n
			}
		}

		purgeOrder := []string{
			"compliance_results", "device_commands", "file_transfers", "patches", "tickets",
			"webhooks", "alert_rules", "alert_settings", "scripts", "branding",
			"metrics_history", "agent_tokens", "devices", "users",
		}
		if includeAudit {
			purgeOrder = append(purgeOrder, "audit_logs")
		}
		for _, t := range purgeOrder {
			res, err := db.DB.Exec(`DELETE FROM `+t+` WHERE tenant_id = ?`, id)
			if err != nil {
				slog.Warn("purge step failed", "table", t, "tenant_id", id, "error", err)
				continue
			}
			if n, _ := res.RowsAffected(); n > 0 {
				totalRows += n
			}
		}
		if _, err := db.DB.Exec(`DELETE FROM tenants WHERE id = ?`, id); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to delete tenant row", "message": err.Error()})
		}

		adminID, _ := c.Locals("user_id").(string)
		callerTenant, _ := c.Locals("tenant_id").(string)
		events.AuditLogTenant(callerTenant, adminID, "tenant.purge", "tenant", id, fmt.Sprintf("purged tenant %s (%d rows, audit=%v)", id, totalRows, includeAudit), c.IP())
		return c.JSON(fiber.Map{
			"message":         "Tenant purged",
			"tenant_id":       id,
			"rows_deleted":    totalRows,
			"audit_preserved": !includeAudit,
		})
	})
}

// writeTenantExport streams a JSON document to the client containing every
// row in every tenant-tagged table for the given tenant.
func writeTenantExport(c *fiber.Ctx, tenantID string) error {
	// Confirm tenant exists so we don't write a successful empty export.
	var name string
	if err := db.DB.QueryRow(`SELECT name FROM tenants WHERE id = ?`, tenantID).Scan(&name); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant not found"})
	}
	out := map[string]interface{}{
		"format_version":  1,
		"exported_at":     time.Now().Unix(),
		"tenant_id":       tenantID,
		"tenant_name":     name,
		"server_version":  "vaporrmm",
		"sensitive_omitted": []string{
			"alert_settings.smtp_password (encrypted)",
			"webhooks.secret (encrypted)",
			"agent_tokens.token_plaintext (never stored)",
			"metrics_history.* (regenerable)",
		},
	}

	// Tenant row itself
	var tenantRow map[string]interface{}
	if r, err := selectRowsAsMaps(db.DB.Query(`SELECT id, name, slug, plan, status, max_devices, max_users, created_at, updated_at FROM tenants WHERE id = ?`, tenantID)); err == nil && len(r) > 0 {
		tenantRow = r[0]
	}
	out["tenant"] = tenantRow

	tables := map[string]interface{}{}
	totalRows := 0
	for _, spec := range exportTables {
		query := fmt.Sprintf(`SELECT %s FROM %s WHERE tenant_id = ?`, spec.Columns, spec.Table)
		rows, err := db.DB.Query(query, tenantID)
		if err != nil {
			slog.Warn("export step failed", "table", spec.Table, "tenant_id", tenantID, "error", err)
			tables[spec.Table] = []interface{}{}
			continue
		}
		records, err := scanRowsAsMaps(rows, spec.Columns)
		rows.Close()
		if err != nil {
			slog.Warn("export scan failed", "table", spec.Table, "error", err)
		}
		tables[spec.Table] = records
		totalRows += len(records)
	}
	out["tables"] = tables
	out["total_rows"] = totalRows

	adminID, _ := c.Locals("user_id").(string)
	events.AuditLogTenant(tenantID, adminID, "tenant.export", "tenant", tenantID, fmt.Sprintf("exported %d rows", totalRows), c.IP())

	c.Set("Content-Type", "application/json")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="vaporrmm-export-%s-%s.json"`, tenantID, time.Now().Format("20060102-150405")))
	c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	enc := json.NewEncoder(c.Response().BodyWriter())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// selectRowsAsMaps runs a query that yields a small result set and returns
// each row as a map keyed by column name. Used for tiny lookup helpers.
func selectRowsAsMaps(rows *sql.Rows, err error) ([]map[string]interface{}, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]interface{}{}
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]interface{}, len(cols))
		for i, col := range cols {
			m[col] = normalizeValue(vals[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// scanRowsAsMaps splits a comma-separated column-spec into names and scans.
func scanRowsAsMaps(rows *sql.Rows, columnSpec string) ([]map[string]interface{}, error) {
	colNames := strings.Split(columnSpec, ",")
	for i := range colNames {
		colNames[i] = strings.TrimSpace(colNames[i])
	}
	out := []map[string]interface{}{}
	for rows.Next() {
		vals := make([]interface{}, len(colNames))
		ptrs := make([]interface{}, len(colNames))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return out, err
		}
		m := make(map[string]interface{}, len(colNames))
		for i, col := range colNames {
			m[col] = normalizeValue(vals[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// normalizeValue converts driver-specific values to JSON-friendly Go primitives.
func normalizeValue(v interface{}) interface{} {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return x
	}
}
