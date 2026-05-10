package handlers

import (
	"log/slog"
	"sync"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
)

// Defaults baked into code if a tenant has no policy row. Audit floor
// is 30 days regardless of operator choice — compliance regimes mostly
// require ≥30; we refuse anything lower.
//
// TODO(stage-14-followup): the failed_login_threshold and lockout_minutes
// fields are stored and surfaced via /admin/policies but the login
// handler still uses the existing in-memory in-process IP/email-based
// counters in handlers/auth.go. Wiring the per-tenant policy into that
// path requires looking up tenant before user (currently user lookup
// drives tenant resolution); a follow-up will rebalance the order so a
// tenant can configure its own thresholds.
const (
	defaultAuditRetentionDays         = 365
	defaultMetricsRetentionDays       = 90
	defaultTicketCommentRetentionDays = 0 // 0 = keep forever
	defaultTimeEntryRetentionDays     = 0
	defaultFailedLoginThreshold       = 10
	defaultLockoutMinutes             = 15

	auditRetentionFloor   = 30
	metricsRetentionFloor = 7
	maxRetentionDays      = 3650 // 10 years
)

type tenantPolicy struct {
	AuditRetentionDays         int `json:"audit_retention_days"`
	MetricsRetentionDays       int `json:"metrics_retention_days"`
	TicketCommentRetentionDays int `json:"ticket_comment_retention_days"`
	TimeEntryRetentionDays     int `json:"time_entry_retention_days"`
	FailedLoginThreshold       int `json:"failed_login_threshold"`
	LockoutMinutes             int `json:"lockout_minutes"`
}

func defaultPolicy() tenantPolicy {
	return tenantPolicy{
		AuditRetentionDays:         defaultAuditRetentionDays,
		MetricsRetentionDays:       defaultMetricsRetentionDays,
		TicketCommentRetentionDays: defaultTicketCommentRetentionDays,
		TimeEntryRetentionDays:     defaultTimeEntryRetentionDays,
		FailedLoginThreshold:       defaultFailedLoginThreshold,
		LockoutMinutes:             defaultLockoutMinutes,
	}
}

// loadPolicy fetches the tenant's policy or returns defaults.
func loadPolicy(tenantID string) tenantPolicy {
	p := defaultPolicy()
	_ = db.DB.QueryRow(`SELECT audit_retention_days, metrics_retention_days, ticket_comment_retention_days, time_entry_retention_days, failed_login_threshold, lockout_minutes FROM tenant_policies WHERE tenant_id = ?`, tenantID).
		Scan(&p.AuditRetentionDays, &p.MetricsRetentionDays, &p.TicketCommentRetentionDays, &p.TimeEntryRetentionDays, &p.FailedLoginThreshold, &p.LockoutMinutes)
	return p
}

func RegisterPolicyRoutes(api fiber.Router) {
	api.Get("/admin/policies", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		p := loadPolicy(callerTenantID(c))
		return c.JSON(p)
	})

	api.Put("/admin/policies", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		var req tenantPolicy
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		// Floors prevent operators from disabling audit retention to
		// hide their own actions. Compliance regimes typically require
		// ≥30 days for audit; we enforce that floor.
		if req.AuditRetentionDays < auditRetentionFloor {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "audit_retention_days must be >= 30"})
		}
		if req.AuditRetentionDays > maxRetentionDays {
			req.AuditRetentionDays = maxRetentionDays
		}
		if req.MetricsRetentionDays < metricsRetentionFloor {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "metrics_retention_days must be >= 7"})
		}
		if req.MetricsRetentionDays > maxRetentionDays {
			req.MetricsRetentionDays = maxRetentionDays
		}
		if req.TicketCommentRetentionDays < 0 {
			req.TicketCommentRetentionDays = 0
		}
		if req.TimeEntryRetentionDays < 0 {
			req.TimeEntryRetentionDays = 0
		}
		if req.FailedLoginThreshold < 3 {
			req.FailedLoginThreshold = 3
		}
		if req.FailedLoginThreshold > 100 {
			req.FailedLoginThreshold = 100
		}
		if req.LockoutMinutes < 1 {
			req.LockoutMinutes = 1
		}
		if req.LockoutMinutes > 24*60 {
			req.LockoutMinutes = 24 * 60
		}
		tenantID := callerTenantID(c)
		now := time.Now().Unix()
		var stmt string
		if db.DB.Dialect == "postgres" {
			stmt = `INSERT INTO tenant_policies (tenant_id, audit_retention_days, metrics_retention_days, ticket_comment_retention_days, time_entry_retention_days, failed_login_threshold, lockout_minutes, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id) DO UPDATE SET audit_retention_days = EXCLUDED.audit_retention_days, metrics_retention_days = EXCLUDED.metrics_retention_days, ticket_comment_retention_days = EXCLUDED.ticket_comment_retention_days, time_entry_retention_days = EXCLUDED.time_entry_retention_days, failed_login_threshold = EXCLUDED.failed_login_threshold, lockout_minutes = EXCLUDED.lockout_minutes, updated_at = EXCLUDED.updated_at`
		} else {
			stmt = `INSERT OR REPLACE INTO tenant_policies (tenant_id, audit_retention_days, metrics_retention_days, ticket_comment_retention_days, time_entry_retention_days, failed_login_threshold, lockout_minutes, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
		}
		if _, err := db.DB.Exec(stmt, tenantID, req.AuditRetentionDays, req.MetricsRetentionDays, req.TicketCommentRetentionDays, req.TimeEntryRetentionDays, req.FailedLoginThreshold, req.LockoutMinutes, now); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "save failed"})
		}
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(tenantID, userID, "policy.update", "policy", tenantID, "policies updated", c.IP())
		return c.JSON(req)
	})
}

// retentionMu serializes the prune worker. Single-process — Stage 16
// will need Redis fan-out for HA.
var retentionMu sync.Mutex

// RetentionPruneOnce trims audit_logs / metrics_history / ticket_comments
// / ticket_time_entries to the per-tenant retention. Tenants without a
// policy row use defaults; super-system rows (tenant_id="default" or
// blank) get pruned at the default tier.
func RetentionPruneOnce() {
	retentionMu.Lock()
	defer retentionMu.Unlock()
	now := time.Now().Unix()

	// Discover every distinct tenant in the relevant tables. Using
	// audit_logs as the spine — every tenant with activity has rows
	// there. We then merge in tenants from tenant_policies that may not
	// yet have audit data.
	//
	// Always include "default" so legacy rows whose tenant_id was never
	// migrated still get pruned. Older rows that ended up with empty
	// tenant_id are normalized to "default" via COALESCE in the prune
	// queries below.
	tenants := map[string]struct{}{"default": {}}
	rows, err := db.DB.Query(`SELECT DISTINCT COALESCE(tenant_id, '') FROM audit_logs`)
	if err == nil {
		for rows.Next() {
			var tid string
			if err := rows.Scan(&tid); err == nil {
				if tid == "" {
					tid = "default"
				}
				tenants[tid] = struct{}{}
			}
		}
		rows.Close()
	}
	prows, err := db.DB.Query(`SELECT tenant_id FROM tenant_policies`)
	if err == nil {
		for prows.Next() {
			var tid string
			if err := prows.Scan(&tid); err == nil && tid != "" {
				tenants[tid] = struct{}{}
			}
		}
		prows.Close()
	}

	for tid := range tenants {
		p := loadPolicy(tid)
		// Default tenant also sweeps legacy rows with NULL/empty
		// tenant_id; named tenants only sweep their own rows. Without
		// this split a row with empty tenant_id would get DELETE'd by
		// every tenant's pass.
		auditCutoff := now - int64(p.AuditRetentionDays)*86400
		// Audit retention MUST go through CompactAuditChainForTenant —
		// a bare DELETE would falsify the tamper-evident chain at the
		// first surviving row. Compaction inserts a signed bridge
		// record in the deleted range's chain slot so the chain stays
		// verifiable end-to-end.
		if n, _, err := events.CompactAuditChainForTenant(tid, auditCutoff); err != nil {
			slog.Warn("retention: audit chain compaction failed", "tenant", tid, "error", err)
		} else if n > 0 {
			slog.Info("retention compacted audit_logs", "tenant", tid, "rows", n)
		}
		// Legacy rows with NULL/empty tenant_id are reassigned to
		// "default" by the chain backfill at startup; the compaction
		// path above handles them under tid="default".
		metricCutoff := now - int64(p.MetricsRetentionDays)*86400
		if res, err := db.DB.Exec(`DELETE FROM metrics_history WHERE tenant_id = ? AND recorded_at < ?`, tid, metricCutoff); err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				slog.Info("retention pruned metrics_history", "tenant", tid, "rows", n)
			}
		}
		if p.TicketCommentRetentionDays > 0 {
			cutoff := now - int64(p.TicketCommentRetentionDays)*86400
			if res, err := db.DB.Exec(`DELETE FROM ticket_comments WHERE tenant_id = ? AND created_at < ?`, tid, cutoff); err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					slog.Info("retention pruned ticket_comments", "tenant", tid, "rows", n)
				}
			}
		}
		if p.TimeEntryRetentionDays > 0 {
			cutoff := now - int64(p.TimeEntryRetentionDays)*86400
			if res, err := db.DB.Exec(`DELETE FROM ticket_time_entries WHERE tenant_id = ? AND started_at < ?`, tid, cutoff); err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					slog.Info("retention pruned time_entries", "tenant", tid, "rows", n)
				}
			}
		}
	}

	// Also prune expired ephemeral state — oidc_states + webauthn_sessions.
	_, _ = db.DB.Exec(`DELETE FROM oidc_states WHERE expires_at < ?`, now)
	_, _ = db.DB.Exec(`DELETE FROM webauthn_sessions WHERE expires_at < ?`, now)
}
