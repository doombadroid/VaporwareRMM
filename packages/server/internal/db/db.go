package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/mattn/go-sqlite3"
)

type Wrapper struct {
	*sql.DB
	Dialect string // "sqlite" or "postgres"
}

// migration represents a single database migration.
type migration struct {
	Version string
	Name    string
	SQL     string
	// PostgresOnly skips this migration on SQLite. Used for things that
	// only make sense on Postgres — pgvector, vector indexes, etc.
	PostgresOnly bool
	// PostgresSQL overrides SQL when running on Postgres. Use when the
	// dialects need different DDL (e.g. BLOB vs BYTEA, INTEGER PK vs
	// SERIAL). Leave empty to use SQL for both.
	PostgresSQL string
}

// q rewrites ? placeholders to $1,$2,... for PostgreSQL; no-op for SQLite.
// It skips ? characters inside single-quoted string literals to avoid corrupting data.
func (d *Wrapper) q(query string) string {
	if d.Dialect != "postgres" {
		return query
	}
	var buf strings.Builder
	n := 0
	inString := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if ch == '\'' {
			inString = !inString
		}
		if !inString && ch == '?' {
			n++
			fmt.Fprintf(&buf, "$%d", n)
		} else {
			buf.WriteByte(ch)
		}
	}
	return buf.String()
}

// Exec rewrites placeholders then delegates to the underlying sql.DB.
func (d *Wrapper) Exec(query string, args ...interface{}) (sql.Result, error) {
	return d.DB.Exec(d.q(query), args...)
}

// Query rewrites placeholders then delegates to the underlying sql.DB.
func (d *Wrapper) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return d.DB.Query(d.q(query), args...)
}

// QueryRow rewrites placeholders then delegates to the underlying sql.DB.
func (d *Wrapper) QueryRow(query string, args ...interface{}) *sql.Row {
	return d.DB.QueryRow(d.q(query), args...)
}

// Q exposes the placeholder rewrite for callers that hold a raw *sql.Tx
// (where the wrapper's auto-rewrite doesn't apply). Use as
// `tx.QueryContext(ctx, db.DB.Q("... ?"), arg)` so the same SQL works on
// SQLite and Postgres.
func (d *Wrapper) Q(query string) string { return d.q(query) }
func RunMigrations(dialect string) error {
	// Create schema_migrations table
	createMigrationsTable := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		name TEXT,
		applied_at INTEGER
	);`
	if _, err := DB.Exec(createMigrationsTable); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	migrations := []migration{
		{
			Version: "001",
			Name:    "add_sunshine_columns",
			SQL: `ALTER TABLE devices ADD COLUMN sunshine_installed INTEGER DEFAULT 0;
			ALTER TABLE devices ADD COLUMN sunshine_running INTEGER DEFAULT 0;
			ALTER TABLE devices ADD COLUMN sunshine_port INTEGER DEFAULT 0;`,
		},
		{
			Version: "002",
			Name:    "add_tailscale_columns",
			SQL: `ALTER TABLE devices ADD COLUMN tailscale_installed INTEGER DEFAULT 0;
			ALTER TABLE devices ADD COLUMN tailscale_connected INTEGER DEFAULT 0;
			ALTER TABLE devices ADD COLUMN tailscale_ip TEXT DEFAULT '';
			ALTER TABLE devices ADD COLUMN tailscale_hostname TEXT DEFAULT '';
			ALTER TABLE devices ADD COLUMN tailscale_peers INTEGER DEFAULT 0;
			ALTER TABLE devices ADD COLUMN tailscale_backend_state TEXT DEFAULT '';`,
		},
		{
			Version: "003",
			Name:    "add_tags_column",
			SQL:     `ALTER TABLE devices ADD COLUMN tags TEXT DEFAULT '';`,
		},
		{
			Version: "004",
			Name:    "add_user_sessions_table",
			SQL: `CREATE TABLE IF NOT EXISTS user_sessions (
				id TEXT PRIMARY KEY,
				user_id TEXT NOT NULL,
				token_hash TEXT NOT NULL,
				ip_address TEXT,
				user_agent TEXT,
				created_at INTEGER NOT NULL,
				last_seen INTEGER NOT NULL
			);`,
		},
		{
			Version: "005",
			Name:    "add_password_resets_table",
			SQL: `CREATE TABLE IF NOT EXISTS password_resets (
				id TEXT PRIMARY KEY,
				user_id TEXT NOT NULL,
				token_hash TEXT NOT NULL,
				expires_at INTEGER NOT NULL,
				used INTEGER DEFAULT 0,
				created_at INTEGER NOT NULL
			);`,
		},
		{
			Version: "006",
			Name:    "add_audit_logs_table",
			SQL: `CREATE TABLE IF NOT EXISTS audit_logs (
				id TEXT PRIMARY KEY,
				user_id TEXT,
				action TEXT NOT NULL,
				resource_type TEXT,
				resource_id TEXT,
				details TEXT,
				ip_address TEXT,
				created_at INTEGER NOT NULL
			);`,
		},
		{
			Version: "007",
			Name:    "add_webhooks_table",
			SQL: `CREATE TABLE IF NOT EXISTS webhooks (
				id TEXT PRIMARY KEY,
				url TEXT NOT NULL,
				secret TEXT,
				events TEXT NOT NULL,
				enabled INTEGER DEFAULT 1,
				created_at INTEGER NOT NULL
			);`,
		},
		{
			Version: "008",
			Name:    "add_patches_table",
			SQL: `CREATE TABLE IF NOT EXISTS patches (
				id TEXT PRIMARY KEY,
				device_id TEXT NOT NULL,
				title TEXT NOT NULL,
				description TEXT,
				severity TEXT DEFAULT 'medium',
				status TEXT DEFAULT 'pending',
				installed_at INTEGER,
				created_at INTEGER NOT NULL
			);`,
		},
		{
			Version: "009",
			Name:    "add_file_transfers_table",
			SQL: `CREATE TABLE IF NOT EXISTS file_transfers (
				id TEXT PRIMARY KEY,
				device_id TEXT NOT NULL,
				type TEXT NOT NULL,
				file_name TEXT NOT NULL,
				file_path TEXT NOT NULL,
				status TEXT DEFAULT 'pending',
				progress INTEGER DEFAULT 0,
				created_at INTEGER NOT NULL,
				completed_at INTEGER
			);
			CREATE INDEX IF NOT EXISTS idx_file_transfers_device_status ON file_transfers(device_id, status);`,
		},
		{
			Version: "010",
			Name:    "add_alert_settings_table",
			SQL: `CREATE TABLE IF NOT EXISTS alert_settings (
				id TEXT PRIMARY KEY DEFAULT 'default',
				smtp_host TEXT,
				smtp_port INTEGER DEFAULT 587,
				smtp_user TEXT,
				smtp_password TEXT,
				smtp_from TEXT,
				smtp_tls INTEGER DEFAULT 1,
				enabled INTEGER DEFAULT 0,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);
			CREATE TABLE IF NOT EXISTS alert_rules (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				event_type TEXT NOT NULL,
				severity TEXT DEFAULT 'medium',
				enabled INTEGER DEFAULT 1,
				email_recipients TEXT,
				webhook_url TEXT,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);`,
		},
		{
			Version: "011",
			Name:    "add_scripts_table",
			SQL: `CREATE TABLE IF NOT EXISTS scripts (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				description TEXT,
				content TEXT NOT NULL,
				platform TEXT DEFAULT 'all',
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);`,
		},
		{
			Version: "012",
			Name:    "add_compliance_results_table",
			SQL: `CREATE TABLE IF NOT EXISTS compliance_results (
				id TEXT PRIMARY KEY,
				device_id TEXT NOT NULL,
				check_type TEXT NOT NULL,
				status TEXT DEFAULT 'fail',
				details TEXT,
				severity TEXT DEFAULT 'medium',
				created_at INTEGER NOT NULL
			);`,
		},
		{
			Version: "013",
			Name:    "add_agent_token_expires_at",
			SQL:     `ALTER TABLE agent_tokens ADD COLUMN expires_at INTEGER DEFAULT 0;`,
		},
		{
			Version: "014",
			Name:    "add_tickets_table",
			SQL: `CREATE TABLE IF NOT EXISTS tickets (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				description TEXT,
				status TEXT DEFAULT 'open',
				priority TEXT DEFAULT 'medium',
				device_id TEXT,
				assigned_to TEXT,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL,
				due_date INTEGER,
				category TEXT DEFAULT 'general'
			);
			CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets(status);
			CREATE INDEX IF NOT EXISTS idx_tickets_priority ON tickets(priority);`,
		},
		{
			Version: "015",
			Name:    "add_user_totp_table",
			SQL: `CREATE TABLE IF NOT EXISTS user_totp (
				user_id TEXT PRIMARY KEY,
				secret TEXT NOT NULL,
				enabled INTEGER DEFAULT 0,
				created_at INTEGER NOT NULL,
				enabled_at INTEGER
			);`,
		},
		{
			Version: "016",
			Name:    "add_user_totp_backup_codes_table",
			SQL: `CREATE TABLE IF NOT EXISTS user_totp_backup_codes (
				id TEXT PRIMARY KEY,
				user_id TEXT NOT NULL,
				code_hash TEXT NOT NULL,
				used INTEGER DEFAULT 0,
				created_at INTEGER NOT NULL,
				used_at INTEGER
			);
			CREATE INDEX IF NOT EXISTS idx_totp_backup_user ON user_totp_backup_codes(user_id);`,
		},
		{
			Version: "017",
			Name:    "add_tenants_and_tenant_id_columns",
			SQL: `CREATE TABLE IF NOT EXISTS tenants (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				slug TEXT,
				plan TEXT DEFAULT 'free',
				status TEXT DEFAULT 'active',
				registration_secret TEXT,
				max_devices INTEGER DEFAULT 0,
				max_users INTEGER DEFAULT 0,
				created_at INTEGER NOT NULL,
				updated_at INTEGER
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_slug ON tenants(slug);
			CREATE INDEX IF NOT EXISTS idx_tenants_reg_secret ON tenants(registration_secret);
			ALTER TABLE users ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE devices ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE agent_tokens ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE scripts ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE alert_rules ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE alert_settings ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE webhooks ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE audit_logs ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE patches ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE tickets ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE branding ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id);
			CREATE INDEX IF NOT EXISTS idx_devices_tenant ON devices(tenant_id);
			CREATE INDEX IF NOT EXISTS idx_agent_tokens_tenant ON agent_tokens(tenant_id);
			CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant ON audit_logs(tenant_id);
			UPDATE users SET role = 'super_admin' WHERE role = 'admin';`,
		},
		{
			Version: "018",
			Name:    "add_tenant_id_to_child_tables",
			SQL: `ALTER TABLE device_commands ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE file_transfers ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE compliance_results ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			ALTER TABLE metrics_history ADD COLUMN tenant_id TEXT NOT NULL DEFAULT 'default';
			CREATE INDEX IF NOT EXISTS idx_device_commands_tenant ON device_commands(tenant_id);
			CREATE INDEX IF NOT EXISTS idx_file_transfers_tenant ON file_transfers(tenant_id);
			CREATE INDEX IF NOT EXISTS idx_compliance_results_tenant ON compliance_results(tenant_id);
			CREATE INDEX IF NOT EXISTS idx_metrics_history_tenant ON metrics_history(tenant_id);`,
		},
		{
			Version: "019",
			Name:    "add_user_invites_table",
			SQL: `CREATE TABLE IF NOT EXISTS user_invites (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				email TEXT NOT NULL,
				role TEXT NOT NULL DEFAULT 'user',
				token_hash TEXT NOT NULL,
				invited_by TEXT NOT NULL,
				expires_at INTEGER NOT NULL,
				accepted_at INTEGER,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_user_invites_tenant ON user_invites(tenant_id);
			CREATE INDEX IF NOT EXISTS idx_user_invites_token ON user_invites(token_hash);`,
		},
		{
			Version: "020",
			Name:    "add_tenant_suspension_grace",
			SQL:     `ALTER TABLE tenants ADD COLUMN suspended_at INTEGER;`,
		},
		{
			Version: "021",
			Name:    "add_uniqueness_constraints",
			SQL: `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_tenant ON users(email, tenant_id);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_user_invites_token_hash ON user_invites(token_hash);`,
		},
		{
			Version: "022",
			Name:    "add_tenants_ai_fields",
			SQL: `ALTER TABLE tenants ADD COLUMN ai_enabled INTEGER DEFAULT 0;
			ALTER TABLE tenants ADD COLUMN ai_billing_mode TEXT DEFAULT 'absorb';
			ALTER TABLE tenants ADD COLUMN ai_max_chat_cost_per_day_micros BIGINT DEFAULT 0;
			ALTER TABLE tenants ADD COLUMN ai_max_embedding_cost_per_day_micros BIGINT DEFAULT 0;
			ALTER TABLE tenants ADD COLUMN ai_dpa_acknowledged_at INTEGER;`,
		},
		{
			Version: "023",
			Name:    "add_ai_providers",
			SQL: `CREATE TABLE IF NOT EXISTS ai_providers (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				kind TEXT NOT NULL,
				name TEXT NOT NULL,
				base_url TEXT,
				api_key_encrypted TEXT,
				region TEXT,
				model_trust_level TEXT DEFAULT 'external',
				enabled INTEGER DEFAULT 0,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_ai_providers_tenant ON ai_providers(tenant_id);`,
		},
		{
			Version: "024",
			Name:    "add_ai_routing_rules",
			SQL: `CREATE TABLE IF NOT EXISTS ai_routing_rules (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				task_type TEXT NOT NULL,
				preferred_provider_id TEXT NOT NULL,
				fallback_provider_id TEXT,
				model_name TEXT NOT NULL,
				embedding_model_name TEXT,
				max_cost_per_call_micros BIGINT DEFAULT 1000000,
				max_input_tokens INTEGER DEFAULT 8000,
				max_output_tokens INTEGER DEFAULT 1000,
				cost_per_1k_input_micros BIGINT DEFAULT 0,
				cost_per_1k_output_micros BIGINT DEFAULT 0,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_routing_rules_task ON ai_routing_rules(tenant_id, task_type);`,
		},
		{
			Version: "025",
			Name:    "add_ai_runs",
			SQL: `CREATE TABLE IF NOT EXISTS ai_runs (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				customer_id TEXT,
				device_id TEXT,
				ticket_id TEXT,
				capability_id TEXT,
				run_type TEXT NOT NULL,
				call_chain_id TEXT,
				parent_run_id TEXT,
				provider_id TEXT NOT NULL,
				model_name TEXT NOT NULL,
				model_version TEXT,
				model_trust_level TEXT,
				prompt_hash TEXT,
				prompt_token_count INTEGER DEFAULT 0,
				output_token_count INTEGER DEFAULT 0,
				cost_usd_micros BIGINT DEFAULT 0,
				latency_ms INTEGER DEFAULT 0,
				retrieved_context_refs TEXT,
				output_text TEXT,
				action_taken TEXT,
				scope_snapshot_hash TEXT,
				rung_at_call TEXT NOT NULL,
				tenant_status_at_call TEXT,
				approved_by_user_id TEXT,
				outcome TEXT,
				outcome_set_by TEXT,
				outcome_set_at INTEGER,
				rollback_attempted INTEGER DEFAULT 0,
				rollback_succeeded INTEGER DEFAULT 0,
				signed_hash TEXT,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_ai_runs_tenant_created ON ai_runs(tenant_id, created_at);
			CREATE INDEX IF NOT EXISTS idx_ai_runs_capability_created ON ai_runs(capability_id, created_at);
			CREATE INDEX IF NOT EXISTS idx_ai_runs_chain ON ai_runs(call_chain_id);`,
		},
		{
			Version: "026",
			Name:    "add_ai_run_prompts",
			SQL: `CREATE TABLE IF NOT EXISTS ai_run_prompts (
				run_id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				prompt_text TEXT,
				archived_at INTEGER,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_ai_run_prompts_tenant ON ai_run_prompts(tenant_id, created_at);`,
		},
		{
			Version: "027",
			Name:    "add_ai_capabilities",
			SQL: `CREATE TABLE IF NOT EXISTS ai_capabilities (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL UNIQUE,
				category TEXT NOT NULL,
				description TEXT,
				stage INTEGER NOT NULL,
				required_provider_caps TEXT,
				created_at INTEGER NOT NULL
			);
			CREATE TABLE IF NOT EXISTS ai_capability_tenant_config (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				capability_id TEXT NOT NULL,
				enabled INTEGER DEFAULT 0,
				rung TEXT DEFAULT 'shadow',
				scope_filter TEXT,
				confidence_threshold INTEGER DEFAULT 0,
				blast_radius_max_devices INTEGER DEFAULT 0,
				blast_radius_window_minutes INTEGER DEFAULT 5,
				promotion_criteria TEXT,
				kill_switch INTEGER DEFAULT 0,
				last_promoted_at INTEGER,
				last_demoted_at INTEGER,
				updated_at INTEGER NOT NULL
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_cap_tenant ON ai_capability_tenant_config(tenant_id, capability_id);
			CREATE TABLE IF NOT EXISTS ai_capability_metrics_daily (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				capability_id TEXT NOT NULL,
				day TEXT NOT NULL,
				calls INTEGER DEFAULT 0,
				suggestions_offered INTEGER DEFAULT 0,
				suggestions_taken INTEGER DEFAULT 0,
				suggestions_overridden INTEGER DEFAULT 0,
				actions_executed INTEGER DEFAULT 0,
				actions_rolled_back INTEGER DEFAULT 0,
				labeled_correct INTEGER DEFAULT 0,
				labeled_incorrect INTEGER DEFAULT 0,
				labeled_unclear INTEGER DEFAULT 0,
				customer_complaints INTEGER DEFAULT 0,
				cost_usd_micros BIGINT DEFAULT 0,
				created_at INTEGER NOT NULL
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_cap_metrics ON ai_capability_metrics_daily(tenant_id, capability_id, day);
			CREATE TABLE IF NOT EXISTS ai_capability_dependencies (
				capability_id TEXT NOT NULL,
				depends_on TEXT NOT NULL,
				PRIMARY KEY (capability_id, depends_on)
			);`,
		},
		{
			Version: "028",
			Name:    "add_ai_kill_switches",
			SQL: `CREATE TABLE IF NOT EXISTS ai_kill_switches (
				scope TEXT PRIMARY KEY,
				enabled INTEGER NOT NULL,
				reason TEXT,
				set_by_user_id TEXT,
				set_at INTEGER NOT NULL
			);`,
		},
		{
			Version: "029",
			Name:    "extend_tickets_for_ai",
			SQL: `ALTER TABLE tickets ADD COLUMN tenant_id TEXT DEFAULT 'default';
			ALTER TABLE tickets ADD COLUMN customer_id TEXT;
			ALTER TABLE tickets ADD COLUMN ai_triage TEXT;
			ALTER TABLE tickets ADD COLUMN cluster_id TEXT;
			ALTER TABLE tickets ADD COLUMN related_alert_ids TEXT;
			ALTER TABLE tickets ADD COLUMN root_cause TEXT;
			ALTER TABLE tickets ADD COLUMN resolution_summary TEXT;
			-- Belt-and-suspenders backfill: ALTER ADD COLUMN with DEFAULT
			-- backfills existing rows on Postgres 11+ and SQLite 3.25+, but
			-- older SQLite leaves them NULL. The explicit UPDATE guarantees
			-- every existing ticket lives in the default tenant after the
			-- migration regardless of dialect version.
			UPDATE tickets SET tenant_id = 'default' WHERE tenant_id IS NULL;
			CREATE INDEX IF NOT EXISTS idx_tickets_tenant_status ON tickets(tenant_id, status);
			CREATE TABLE IF NOT EXISTS ticket_clusters (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				customer_id TEXT,
				signature_hash TEXT NOT NULL,
				name TEXT,
				likely_cause TEXT,
				first_seen INTEGER NOT NULL,
				last_seen INTEGER NOT NULL,
				count INTEGER NOT NULL DEFAULT 0,
				status TEXT NOT NULL DEFAULT 'active',
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_ticket_clusters_tenant_active ON ticket_clusters(tenant_id, status, last_seen);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_ticket_clusters_sig ON ticket_clusters(tenant_id, signature_hash);`,
		},
		{
			Version:      "030",
			Name:         "enable_pgvector_and_embeddings",
			PostgresOnly: true,
			SQL: `CREATE EXTENSION IF NOT EXISTS vector;
			CREATE TABLE IF NOT EXISTS ai_embeddings (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				customer_id TEXT,
				source_kind TEXT NOT NULL,
				source_id TEXT NOT NULL,
				text_hash TEXT NOT NULL,
				model_name TEXT NOT NULL,
				dim INTEGER NOT NULL,
				embedding vector(1536),
				created_at BIGINT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_ai_embeddings_tenant ON ai_embeddings(tenant_id, source_kind);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_embeddings_dedup ON ai_embeddings(tenant_id, source_kind, source_id, model_name);`,
		},
		{
			Version: "031",
			Name:    "add_devices_os_class",
			SQL:     `ALTER TABLE devices ADD COLUMN os_class TEXT;`,
		},
		{
			Version: "032",
			Name:    "add_ai_rollback_probes",
			SQL: `CREATE TABLE IF NOT EXISTS ai_rollback_probes (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				capability_id TEXT NOT NULL,
				playbook TEXT NOT NULL,
				token TEXT NOT NULL,
				alert_signature TEXT,
				preconditions TEXT,
				run_at INTEGER NOT NULL,
				rollback_window_ends INTEGER NOT NULL,
				status TEXT NOT NULL DEFAULT 'pending',
				attempts INTEGER NOT NULL DEFAULT 0,
				outcome TEXT,
				outcome_reason TEXT,
				outcome_set_at INTEGER,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_ai_rollback_probes_due ON ai_rollback_probes(status, run_at);
			CREATE INDEX IF NOT EXISTS idx_ai_rollback_probes_tenant ON ai_rollback_probes(tenant_id, capability_id, created_at);
			-- Dedup: a capability that fires twice for the same alert in
			-- quick succession registers two probes with the same
			-- (token, alert_signature). The expression-index COALESCE
			-- treats NULL signatures as the literal "__null__" so two
			-- probes for a no-signature playbook (free_disk_space,
			-- force_gpupdate) collapse to one — Postgres's default
			-- unique-index NULL semantics would otherwise let both rows
			-- through.
			CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_rollback_probes_dedup ON ai_rollback_probes(tenant_id, token, COALESCE(alert_signature, '__null__'));`,
		},
		{
			Version: "033",
			Name:    "add_users_skill_tags",
			SQL: `ALTER TABLE users ADD COLUMN skill_tags TEXT;
			ALTER TABLE users ADD COLUMN routing_weight INTEGER DEFAULT 100;
			ALTER TABLE tickets ADD COLUMN ai_route TEXT;`,
		},
		{
			Version: "034",
			Name:    "add_alerts_table",
			// Persistent incident log surfaced on the dashboard /alerts page.
			// Distinct from alert_rules (config) and audit_logs (every admin
			// action). Rows here are user-acknowledgeable events: a device
			// went offline, CPU pinned for N minutes, AI cluster opened.
			// resolved_at is nullable; the index keeps the open-incidents
			// query (the common dashboard hit) cheap.
			SQL: `CREATE TABLE IF NOT EXISTS alerts (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				device_id TEXT,
				type TEXT NOT NULL,
				severity TEXT NOT NULL DEFAULT 'warning',
				message TEXT NOT NULL,
				resolved INTEGER NOT NULL DEFAULT 0,
				resolved_at INTEGER,
				resolved_by TEXT,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_alerts_tenant_open ON alerts(tenant_id, resolved, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_alerts_device ON alerts(device_id);`,
		},
		{
			Version: "035",
			Name:    "add_ticket_comments_table",
			// Comment thread per ticket. internal=1 means the comment is
			// staff-only and never surfaces to a customer (Stage 12 portal).
			// SLA first-response (handlers/dashboard.go) uses the earliest
			// internal=0 comment timestamp; the index covers that lookup.
			SQL: `CREATE TABLE IF NOT EXISTS ticket_comments (
				id TEXT PRIMARY KEY,
				ticket_id TEXT NOT NULL,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				user_id TEXT NOT NULL,
				body TEXT NOT NULL,
				internal INTEGER NOT NULL DEFAULT 0,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_ticket_comments_ticket ON ticket_comments(ticket_id, created_at);
			CREATE INDEX IF NOT EXISTS idx_ticket_comments_first_response ON ticket_comments(ticket_id, internal, created_at);`,
		},
		{
			Version: "036",
			Name:    "add_inventory_and_groups",
			// device_software is rebuilt-from-scratch on each agent inventory
			// post (see handlers/inventory.go); we don't keep history. Index
			// on (tenant_id, name) supports fleet-wide "who has X" queries.
			// device_groups is flat (no nesting) per Stage 10 plan.
			SQL: `CREATE TABLE IF NOT EXISTS device_software (
				id TEXT PRIMARY KEY,
				device_id TEXT NOT NULL,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				name TEXT NOT NULL,
				version TEXT,
				vendor TEXT,
				install_date INTEGER,
				updated_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_software_device ON device_software(device_id);
			CREATE INDEX IF NOT EXISTS idx_software_tenant_name ON device_software(tenant_id, name);

			CREATE TABLE IF NOT EXISTS device_hardware (
				device_id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				cpu_model TEXT,
				cpu_cores INTEGER,
				ram_bytes INTEGER,
				disk_total_bytes INTEGER,
				platform TEXT,
				platform_version TEXT,
				kernel_version TEXT,
				updated_at INTEGER NOT NULL
			);

			CREATE TABLE IF NOT EXISTS device_groups (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				name TEXT NOT NULL,
				description TEXT,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_groups_tenant ON device_groups(tenant_id);

			CREATE TABLE IF NOT EXISTS device_group_members (
				group_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				PRIMARY KEY (group_id, device_id)
			);
			CREATE INDEX IF NOT EXISTS idx_group_members_device ON device_group_members(device_id);`,
		},
		{
			Version: "037",
			Name:    "patches_v2_and_maintenance_windows",
			// patches gets addressing columns for OS-discovered updates
			// (kb_id for Windows / package name for unix-like; cve list
			// for advisory linkage; source for "where did this come
			// from" — apt/dnf/pacman/winupdate/macos/manual). The unique
			// index lets the agent resync repeatedly without inserting
			// dupes — INSERT ... ON CONFLICT (or INSERT OR IGNORE on
			// SQLite) skips identical rows.
			//
			// maintenance_windows is per-tenant. group_id is nullable so a
			// window can target the whole tenant fleet (NULL = all).
			// weekly_cron is a 3-field expression "minute hour dow"
			// (e.g. "0 2 0" = Sunday 02:00). timezone is an IANA name.
			SQL: `ALTER TABLE patches ADD COLUMN kb_id TEXT;
			ALTER TABLE patches ADD COLUMN cve TEXT;
			ALTER TABLE patches ADD COLUMN source TEXT;
			ALTER TABLE patches ADD COLUMN install_command TEXT;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_patches_dedup ON patches(device_id, source, kb_id);

			CREATE TABLE IF NOT EXISTS maintenance_windows (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				name TEXT NOT NULL,
				group_id TEXT,
				weekly_cron TEXT NOT NULL,
				duration_minutes INTEGER NOT NULL DEFAULT 60,
				timezone TEXT NOT NULL DEFAULT 'UTC',
				enabled INTEGER NOT NULL DEFAULT 1,
				last_run_at INTEGER,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_windows_tenant ON maintenance_windows(tenant_id);`,
		},
		{
			Version: "038",
			Name:    "portal_customers_and_time_entries",
			// customer_users is the SECOND user table — kept entirely
			// separate from the existing `users` table on purpose. Mixing
			// admin and end-user identities in one table risks scope leaks
			// (LIKE-search on email, role mistypes, etc.). Customer scope
			// is enforced by a different JWT issuer + cookie name; the
			// table separation makes that boundary obvious in the data.
			//
			// password_hash uses the same bcrypt routine as users; we do
			// not allow customers to authenticate via OIDC / SSO until
			// Stage 14 — they're local accounts only for now.
			//
			// device_id is optional; if set, the customer only sees
			// tickets attached to that device (single-machine MSP
			// customer). NULL device_id = full tenant visibility for
			// that customer (multi-machine MSP customer).
			//
			// ticket_time_entries is admin-side only — never exposed via
			// the portal endpoints. Used to compute billable hours per
			// ticket / per tenant for monthly export.
			SQL: `CREATE TABLE IF NOT EXISTS customer_users (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				email TEXT NOT NULL,
				name TEXT NOT NULL,
				password_hash TEXT NOT NULL,
				device_id TEXT,
				totp_secret TEXT,
				totp_enabled INTEGER NOT NULL DEFAULT 0,
				last_login INTEGER,
				created_at INTEGER NOT NULL,
				disabled INTEGER NOT NULL DEFAULT 0
			);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_customer_users_email ON customer_users(tenant_id, email);

			CREATE TABLE IF NOT EXISTS ticket_time_entries (
				id TEXT PRIMARY KEY,
				ticket_id TEXT NOT NULL,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				user_id TEXT NOT NULL,
				minutes INTEGER NOT NULL,
				billable INTEGER NOT NULL DEFAULT 1,
				note TEXT,
				started_at INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_time_entries_ticket ON ticket_time_entries(ticket_id);
			CREATE INDEX IF NOT EXISTS idx_time_entries_tenant_started ON ticket_time_entries(tenant_id, started_at);`,
		},
		{
			Version: "039",
			Name:    "network_discovery_cert_snmp",
			// neighbor_observations: agent-reported ARP / ip-neigh entries.
			// Replace-per-agent semantics — each agent rebuilds its own
			// list on each post; index supports the per-subnet aggregation
			// the dashboard renders.
			//
			// cert_monitors: server-side TLS probe targets. internal_allowed
			// gates the SSRF allowlist — defaults 0 so a tenant can't watch
			// internal corp endpoints unless an admin explicitly opts in.
			//
			// snmp_targets: SNMP v3 only. credentials encrypted at rest with
			// internal/crypto. The agent that's on the right subnet polls
			// (server-side polling won't reach customer LANs).
			SQL: `CREATE TABLE IF NOT EXISTS neighbor_observations (
				id TEXT PRIMARY KEY,
				device_id TEXT NOT NULL,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				ip TEXT NOT NULL,
				mac TEXT,
				hostname TEXT,
				iface TEXT,
				observed_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_neighbors_device ON neighbor_observations(device_id);
			CREATE INDEX IF NOT EXISTS idx_neighbors_tenant ON neighbor_observations(tenant_id, observed_at DESC);

			CREATE TABLE IF NOT EXISTS cert_monitors (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				url TEXT NOT NULL,
				alert_threshold_days INTEGER NOT NULL DEFAULT 14,
				internal_allowed INTEGER NOT NULL DEFAULT 0,
				last_checked_at INTEGER,
				last_expiry_at INTEGER,
				last_status TEXT,
				last_error TEXT,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_certs_tenant ON cert_monitors(tenant_id);

			CREATE TABLE IF NOT EXISTS snmp_targets (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				name TEXT NOT NULL,
				host TEXT NOT NULL,
				port INTEGER NOT NULL DEFAULT 161,
				v3_username TEXT NOT NULL,
				v3_auth_protocol TEXT NOT NULL,
				v3_auth_pass_enc TEXT NOT NULL,
				v3_priv_protocol TEXT NOT NULL,
				v3_priv_pass_enc TEXT NOT NULL,
				oids TEXT NOT NULL,
				poll_interval_seconds INTEGER NOT NULL DEFAULT 300,
				enabled INTEGER NOT NULL DEFAULT 1,
				last_polled_at INTEGER,
				last_error TEXT,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_snmp_tenant ON snmp_targets(tenant_id);

			CREATE TABLE IF NOT EXISTS snmp_observations (
				id TEXT PRIMARY KEY,
				target_id TEXT NOT NULL,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				oid TEXT NOT NULL,
				value TEXT NOT NULL,
				observed_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_snmp_obs_target ON snmp_observations(target_id, observed_at DESC);`,
		},
		{
			Version: "040",
			Name:    "auth_sso_webauthn_policies",
			// Postgres-specific DDL: BLOB does not exist; use BYTEA.
			// The SQLite path uses BLOB unchanged. Schema is otherwise
			// identical between dialects.
			PostgresSQL: `CREATE TABLE IF NOT EXISTS tenant_oidc_configs (
				tenant_id TEXT PRIMARY KEY,
				issuer_url TEXT NOT NULL,
				client_id TEXT NOT NULL,
				client_secret_enc TEXT NOT NULL,
				default_role TEXT NOT NULL DEFAULT 'user',
				enabled INTEGER NOT NULL DEFAULT 1,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);

			CREATE TABLE IF NOT EXISTS oidc_states (
				state TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				nonce TEXT NOT NULL,
				code_verifier TEXT NOT NULL,
				redirect_uri TEXT NOT NULL,
				expires_at INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_oidc_states_expires ON oidc_states(expires_at);

			CREATE TABLE IF NOT EXISTS webauthn_credentials (
				id TEXT PRIMARY KEY,
				user_id TEXT NOT NULL,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				credential_id BYTEA NOT NULL,
				public_key BYTEA NOT NULL,
				aaguid BYTEA,
				sign_count INTEGER NOT NULL DEFAULT 0,
				transports TEXT,
				name TEXT,
				created_at INTEGER NOT NULL,
				last_used_at INTEGER
			);
			CREATE INDEX IF NOT EXISTS idx_webauthn_user ON webauthn_credentials(user_id);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_webauthn_credid ON webauthn_credentials(credential_id);

			CREATE TABLE IF NOT EXISTS webauthn_sessions (
				id TEXT PRIMARY KEY,
				user_id TEXT,
				kind TEXT NOT NULL,
				challenge BYTEA NOT NULL,
				expires_at INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_expires ON webauthn_sessions(expires_at);

			CREATE TABLE IF NOT EXISTS report_schedules (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				name TEXT NOT NULL,
				report_type TEXT NOT NULL,
				weekly_cron TEXT NOT NULL,
				timezone TEXT NOT NULL DEFAULT 'UTC',
				email_recipients TEXT NOT NULL,
				enabled INTEGER NOT NULL DEFAULT 1,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL,
				last_run_at INTEGER,
				last_error TEXT
			);

			CREATE TABLE IF NOT EXISTS tenant_policies (
				tenant_id TEXT PRIMARY KEY,
				audit_retention_days INTEGER NOT NULL DEFAULT 365,
				metrics_retention_days INTEGER NOT NULL DEFAULT 90,
				ticket_comment_retention_days INTEGER NOT NULL DEFAULT 0,
				time_entry_retention_days INTEGER NOT NULL DEFAULT 0,
				failed_login_threshold INTEGER NOT NULL DEFAULT 10,
				lockout_minutes INTEGER NOT NULL DEFAULT 15,
				updated_at INTEGER NOT NULL
			);`,
			// tenant_oidc_configs: per-tenant OIDC provider. client_secret
			// encrypted at rest. JIT-provisioned users get role="user" by
			// default; admins can promote later.
			//
			// oidc_states: ephemeral state+nonce store for OIDC callback.
			// Rows expire ~10 min after issue. We use a DB row instead of
			// a signed cookie so a forged callback can't replay state from
			// an attacker-controlled origin.
			//
			// webauthn_credentials: stored after registration. AAGUID +
			// counter let us detect cloned authenticators.
			//
			// tenant_policies: per-tenant retention + lockout knobs.
			// Defaults baked into the app code if no row exists.
			SQL: `CREATE TABLE IF NOT EXISTS tenant_oidc_configs (
				tenant_id TEXT PRIMARY KEY,
				issuer_url TEXT NOT NULL,
				client_id TEXT NOT NULL,
				client_secret_enc TEXT NOT NULL,
				default_role TEXT NOT NULL DEFAULT 'user',
				enabled INTEGER NOT NULL DEFAULT 1,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);

			CREATE TABLE IF NOT EXISTS oidc_states (
				state TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL,
				nonce TEXT NOT NULL,
				code_verifier TEXT NOT NULL,
				redirect_uri TEXT NOT NULL,
				expires_at INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_oidc_states_expires ON oidc_states(expires_at);

			CREATE TABLE IF NOT EXISTS webauthn_credentials (
				id TEXT PRIMARY KEY,
				user_id TEXT NOT NULL,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				credential_id BLOB NOT NULL,
				public_key BLOB NOT NULL,
				aaguid BLOB,
				sign_count INTEGER NOT NULL DEFAULT 0,
				transports TEXT,
				name TEXT,
				created_at INTEGER NOT NULL,
				last_used_at INTEGER
			);
			CREATE INDEX IF NOT EXISTS idx_webauthn_user ON webauthn_credentials(user_id);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_webauthn_credid ON webauthn_credentials(credential_id);

			CREATE TABLE IF NOT EXISTS webauthn_sessions (
				id TEXT PRIMARY KEY,
				user_id TEXT,
				kind TEXT NOT NULL,
				challenge BLOB NOT NULL,
				expires_at INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_expires ON webauthn_sessions(expires_at);

			CREATE TABLE IF NOT EXISTS report_schedules (
				id TEXT PRIMARY KEY,
				tenant_id TEXT NOT NULL DEFAULT 'default',
				name TEXT NOT NULL,
				report_type TEXT NOT NULL,
				weekly_cron TEXT NOT NULL,
				timezone TEXT NOT NULL DEFAULT 'UTC',
				email_recipients TEXT NOT NULL,
				enabled INTEGER NOT NULL DEFAULT 1,
				last_run_at INTEGER,
				last_error TEXT,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_reports_tenant ON report_schedules(tenant_id);

			CREATE TABLE IF NOT EXISTS tenant_policies (
				tenant_id TEXT PRIMARY KEY,
				audit_retention_days INTEGER NOT NULL DEFAULT 365,
				metrics_retention_days INTEGER NOT NULL DEFAULT 90,
				ticket_comment_retention_days INTEGER NOT NULL DEFAULT 0,
				time_entry_retention_days INTEGER NOT NULL DEFAULT 0,
				failed_login_threshold INTEGER NOT NULL DEFAULT 10,
				lockout_minutes INTEGER NOT NULL DEFAULT 15,
				updated_at INTEGER NOT NULL
			);`,
		},
		{
			Version: "041",
			Name:    "device_network_latency",
			// network_latency_ms is the round-trip time the agent
			// measured to the server on its previous heartbeat. Updated
			// on each heartbeat. We sanity-clamp the value at write
			// time (see handlers/agent.go) so a buggy agent reporting
			// 999999ms doesn't poison the dashboard average.
			SQL: `ALTER TABLE devices ADD COLUMN network_latency_ms INTEGER DEFAULT 0;`,
		},
		{
			Version: "042",
			Name:    "audit_log_tamper_evidence",
			// Tamper-evident hash chain over audit_logs. Each row's
			// signature is HMAC-SHA256 over the previous row's signature
			// concatenated with a canonical encoding of this row.
			// Rewriting any field, deleting a row, or inserting a row
			// out of order breaks the chain at the tampered point and
			// every row downstream of it. The verifier endpoint walks
			// the chain and reports the first break.
			//
			// Existing rows (pre-migration) get the signature backfilled
			// in chain order so the chain is contiguous from epoch 0
			// onward. Operators who don't trust the integrity of pre-
			// migration data can purge audit_logs and restart the
			// chain — the migration leaves that decision to them.
			//
			// SQLite ALTER TABLE ADD COLUMN does not allow NOT NULL
			// without a default, so the column lands with DEFAULT '' and
			// is populated by a backfill pass below. New writes from
			// AuditLogTenant always set a non-empty value.
			SQL: `ALTER TABLE audit_logs ADD COLUMN signature TEXT NOT NULL DEFAULT '';`,
		},
		{
			Version: "043",
			Name:    "audit_log_chain_seq",
			// Explicit per-tenant chain ordering. The original chain
			// in migration 042 sorted by (created_at, id), which broke
			// the moment retention deleted old rows: the chain needs
			// to be navigable past gaps, and a compaction record
			// inserted now has a created_at greater than the rows it
			// bridges so it sorts at the wrong end of the chain.
			//
			// chain_seq is a monotonic per-tenant counter assigned
			// under auditChainMu. Compaction records claim the
			// smallest chain_seq of the rows they replace, so the
			// verifier walks rows in chain_seq ASC and sees the CR
			// in the spot the deleted rows used to occupy.
			//
			// Backfill in code (events.BackfillAuditChain) walks rows
			// in (tenant_id, created_at, id) order and assigns seq
			// starting from 1 per tenant.
			SQL: `ALTER TABLE audit_logs ADD COLUMN chain_seq INTEGER NOT NULL DEFAULT 0;`,
		},
		{
			Version: "044",
			Name:    "agent_tokens_supersede",
			// Bound the growth of agent_tokens across re-registers.
			// Pre-fix, every re-registration (token rotation, agent
			// reinstall, the old retry-exhaustion loop) inserted a
			// fresh row and left every prior token row in place. Over
			// months a single host accumulated dozens of rows that
			// were no longer reachable from any current agent.
			//
			// superseded_at marks rows replaced by a newer
			// registration for the same (tenant_id, device_id,
			// hostname). AuthMiddleware rejects tokens past their
			// superseded_at; the in-memory cache prune sweeps them.
			// A small overlap window (default 60s) prevents an
			// in-flight heartbeat carrying the old token from
			// 401-flapping during rotation.
			SQL: `ALTER TABLE agent_tokens ADD COLUMN superseded_at INTEGER NOT NULL DEFAULT 0;`,
		},
		{
			Version: "045",
			Name:    "agent_tokens_proof_of_possession",
			// Codex #6: re-registration hijack fix. The current re-register
			// handler matches an existing device by (tenant_id, hostname,
			// mac_address) — all client-controlled — and silently rotates
			// its token. Anyone holding REGISTRATION_SECRET can take over
			// any device by claiming its hostname+MAC. The fix requires
			// the caller to prove possession of either the active token
			// or the recently-rotated previous token (60s grace window
			// for in-flight heartbeats and benign rotation races).
			//
			// previous_token_hash + previous_token_rotated_at sit on the
			// CURRENT (active) row: a single-step memory of the token
			// that was superseded by the row's creation. Single previous,
			// not a chain — the proposal doc rules out chained history.
			//
			// devices.legacy_pop_bypass_used records whether the device
			// has already consumed its one-time pre-migration bypass.
			// Agents in the field built before this commit do not carry
			// a persisted token to present; the bypass lets them
			// re-register exactly once, after which the device is on
			// the standard PoP track for every subsequent re-register.
			SQL: `ALTER TABLE agent_tokens ADD COLUMN previous_token_hash TEXT;
			      ALTER TABLE agent_tokens ADD COLUMN previous_token_rotated_at INTEGER;
			      ALTER TABLE devices ADD COLUMN legacy_pop_bypass_used INTEGER NOT NULL DEFAULT 0;
			      CREATE INDEX IF NOT EXISTS idx_agent_tokens_active_device ON agent_tokens(tenant_id, device_id, hostname, superseded_at);`,
		},
	}

	for _, m := range migrations {
		var exists int
		if err := DB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.Version).Scan(&exists); err != nil {
			slog.Warn("db query row scan failed", "error", err)
		}
		if exists > 0 {
			continue
		}
		// Skip Postgres-only migrations on SQLite. We still record them so a
		// later switch to Postgres knows to run the gap (we deliberately do
		// NOT mark them applied; the next runMigrations call on Postgres will
		// pick them up).
		if m.PostgresOnly && dialect != "postgres" {
			slog.Info("migration skipped (postgres-only)", "version", m.Version, "name", m.Name)
			continue
		}

		sqlStmt := m.SQL
		if dialect == "postgres" && m.PostgresSQL != "" {
			sqlStmt = m.PostgresSQL
		}
		_, err := DB.Exec(sqlStmt)
		if err != nil {
			// Tolerate idempotent re-runs (duplicate column, already exists)
			// and the special case of CREATE EXTENSION on Postgres without
			// superuser — operators with managed Postgres (RDS, Azure)
			// should pre-install pgvector, and we shouldn't block server
			// boot on the AI-only extension migration.
			errStr := err.Error()
			tolerated := strings.Contains(errStr, "duplicate column name") ||
				strings.Contains(errStr, "already exists") ||
				strings.Contains(errStr, "42701")
			extPermDenied := strings.Contains(errStr, "permission denied") &&
				strings.Contains(strings.ToLower(m.SQL), "create extension")
			if extPermDenied {
				slog.Warn("migration skipped — CREATE EXTENSION requires superuser; ask DBA to install manually",
					"version", m.Version, "name", m.Name, "error", err)
				continue
			}
			if !tolerated {
				slog.Warn("migration failed", "version", m.Version, "error", err)
				continue
			}
		}

		_, err = DB.Exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
			m.Version, m.Name, time.Now().Unix())
		if err != nil {
			return fmt.Errorf("failed to record migration %s: %w", m.Version, err)
		}
		slog.Info("migration applied", "version", m.Version, "name", m.Name)
	}

	return nil
}

// EnsureDefaultTenant inserts the 'default' tenant row if missing.
// Idempotent: safe to call on every startup.
func EnsureDefaultTenant() {
	var count int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM tenants WHERE id = 'default'`).Scan(&count); err != nil {
		slog.Warn("could not check default tenant", "error", err)
		return
	}
	if count > 0 {
		return
	}
	now := time.Now().Unix()
	if _, err := DB.Exec(
		`INSERT INTO tenants (id, name, slug, plan, status, created_at, updated_at) VALUES ('default', 'Default', 'default', 'free', 'active', ?, ?)`,
		now, now,
	); err != nil {
		slog.Warn("could not create default tenant", "error", err)
		return
	}
	slog.Info("created default tenant")
}

var DB *Wrapper

func Init() error {
	var rawDB *sql.DB
	var err error

	// Detect driver from DATABASE_URL (postgres) or DATABASE_PATH (sqlite)
	databaseURL := os.Getenv("DATABASE_URL")
	dialect := "sqlite"

	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		dialect = "postgres"
		rawDB, err = sql.Open("postgres", databaseURL)
		if err != nil {
			return fmt.Errorf("failed to open postgres: %w", err)
		}
		// Connection pool tuning for production
		rawDB.SetMaxOpenConns(25)
		rawDB.SetMaxIdleConns(5)
		rawDB.SetConnMaxLifetime(5 * time.Minute)
		slog.Info("Using PostgreSQL database")
	} else {
		dbPath := os.Getenv("DATABASE_PATH")
		if dbPath == "" {
			dbPath = "./data/vapor_rmm.db"
		}
		if err := os.MkdirAll("./data", 0755); err != nil {
			return fmt.Errorf("failed to create data directory: %w", err)
		}
		rawDB, err = sql.Open("sqlite3", dbPath)
		if err != nil {
			return fmt.Errorf("failed to open sqlite: %w", err)
		}
		slog.Info("using sqlite database", "path", dbPath)
	}

	DB = &Wrapper{DB: rawDB, Dialect: dialect}

	if err := DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	// SQLite-only tuning
	if DB.Dialect == "sqlite" {
		if _, err := DB.DB.Exec(`PRAGMA journal_mode=WAL`); err != nil {
			slog.Warn("could not enable WAL mode", "error", err)
		}
		if _, err := DB.DB.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			slog.Warn("could not enable foreign keys", "error", err)
		}
	}

	// Create branding table
	createBrandingSQL := `
	CREATE TABLE IF NOT EXISTS branding (
		id TEXT PRIMARY KEY DEFAULT 'default',
		app_name TEXT DEFAULT 'vaporRMM',
		icon_url TEXT DEFAULT '',
		company_name TEXT DEFAULT 'Vaporware RMM',
		primary_color TEXT DEFAULT '#3b82f6'
	);`

	if _, err = DB.Exec(createBrandingSQL); err != nil {
		return fmt.Errorf("failed to create branding table: %w", err)
	}

	// Insert default branding — dialect-aware upsert
	var insertDefaultBranding string
	if DB.Dialect == "postgres" {
		insertDefaultBranding = `
		INSERT INTO branding (id, app_name, icon_url, company_name, primary_color)
		VALUES ('default', 'vaporRMM', '', 'Vaporware RMM', '#3b82f6')
		ON CONFLICT (id) DO NOTHING;`
	} else {
		insertDefaultBranding = `
		INSERT OR IGNORE INTO branding (id, app_name, icon_url, company_name, primary_color)
		VALUES ('default', 'vaporRMM', '', 'Vaporware RMM', '#3b82f6');`
	}

	if _, err = DB.Exec(insertDefaultBranding); err != nil {
		slog.Warn("could not insert default branding", "error", err)
	}

	// Create devices table
	createDevicesSQL := `
	CREATE TABLE IF NOT EXISTS devices (
		id TEXT PRIMARY KEY,
		name TEXT,
		hostname TEXT,
		ip_address TEXT,
		mac_address TEXT,
		os_name TEXT,
		os_version TEXT,
		kernel_version TEXT,
		agent_version TEXT,
		status TEXT DEFAULT 'offline',
		last_seen INTEGER,
		created_at INTEGER,
		public_key TEXT,
		user_data TEXT,
		system_uuid TEXT,
		serial_number TEXT,
		manufacturer TEXT,
		model TEXT,
		cpu TEXT,
		memory INTEGER,
		disk_size INTEGER,
		timezone TEXT,
		agent_port INTEGER,
		agent_ip TEXT
	);`

	if _, err = DB.Exec(createDevicesSQL); err != nil {
		return fmt.Errorf("failed to create devices table: %w", err)
	}

	// Add tags column to devices table (migration)
	if DB.Dialect == "postgres" {
		if _, err := DB.Exec(`ALTER TABLE devices ADD COLUMN tags TEXT DEFAULT ''`); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
	} else {
		if _, err := DB.Exec(`ALTER TABLE devices ADD COLUMN tags TEXT DEFAULT ''`); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
	}

	// Create device_commands table (moved from handler)
	createCommandsSQL := `
	CREATE TABLE IF NOT EXISTS device_commands (
		id TEXT PRIMARY KEY,
		device_id TEXT,
		type TEXT,
		payload TEXT,
		status TEXT DEFAULT 'pending',
		output TEXT,
		created_at INTEGER,
		finished_at INTEGER
	);`

	if _, err = DB.Exec(createCommandsSQL); err != nil {
		return fmt.Errorf("failed to create device_commands table: %w", err)
	}

	// Create index for faster command lookups
	createIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_device_commands_device_status 
	ON device_commands(device_id, status);`

	if _, err = DB.Exec(createIndexSQL); err != nil {
		slog.Warn("could not create index", "error", err)
	}

	// Create users table for authentication
	createUsersSQL := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		name TEXT,
		role TEXT DEFAULT 'admin',
		created_at INTEGER,
		last_login INTEGER
	);`

	if _, err = DB.Exec(createUsersSQL); err != nil {
		return fmt.Errorf("failed to create users table: %w", err)
	}

	// Create password_resets table
	createPasswordResetsSQL := `
	CREATE TABLE IF NOT EXISTS password_resets (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		expires_at INTEGER NOT NULL,
		used INTEGER DEFAULT 0,
		created_at INTEGER NOT NULL
	);`
	if _, err = DB.Exec(createPasswordResetsSQL); err != nil {
		return fmt.Errorf("failed to create password_resets table: %w", err)
	}

	// Migrate: add Sunshine/Tailscale columns to devices if they don't exist.
	// PostgreSQL supports IF NOT EXISTS; SQLite ignores the error.
	addColFmt := "ALTER TABLE devices ADD COLUMN %s"
	if DB.Dialect == "postgres" {
		addColFmt = "ALTER TABLE devices ADD COLUMN %s"
	}
	for _, col := range []string{
		"sunshine_installed INTEGER DEFAULT 0",
		"sunshine_running INTEGER DEFAULT 0",
		"sunshine_port INTEGER DEFAULT 47990",
		"tailscale_installed INTEGER DEFAULT 0",
		"tailscale_connected INTEGER DEFAULT 0",
		"tailscale_ip TEXT",
		"tailscale_hostname TEXT",
		"tailscale_peers INTEGER DEFAULT 0",
		"tailscale_backend_state TEXT",
	} {
		if _, err := DB.DB.Exec(fmt.Sprintf(addColFmt, col)); err != nil {
			slog.Warn("db exec failed", "error", err)
		}
	}

	// Create agent_tokens table for persistent agent authentication
	createAgentTokensSQL := `
	CREATE TABLE IF NOT EXISTS agent_tokens (
		token_hash TEXT PRIMARY KEY,
		token TEXT DEFAULT '',
		device_id TEXT NOT NULL,
		hostname TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER DEFAULT 0
	);`

	if _, err = DB.Exec(createAgentTokensSQL); err != nil {
		return fmt.Errorf("failed to create agent_tokens table: %w", err)
	}

	// Migrate legacy schema: add token_hash if missing
	if _, err := DB.Exec(`ALTER TABLE agent_tokens ADD COLUMN token_hash TEXT`); err != nil {
		slog.Warn("db exec failed", "error", err)
	}
	if _, err := DB.Exec(`ALTER TABLE agent_tokens ADD COLUMN token TEXT DEFAULT ''`); err != nil {
		slog.Warn("db exec failed", "error", err)
	}

	// Create user_sessions table for session management
	createSessionsSQL := `
	CREATE TABLE IF NOT EXISTS user_sessions (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		ip_address TEXT,
		user_agent TEXT,
		created_at INTEGER NOT NULL,
		last_seen INTEGER NOT NULL
	);`
	if _, err = DB.Exec(createSessionsSQL); err != nil {
		return fmt.Errorf("failed to create user_sessions table: %w", err)
	}

	// Create metrics_history table — serial/autoincrement is dialect-specific
	var metricsIDCol string
	if DB.Dialect == "postgres" {
		metricsIDCol = "id BIGSERIAL PRIMARY KEY,"
	} else {
		metricsIDCol = "id INTEGER PRIMARY KEY AUTOINCREMENT,"
	}
	createMetricsSQL := fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS metrics_history (
		%s
		device_id TEXT NOT NULL,
		cpu_usage REAL,
		memory_usage REAL,
		disk_usage REAL,
		recorded_at INTEGER NOT NULL
	);`, metricsIDCol)

	if _, err = DB.Exec(createMetricsSQL); err != nil {
		return fmt.Errorf("failed to create metrics_history table: %w", err)
	}

	createMetricsIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_metrics_device_time
	ON metrics_history(device_id, recorded_at DESC);`

	if _, err = DB.Exec(createMetricsIndexSQL); err != nil {
		slog.Warn("could not create metrics index", "error", err)
	}

	// Create audit_logs table
	createAuditLogsSQL := `
	CREATE TABLE IF NOT EXISTS audit_logs (
		id TEXT PRIMARY KEY,
		user_id TEXT,
		action TEXT NOT NULL,
		resource_type TEXT,
		resource_id TEXT,
		details TEXT,
		ip_address TEXT,
		created_at INTEGER NOT NULL
	);`
	if _, err = DB.Exec(createAuditLogsSQL); err != nil {
		return fmt.Errorf("failed to create audit_logs table: %w", err)
	}

	createAuditIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_audit_user
	ON audit_logs(user_id, created_at DESC);`
	if _, err := DB.Exec(createAuditIndexSQL); err != nil {
		slog.Warn("db exec failed", "error", err)
	}

	// Create webhooks table
	createWebhooksSQL := `
	CREATE TABLE IF NOT EXISTS webhooks (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		secret TEXT,
		events TEXT NOT NULL,
		enabled INTEGER DEFAULT 1,
		created_at INTEGER NOT NULL
	);`
	if _, err = DB.Exec(createWebhooksSQL); err != nil {
		return fmt.Errorf("failed to create webhooks table: %w", err)
	}

	// Create patches table
	createPatchesSQL := `
	CREATE TABLE IF NOT EXISTS patches (
		id TEXT PRIMARY KEY,
		device_id TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT,
		severity TEXT DEFAULT 'medium',
		status TEXT DEFAULT 'pending',
		installed_at INTEGER,
		created_at INTEGER NOT NULL
	);`
	if _, err = DB.Exec(createPatchesSQL); err != nil {
		return fmt.Errorf("failed to create patches table: %w", err)
	}

	// Create scripts table (script library)
	createScriptsSQL := `
	CREATE TABLE IF NOT EXISTS scripts (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT,
		content TEXT NOT NULL,
		platform TEXT DEFAULT 'all',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`
	if _, err = DB.Exec(createScriptsSQL); err != nil {
		return fmt.Errorf("failed to create scripts table: %w", err)
	}

	// Create compliance_results table
	createComplianceSQL := `
	CREATE TABLE IF NOT EXISTS compliance_results (
		id TEXT PRIMARY KEY,
		device_id TEXT NOT NULL,
		check_type TEXT NOT NULL,
		status TEXT DEFAULT 'fail',
		details TEXT,
		severity TEXT DEFAULT 'medium',
		created_at INTEGER NOT NULL
	);`
	if _, err = DB.Exec(createComplianceSQL); err != nil {
		return fmt.Errorf("failed to create compliance_results table: %w", err)
	}

	// Create file_transfers table
	createFileTransfersSQL := `
	CREATE TABLE IF NOT EXISTS file_transfers (
		id TEXT PRIMARY KEY,
		device_id TEXT NOT NULL,
		type TEXT NOT NULL,
		file_name TEXT NOT NULL,
		file_path TEXT NOT NULL,
		status TEXT DEFAULT 'pending',
		progress INTEGER DEFAULT 0,
		created_at INTEGER NOT NULL,
		completed_at INTEGER
	);`
	if _, err = DB.Exec(createFileTransfersSQL); err != nil {
		return fmt.Errorf("failed to create file_transfers table: %w", err)
	}

	createFileTransferIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_file_transfers_device_status
	ON file_transfers(device_id, status);`
	if _, err = DB.Exec(createFileTransferIndexSQL); err != nil {
		slog.Warn("could not create file_transfers index", "error", err)
	}

	// Create alert_settings table for SMTP configuration
	createAlertSettingsSQL := `
	CREATE TABLE IF NOT EXISTS alert_settings (
		id TEXT PRIMARY KEY DEFAULT 'default',
		smtp_host TEXT,
		smtp_port INTEGER DEFAULT 587,
		smtp_user TEXT,
		smtp_password TEXT,
		smtp_from TEXT,
		smtp_tls INTEGER DEFAULT 1,
		enabled INTEGER DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`
	if _, err = DB.Exec(createAlertSettingsSQL); err != nil {
		return fmt.Errorf("failed to create alert_settings table: %w", err)
	}

	// Create alert_rules table for configurable alert rules
	createAlertRulesSQL := `
	CREATE TABLE IF NOT EXISTS alert_rules (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		event_type TEXT NOT NULL,
		severity TEXT DEFAULT 'medium',
		enabled INTEGER DEFAULT 1,
		email_recipients TEXT,
		webhook_url TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`
	if _, err = DB.Exec(createAlertRulesSQL); err != nil {
		return fmt.Errorf("failed to create alert_rules table: %w", err)
	}

	// Run schema migrations
	if err := RunMigrations(dialect); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

// BackupSQLite creates a hot backup of the SQLite database to dstPath.
func BackupSQLite(dstPath string) error {
	srcConn, err := DB.DB.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get source connection: %w", err)
	}
	defer srcConn.Close()

	return srcConn.Raw(func(srcRaw interface{}) error {
		srcDB, ok := srcRaw.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("source connection is not sqlite3")
		}

		dstDB, err := sql.Open("sqlite3", dstPath)
		if err != nil {
			return fmt.Errorf("failed to open destination db: %w", err)
		}
		defer dstDB.Close()

		dstConn, err := dstDB.Conn(context.Background())
		if err != nil {
			return fmt.Errorf("failed to get destination connection: %w", err)
		}
		defer dstConn.Close()

		return dstConn.Raw(func(dstRaw interface{}) error {
			dstSQLite, ok := dstRaw.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("destination connection is not sqlite3")
			}
			backup, err := dstSQLite.Backup("main", srcDB, "main")
			if err != nil {
				return fmt.Errorf("failed to initialize backup: %w", err)
			}
			defer func() { _ = backup.Finish() }()
			_, err = backup.Step(-1)
			return err
		})
	})
}
