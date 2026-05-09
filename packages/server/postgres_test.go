package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"vaporrmm/server/internal/db"

	"github.com/google/uuid"
)

// runOnPostgres skips the test if DATABASE_URL is not a postgres URL.
// Used by callers that need to validate behaviour against the real Postgres driver
// (placeholder rewriting, IF NOT EXISTS support, concurrent writes).
func runOnPostgres(t *testing.T) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if !strings.HasPrefix(url, "postgres://") {
		t.Skip("DATABASE_URL not set to a postgres:// URL; skipping Postgres test")
	}
}

// TestPostgres_MigrationsAndIsolation verifies that the full migration set
// applies to a clean Postgres database and that tenant isolation holds.
// This catches dialect drift between SQLite and Postgres (placeholder rewriter,
// IF NOT EXISTS quirks, type coercions).
func TestPostgres_MigrationsAndIsolation(t *testing.T) {
	runOnPostgres(t)

	if err := db.Init(); err != nil {
		t.Fatalf("db.Init on postgres: %v", err)
	}
	defer db.DB.Close()
	db.EnsureDefaultTenant()

	if db.DB.Dialect != "postgres" {
		t.Fatalf("expected postgres dialect, got %s", db.DB.Dialect)
	}

	// Sanity: schema_migrations must contain the latest tenant migrations.
	var n int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version IN ('017','018')`).Scan(&n); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if n != 2 {
		t.Errorf("expected migrations 017 and 018 applied, got %d", n)
	}

	// Sanity: tenant table exists and has the default tenant.
	var defaultName string
	if err := db.DB.QueryRow(`SELECT name FROM tenants WHERE id = 'default'`).Scan(&defaultName); err != nil {
		t.Fatalf("default tenant missing on postgres: %v", err)
	}

	// Insert two tenants and one device in each, verify isolation queries hold.
	tA := "pg-a-" + uuid.New().String()[:8]
	tB := "pg-b-" + uuid.New().String()[:8]
	now := time.Now().Unix()
	for _, tid := range []string{tA, tB} {
		if _, err := db.DB.Exec(
			`INSERT INTO tenants (id, name, slug, plan, status, created_at, updated_at) VALUES (?, ?, ?, 'free', 'active', ?, ?)`,
			tid, tid, tid, now, now,
		); err != nil {
			t.Fatalf("insert tenant: %v", err)
		}
	}

	dA := uuid.New().String()
	dB := uuid.New().String()
	for _, x := range []struct{ id, tid string }{{dA, tA}, {dB, tB}} {
		if _, err := db.DB.Exec(
			`INSERT INTO devices (id, name, hostname, ip_address, mac_address, os_name, os_version, status, last_seen, created_at, tenant_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			x.id, x.id, "host-"+x.id[:6], "10.0.0.1", "aa:bb:cc:dd:ee:ff", "linux", "ubuntu", "online", now, now, x.tid,
		); err != nil {
			t.Fatalf("insert device: %v", err)
		}
	}

	count := func(tid string) int {
		var n int
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE tenant_id = ?`, tid).Scan(&n)
		return n
	}
	if count(tA) != 1 {
		t.Errorf("tenant A device count = %d, want 1", count(tA))
	}
	if count(tB) != 1 {
		t.Errorf("tenant B device count = %d, want 1", count(tB))
	}

	// Verify the placeholder rewriter actually rewrote ? to $1,$2,... for postgres.
	// Indirect proof: this query would syntax-error on postgres if rewriter is broken.
	var hostname string
	if err := db.DB.QueryRow(`SELECT hostname FROM devices WHERE id = ? AND tenant_id = ?`, dA, tA).Scan(&hostname); err != nil {
		t.Fatalf("scoped device lookup on postgres failed (placeholder rewrite): %v", err)
	}
	if hostname != "host-"+dA[:6] {
		t.Errorf("hostname = %q, want host-%s", hostname, dA[:6])
	}

	// Cleanup so re-runs of the test don't leave state.
	_, _ = db.DB.Exec(`DELETE FROM devices WHERE tenant_id IN (?, ?)`, tA, tB)
	_, _ = db.DB.Exec(`DELETE FROM tenants WHERE id IN (?, ?)`, tA, tB)
}
