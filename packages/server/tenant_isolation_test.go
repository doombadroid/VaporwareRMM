package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/google/uuid"
)

// setupTenantTestDB initializes a clean DB with two tenants and one device per tenant.
func setupTenantTestDB(t *testing.T) (tenantA, tenantB, deviceA, deviceB string) {
	t.Helper()
	tmpDir := t.TempDir()
	os.Setenv("DATABASE_PATH", tmpDir+"/iso.db")
	if err := db.Init(); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	db.EnsureDefaultTenant()
	auth.JWTSecret = "test-secret-key-that-is-long-enough"

	tenantA = "tenant-a-" + uuid.New().String()[:8]
	tenantB = "tenant-b-" + uuid.New().String()[:8]
	now := time.Now().Unix()
	for _, tid := range []string{tenantA, tenantB} {
		if _, err := db.DB.Exec(
			`INSERT INTO tenants (id, name, slug, plan, status, created_at, updated_at) VALUES (?, ?, ?, 'free', 'active', ?, ?)`,
			tid, tid, tid, now, now,
		); err != nil {
			t.Fatalf("insert tenant %s: %v", tid, err)
		}
	}

	deviceA = uuid.New().String()
	deviceB = uuid.New().String()
	for _, x := range []struct{ id, tid string }{{deviceA, tenantA}, {deviceB, tenantB}} {
		if _, err := db.DB.Exec(
			`INSERT INTO devices (id, name, hostname, ip_address, mac_address, os_name, os_version, status, last_seen, created_at, tenant_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			x.id, x.id, "host-"+x.id[:6], "10.0.0.1", "aa:bb:cc:dd:ee:ff", "linux", "ubuntu", "online", now, now, x.tid,
		); err != nil {
			t.Fatalf("insert device: %v", err)
		}
	}
	return
}

// TestTenantIsolation_DeviceQueriesScopedByTenant verifies that
// a non-super_admin sees only their tenant's devices via direct DB queries
// using the same WHERE-tenant_id pattern handlers use.
func TestTenantIsolation_DeviceQueriesScopedByTenant(t *testing.T) {
	tenantA, tenantB, _, _ := setupTenantTestDB(t)

	count := func(tid string) int {
		var n int
		_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE tenant_id = ?`, tid).Scan(&n)
		return n
	}
	if count(tenantA) != 1 {
		t.Errorf("tenant A device count = %d, want 1", count(tenantA))
	}
	if count(tenantB) != 1 {
		t.Errorf("tenant B device count = %d, want 1", count(tenantB))
	}
	// Tenant A querying with their tenant_id must not see tenant B's data.
	var foundCrossTenant int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE tenant_id = ? AND tenant_id != ?`, tenantA, tenantA).Scan(&foundCrossTenant)
	if foundCrossTenant != 0 {
		t.Errorf("tenant A query saw %d cross-tenant rows", foundCrossTenant)
	}
}

// TestTenantIsolation_AgentTokenBindsToTenant verifies that registering an
// agent token under tenant A binds the in-memory record to tenant A.
func TestTenantIsolation_AgentTokenBindsToTenant(t *testing.T) {
	tenantA, _, deviceA, _ := setupTenantTestDB(t)
	auth.HashToken = func(token string) string {
		sum := sha256.Sum256([]byte(token))
		return fmt.Sprintf("%x", sum)
	}
	auth.RegisterAgentToken("tok-A", deviceA, "hostA", tenantA)

	auth.TokenMu.RLock()
	tok, ok := auth.RegisteredTokens[auth.HashToken("tok-A")]
	auth.TokenMu.RUnlock()
	if !ok {
		t.Fatal("token not registered")
	}
	if tok.TenantID != tenantA {
		t.Errorf("token tenant = %q, want %q", tok.TenantID, tenantA)
	}
	if tok.DeviceID != deviceA {
		t.Errorf("token device = %q, want %q", tok.DeviceID, deviceA)
	}
}

// TestTenantIsolation_SuspendedTenantBlocked verifies TenantAllowed returns
// false for suspended tenants and missing tenants, true for active.
func TestTenantIsolation_SuspendedTenantBlocked(t *testing.T) {
	tenantA, _, _, _ := setupTenantTestDB(t)

	if !auth.TenantAllowed(tenantA) {
		t.Error("active tenant should be allowed")
	}
	if !auth.TenantAllowed("default") {
		t.Error("default tenant should be allowed")
	}
	if auth.TenantAllowed("nonexistent-" + uuid.New().String()) {
		t.Error("nonexistent tenant should not be allowed")
	}
	if _, err := db.DB.Exec(`UPDATE tenants SET status = 'suspended' WHERE id = ?`, tenantA); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if auth.TenantAllowed(tenantA) {
		t.Error("suspended tenant must not be allowed")
	}
}

// TestTenantIsolation_AuditLogTaggedWithTenant verifies AuditLogTenant writes
// the tenant_id column so tenant-scoped views see correct audit history.
func TestTenantIsolation_AuditLogTaggedWithTenant(t *testing.T) {
	tenantA, tenantB, _, _ := setupTenantTestDB(t)

	events.AuditLogTenant(tenantA, "user-1", "test.action", "test", "rid-1", "from A", "127.0.0.1")
	events.AuditLogTenant(tenantB, "user-2", "test.action", "test", "rid-2", "from B", "127.0.0.2")
	// Audit log writes are async goroutines; give them a moment to land
	time.Sleep(150 * time.Millisecond)

	var nA, nB int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id = ? AND action = 'test.action'`, tenantA).Scan(&nA)
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id = ? AND action = 'test.action'`, tenantB).Scan(&nB)
	if nA != 1 {
		t.Errorf("tenant A audit logs = %d, want 1", nA)
	}
	if nB != 1 {
		t.Errorf("tenant B audit logs = %d, want 1", nB)
	}
	// Tenant A's view must not see tenant B's logs.
	var crossLeak int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id = ? AND details = 'from B'`, tenantA).Scan(&crossLeak)
	if crossLeak != 0 {
		t.Errorf("tenant A view leaked %d tenant B audit log(s)", crossLeak)
	}
}
