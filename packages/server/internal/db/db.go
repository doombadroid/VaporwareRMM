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
	}

	for _, m := range migrations {
		var exists int
		if err := DB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.Version).Scan(&exists); err != nil {
			slog.Warn("db query row scan failed", "error", err)
		}
		if exists > 0 {
			continue
		}

		// For PostgreSQL, we need to handle IF NOT EXISTS differently
		// For simplicity, we skip errors on duplicate columns
		if dialect == "postgres" {
			// PostgreSQL supports IF NOT EXISTS for some ALTER TABLE ops
			// but not all. We just try to run and ignore certain errors.
		}

		_, err := DB.Exec(m.SQL)
		if err != nil {
			// Ignore "duplicate column" errors (SQLite: 1, PostgreSQL: 42701)
			errStr := err.Error()
			if !strings.Contains(errStr, "duplicate column name") &&
				!strings.Contains(errStr, "already exists") &&
				!strings.Contains(errStr, "42701") {
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
			defer backup.Finish()
			_, err = backup.Step(-1)
			return err
		})
	})
}
