package events

import (
	"sync"
	"testing"

	"vaporrmm/server/internal/db"
)

// TestAuditCompaction_ChainValidBeforeRetention is the control case:
// write some rows, do not compact, verifier returns OK. Sanity check
// that the per-tenant chain rewrite didn't break the happy path.
func TestAuditCompaction_ChainValidBeforeRetention(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 6; i++ {
		writeRow(t, "test.pre_retention", "row")
	}
	res := verifyDefaultTenant(t)
	if !res.OK {
		t.Fatalf("pre-compaction verify failed: %s @ %s", res.Reason, res.FirstBadID)
	}
	if res.Verified != 6 {
		t.Fatalf("expected 6 verified, got %d", res.Verified)
	}
}

// TestAuditCompaction_ChainValidAfterRetentionFires writes a long
// chain, fires a retention compaction across the first half, and
// verifies the chain still validates end-to-end. The bridge is the
// compaction record's stored end_sig — without it the verifier would
// see a "signature mismatch" on the first surviving live row.
func TestAuditCompaction_ChainValidAfterRetentionFires(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 10; i++ {
		writeRow(t, "test.before_retention", "row")
	}

	// Capture the cutoff: created_at strictly less than this deletes
	// the first 5 rows. Because writeRow uses auditNow() (Unix
	// seconds) and the test runs sub-second, every row likely has the
	// same created_at; advance auditNow temporarily for the latter
	// half so cutoff is meaningful.
	var fifthTS int64
	if err := db.DB.QueryRow(`SELECT created_at FROM audit_logs WHERE tenant_id = 'default' ORDER BY chain_seq ASC LIMIT 1 OFFSET 4`).Scan(&fifthTS); err != nil {
		t.Fatalf("read 5th row ts: %v", err)
	}
	// Bump auditNow so subsequent rows have a strictly greater
	// created_at, giving us a usable cutoff value.
	origNow := auditNow
	defer func() { auditNow = origNow }()
	auditNow = func() int64 { return fifthTS + 1 }
	for i := 0; i < 5; i++ {
		writeRow(t, "test.kept", "row")
	}

	cutoff := fifthTS + 1 // strictly-less: deletes the first 5 (the ones at fifthTS)

	deleted, crID, err := CompactAuditChainForTenant("default", cutoff)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if deleted < 1 {
		t.Fatalf("expected at least 1 row deleted, got %d", deleted)
	}
	if crID == "" {
		t.Fatal("expected a compaction record id, got empty")
	}

	res := verifyDefaultTenant(t)
	if !res.OK {
		t.Fatalf("post-compaction verify failed: %s @ %s (verified %d/%d)", res.Reason, res.FirstBadID, res.Verified, res.Total)
	}
	if res.Verified < 1 {
		t.Fatalf("expected at least one row verified, got %d", res.Verified)
	}

	// The compaction record itself must be in the chain and must have
	// the right action. We refuse to ship the verifier endpoint until
	// this property holds.
	var actCount int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id = 'default' AND action = ?`, AuditCompactionAction).Scan(&actCount); err != nil {
		t.Fatalf("count CR: %v", err)
	}
	if actCount != 1 {
		t.Errorf("expected exactly one compaction record, got %d", actCount)
	}
}

// TestAuditCompaction_ChainInvalidIfCompactionRecordEdited proves the
// compaction record is itself tamper-evident: editing its details
// (where end_sig lives) breaks the chain at that row.
func TestAuditCompaction_ChainInvalidIfCompactionRecordEdited(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 6; i++ {
		writeRow(t, "test.before", "row")
	}
	var fifthTS int64
	if err := db.DB.QueryRow(`SELECT created_at FROM audit_logs WHERE tenant_id = 'default' ORDER BY chain_seq ASC LIMIT 1 OFFSET 4`).Scan(&fifthTS); err != nil {
		t.Fatalf("read 5th: %v", err)
	}
	origNow := auditNow
	defer func() { auditNow = origNow }()
	auditNow = func() int64 { return fifthTS + 1 }
	for i := 0; i < 4; i++ {
		writeRow(t, "test.kept", "row")
	}

	_, crID, err := CompactAuditChainForTenant("default", fifthTS+1)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}

	if res := verifyDefaultTenant(t); !res.OK {
		t.Fatalf("baseline verify failed: %s @ %s", res.Reason, res.FirstBadID)
	}

	// Tamper: rewrite the compaction record's details. We're forging
	// what end_sig it claims to bridge to.
	if _, err := db.DB.Exec(`UPDATE audit_logs SET details = ? WHERE id = ?`, `{"first_deleted_id":"x","last_deleted_id":"y","count":99,"end_sig":"forged"}`, crID); err != nil {
		t.Fatalf("tamper CR: %v", err)
	}

	res := verifyDefaultTenant(t)
	if res.OK {
		t.Fatal("expected verifier to flag tampered compaction record, got OK")
	}
	if res.FirstBadID != crID {
		t.Fatalf("expected first_bad=%s (the CR), got %s", crID, res.FirstBadID)
	}
}

// TestAuditCompaction_ChainInvalidIfRowsDeletedWithoutCompactionRecord
// confirms that a naive DELETE (no CR inserted) still breaks the chain.
// The compaction path is the ONLY sanctioned way to remove rows; any
// other delete must trip the verifier so operators see it.
func TestAuditCompaction_ChainInvalidIfRowsDeletedWithoutCompactionRecord(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 6; i++ {
		writeRow(t, "test.naive_del", "row")
	}
	// Delete a middle row without inserting a compaction record.
	var victimID string
	if err := db.DB.QueryRow(`SELECT id FROM audit_logs WHERE tenant_id = 'default' ORDER BY chain_seq ASC LIMIT 1 OFFSET 2`).Scan(&victimID); err != nil {
		t.Fatalf("pick victim: %v", err)
	}
	if _, err := db.DB.Exec(`DELETE FROM audit_logs WHERE id = ?`, victimID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	res := verifyDefaultTenant(t)
	if res.OK {
		t.Fatal("expected verifier to flag a naive delete, got OK")
	}
	if res.Reason != "signature mismatch" {
		t.Errorf("expected reason=signature mismatch, got %q", res.Reason)
	}
}

// TestAuditCompaction_UnderConcurrentWriters fires CompactAuditChainForTenant
// while a separate goroutine writes new audit rows at ~200/s. The contract
// is: every row that completes its INSERT before compaction finishes
// belongs to the chain and is either deleted (with its sig bridged by the
// CR) or survives. No row's INSERT can land "inside" the compacted range
// because both paths take auditChainMu.
//
// This is the load-shaped variant the audit demanded — "tested under live
// writes" not "tested in a single transaction".
func TestAuditCompaction_UnderConcurrentWriters(t *testing.T) {
	setupTestDB(t)
	// Seed older rows so there's something to compact.
	origNow := auditNow
	defer func() { auditNow = origNow }()
	const oldTS = int64(1_000_000)
	auditNow = func() int64 { return oldTS }
	for i := 0; i < 50; i++ {
		writeRow(t, "test.old", "row")
	}
	// Advance time so subsequent rows land after the cutoff.
	auditNow = func() int64 { return oldTS + 100 }

	// Start writer.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				writeRow(t, "test.writer", "row")
				i++
			}
		}
	}()

	// Hit compaction repeatedly while the writer is going.
	for i := 0; i < 3; i++ {
		_, _, err := CompactAuditChainForTenant("default", oldTS+1)
		if err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("compact %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	// Chain still verifies.
	res := verifyDefaultTenant(t)
	if !res.OK {
		t.Fatalf("post-concurrent-compact verify failed: %s @ %s (verified %d/%d)", res.Reason, res.FirstBadID, res.Verified, res.Total)
	}
}
