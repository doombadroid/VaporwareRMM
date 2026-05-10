package events

import (
	"os"
	"testing"

	"vaporrmm/server/internal/db"
)

// audit_chain_test verifies the four properties named in the audit:
//   1. Chain intact after a series of legitimate writes.
//   2. Chain breaks at the first row when a row's content is edited.
//   3. Chain breaks at the row after a deleted middle row.
//   4. Chain stays intact across a server restart (close + reopen DB)
//      — this is the case that usually breaks integrity-chain
//      implementations because in-memory chain state is lost. We rely
//      on loadLastAuditSignature reading the chain head from disk.

func setupTestDB(t *testing.T) {
	t.Helper()
	// Engine-agnostic. SQLite path: per-test tempdir DB. Postgres path:
	// reuse the shared DATABASE_URL connection but TRUNCATE every table
	// so cross-test state doesn't bleed (Postgres in CI has one shared
	// database for the whole package run).
	if os.Getenv("DATABASE_URL") == "" {
		dbPath := t.TempDir() + "/audit_chain_test.db"
		os.Setenv("DATABASE_PATH", dbPath)
	}
	os.Setenv("SECRETS_ENCRYPTION_KEY", "fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY=")
	if err := db.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	if err := db.ResetForTests(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	t.Cleanup(func() {
		if db.DB != nil && os.Getenv("DATABASE_URL") == "" {
			db.DB.Close()
		}
	})
}

// writeRow synchronously writes one audit row. We use the *Sync helper
// so the test can rely on the row being committed when the call
// returns, instead of racing AuditLogTenant's background goroutine.
func writeRow(t *testing.T, action, details string) {
	t.Helper()
	AuditLogTenantSync("default", "system", action, "audit_test", "rid-1", details, "127.0.0.1")
}

// verifyDefaultTenant runs VerifyAuditChain scoped to "default" and
// returns the single result. Tests are all single-tenant.
func verifyDefaultTenant(t *testing.T) AuditChainVerifyResult {
	t.Helper()
	res, err := VerifyAuditChain("default")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected exactly one tenant result, got %d", len(res))
	}
	return res[0]
}

func TestAuditChain_IntactAfterLegitimateWrites(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 5; i++ {
		writeRow(t, "test.action", "row")
	}
	res := verifyDefaultTenant(t)
	if !res.OK {
		t.Fatalf("expected OK chain, got reason=%q first_bad=%s", res.Reason, res.FirstBadID)
	}
	if res.Verified < 5 {
		t.Fatalf("expected at least 5 rows verified, got %d", res.Verified)
	}
}

func TestAuditChain_BreaksOnRowEdit(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 4; i++ {
		writeRow(t, "test.edit", "before")
	}
	// Pick a middle row and rewrite its details column. The signature
	// over the row no longer matches; the verifier should report this
	// row as the first bad one.
	var victimID string
	if err := db.DB.QueryRow(`SELECT id FROM audit_logs WHERE tenant_id = 'default' ORDER BY chain_seq ASC LIMIT 1 OFFSET 2`).Scan(&victimID); err != nil {
		t.Fatalf("pick victim: %v", err)
	}
	if _, err := db.DB.Exec(`UPDATE audit_logs SET details = ? WHERE id = ?`, "TAMPERED", victimID); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	res := verifyDefaultTenant(t)
	if res.OK {
		t.Fatal("expected verifier to flag the tampered row, got OK")
	}
	if res.FirstBadID != victimID {
		t.Fatalf("expected first_bad=%s, got %s", victimID, res.FirstBadID)
	}
	if res.Reason != "signature mismatch" {
		t.Fatalf("expected reason=signature mismatch, got %q", res.Reason)
	}
}

func TestAuditChain_BreaksOnRowDelete(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 4; i++ {
		writeRow(t, "test.delete", "row")
	}
	// Capture row ids in chain order. The deleted row's *successor* is
	// the one that breaks: its "previous signature" no longer exists in
	// the chain (we deleted it), so its recomputed signature uses the
	// new previous head and won't match what was stored.
	rows, err := db.DB.Query(`SELECT id FROM audit_logs WHERE tenant_id = 'default' ORDER BY chain_seq ASC`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) < 3 {
		t.Fatalf("need at least 3 rows for delete test, got %d", len(ids))
	}

	deleted := ids[1]
	successor := ids[2]
	if _, err := db.DB.Exec(`DELETE FROM audit_logs WHERE id = ?`, deleted); err != nil {
		t.Fatalf("delete: %v", err)
	}

	res := verifyDefaultTenant(t)
	if res.OK {
		t.Fatal("expected verifier to flag the orphaned successor, got OK")
	}
	if res.FirstBadID != successor {
		t.Fatalf("expected first_bad=%s (successor of deleted row), got %s", successor, res.FirstBadID)
	}
}

func TestAuditChain_IntactAcrossDBRestart(t *testing.T) {
	// Fresh DB, write rows, close DB, reopen DB, write more rows, verify.
	// The interesting case is whether the post-restart writes correctly
	// pick up the on-disk chain head rather than starting fresh from
	// "" — that bug would silently produce a chain that the verifier
	// flags at the first post-restart row.
	//
	// This is the regression case the audit calls out: "the restart
	// case is where these implementations usually break".
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/restart_test.db")
	}
	os.Setenv("SECRETS_ENCRYPTION_KEY", "fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY=")

	if err := db.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	if err := db.ResetForTests(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	for i := 0; i < 3; i++ {
		writeRow(t, "test.before_restart", "row")
	}

	// Close and reopen — same backing database. On SQLite that's the
	// same on-disk file; on Postgres it's a fresh connection to the
	// same DB. Either way the chain head must be re-derivable from
	// disk by loadLastAuditState.
	if err := db.DB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := db.Init(); err != nil {
		t.Fatalf("db re-init: %v", err)
	}
	t.Cleanup(func() {
		if db.DB != nil && os.Getenv("DATABASE_URL") == "" {
			db.DB.Close()
		}
	})

	for i := 0; i < 3; i++ {
		writeRow(t, "test.after_restart", "row")
	}

	res := verifyDefaultTenant(t)
	if !res.OK {
		t.Fatalf("expected OK chain across restart, got reason=%q first_bad=%s (verified %d/%d)", res.Reason, res.FirstBadID, res.Verified, res.Total)
	}
	if res.Verified < 6 {
		t.Fatalf("expected at least 6 rows verified, got %d", res.Verified)
	}
}

// TestAuditChain_BackfillIsIdempotent is a fifth sanity test: the
// startup backfill must not rewrite signatures of rows that were
// already chained correctly. If it did, an attacker could insert a
// tampered row plus a fresh signature and rely on a server restart to
// "ratify" the tampering. The backfill explicitly trusts existing
// signatures and only fills empty ones.
func TestAuditChain_BackfillIsIdempotent(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 3; i++ {
		writeRow(t, "test.idempotent", "row")
	}

	// Snapshot signatures.
	rows, err := db.DB.Query(`SELECT id, signature FROM audit_logs ORDER BY created_at ASC, id ASC`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	before := map[string]string{}
	for rows.Next() {
		var id, sig string
		if err := rows.Scan(&id, &sig); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		before[id] = sig
	}
	rows.Close()

	// Run backfill twice — should be a no-op.
	if err := BackfillAuditChain(); err != nil {
		t.Fatalf("backfill 1: %v", err)
	}
	if err := BackfillAuditChain(); err != nil {
		t.Fatalf("backfill 2: %v", err)
	}

	rows, err = db.DB.Query(`SELECT id, signature FROM audit_logs ORDER BY created_at ASC, id ASC`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	after := map[string]string{}
	for rows.Next() {
		var id, sig string
		if err := rows.Scan(&id, &sig); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		after[id] = sig
	}
	rows.Close()

	for id, sig := range before {
		if after[id] != sig {
			t.Errorf("backfill rewrote signature for row %s (before=%s after=%s)", id, sig, after[id])
		}
	}
}
