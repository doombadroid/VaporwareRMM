package db

// Helpers exported here are intended for use from _test.go files in
// other packages. They are NOT for use by production code. Centralised
// in the db package so the Postgres CI lane can call them from
// per-test setup without each package re-implementing the same
// teardown logic.

import (
	"fmt"
	"strings"
)

// testTables enumerates every table the test suite touches that could
// carry state across tests. Hand-curated for the same reason
// fkTablesReferencingDeviceID is — Postgres information_schema lookup
// would work but explicit listing forces a code-review touchpoint when
// a new table is added.
var testTables = []string{
	// User-facing tenant data
	"audit_logs",
	"devices",
	"agent_tokens",
	"tickets",
	"ticket_comments",
	"ticket_time_entries",
	"alerts",
	"patches",
	"device_commands",
	"file_transfers",
	"compliance_results",
	"device_software",
	"device_hardware",
	"device_group_members",
	"device_groups",
	"metrics_history",
	"customer_users",
	"maintenance_windows",
	"neighbor_observations",
	"snmp_targets",
	"snmp_observations",
	"cert_monitors",
	"alert_rules",
	"webhooks",
	"report_schedules",
	"scripts",
	"ai_providers",
	"ai_routing_rules",
	"ai_runs",
	"ai_run_prompts",
	"ai_capabilities",
	"ai_capability_dependencies",
	"ai_capability_metrics_daily",
	"ai_capability_tenant_config",
	"ai_kill_switches",
	"ai_rollback_probes",
	// Auth surfaces
	"user_sessions",
	"user_invites",
	"user_totp",
	"user_totp_backup_codes",
	"webauthn_credentials",
	"webauthn_sessions",
	"oidc_states",
	"password_resets",
	"users",
	"branding",
	"alert_settings",
	"tenant_policies",
	"tenant_oidc_configs",
	// Tenants is intentionally last because most other tables
	// reference its id; defaulting to "default" should remain.
	"tenants",
}

// ResetForTests truncates every table the test suite touches. Engine-
// agnostic: on SQLite it issues DELETE statements (TRUNCATE is not a
// statement there); on Postgres it issues TRUNCATE ... RESTART
// IDENTITY CASCADE which is dramatically faster than DELETE on large
// tables and resets sequence numbers.
//
// Idempotent. Safe to call before any test that wants a clean slate.
// SQLite tests that use t.TempDir() per test don't need this but
// calling it is cheap. Postgres-lane tests rely on it.
//
// After truncation we recreate the row that's expected to exist:
// tenants("default") so foreign-key checks on tenant_id pass without
// every test having to seed it.
func ResetForTests() error {
	if DB == nil {
		return fmt.Errorf("ResetForTests: db not initialised")
	}
	for _, t := range testTables {
		var stmt string
		if DB.Dialect == "postgres" {
			stmt = fmt.Sprintf(`TRUNCATE TABLE %s RESTART IDENTITY CASCADE`, t)
		} else {
			stmt = fmt.Sprintf(`DELETE FROM %s`, t)
		}
		if _, err := DB.DB.Exec(stmt); err != nil {
			// "no such table" / "does not exist" is fine — some
			// optional tables aren't present in every deployment.
			low := strings.ToLower(err.Error())
			if strings.Contains(low, "no such table") || strings.Contains(low, "does not exist") {
				continue
			}
			return fmt.Errorf("ResetForTests: %s: %w", t, err)
		}
	}
	// Drop runtime-created indexes that aren't part of the schema-
	// migration set. The dedup pass installs idx_devices_dedup if
	// it isn't already there; persisting it across Postgres test runs
	// would prevent the dedup-test seed from inserting duplicates.
	// The dedup pass recreates the index idempotently.
	for _, idx := range []string{
		"idx_devices_dedup",
	} {
		_, _ = DB.DB.Exec(fmt.Sprintf(`DROP INDEX IF EXISTS %s`, idx))
	}
	// Re-seed the default tenant so FKs that expect it stay valid.
	EnsureDefaultTenant()
	return nil
}
