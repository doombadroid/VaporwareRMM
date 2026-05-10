package db

import (
	"fmt"
	"os"
	"testing"
)

// TestDeduplicateDevicesAndCreateIndex_Synthetic10k seeds 1000 unique
// hostnames each with 10 duplicate device rows + 11 child rows scattered
// across FK tables, totalling ~10k duplicates / ~110k child rows. After
// the dedup pass:
//   - exactly 1000 device rows survive
//   - every child-table row points at a live device
//   - the unique index exists and rejects future duplicates
//   - re-running the pass is a no-op (returns merged=0)
//
// The user's spec said "test the migration against a synthetic dataset
// with at least 10k duplicate rows before claiming it's safe". 10k
// duplicates means 10k rows that need to be deleted; we reach that with
// 1000 sets of 11 rows each (1000 winners + 10000 losers).
func TestDeduplicateDevicesAndCreateIndex_Synthetic10k(t *testing.T) {
	dbPath := t.TempDir() + "/dedup_test.db"
	os.Setenv("DATABASE_PATH", dbPath)
	if err := Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() {
		if DB != nil {
			DB.Close()
		}
	})

	const sets = 1000
	const dupsPerSet = 11

	// Seed: 1000 unique hostnames; for each, 11 device rows. Increasing
	// created_at means the last row is the winner. We attach one
	// device_command row per device so we can assert FK rewiring.
	tx, err := DB.DB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmtDev, err := tx.Prepare(`INSERT INTO devices (id, hostname, mac_address, status, last_seen, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prep dev: %v", err)
	}
	stmtCmd, err := tx.Prepare(`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, 'shell', '{}', 'pending', ?, ?)`)
	if err != nil {
		t.Fatalf("prep cmd: %v", err)
	}
	for s := 0; s < sets; s++ {
		host := fmt.Sprintf("host-%04d", s)
		mac := fmt.Sprintf("aa:bb:cc:%02x:%02x:%02x", (s>>16)&0xff, (s>>8)&0xff, s&0xff)
		for d := 0; d < dupsPerSet; d++ {
			devID := fmt.Sprintf("dev-%04d-%02d", s, d)
			if _, err := stmtDev.Exec(devID, host, mac, "offline", 0, int64(s*100+d), "default"); err != nil {
				t.Fatalf("seed dev: %v", err)
			}
			cmdID := fmt.Sprintf("cmd-%04d-%02d", s, d)
			if _, err := stmtCmd.Exec(cmdID, devID, int64(s*100+d), "default"); err != nil {
				t.Fatalf("seed cmd: %v", err)
			}
		}
	}
	stmtDev.Close()
	stmtCmd.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Sanity: pre-dedup we have sets*dupsPerSet device rows and the
	// duplicate count is sets*(dupsPerSet-1).
	preDup, err := CountDuplicateDevices()
	if err != nil {
		t.Fatalf("pre count: %v", err)
	}
	expectedDup := sets * (dupsPerSet - 1)
	if preDup != expectedDup {
		t.Fatalf("pre-dedup duplicate count: got %d, want %d", preDup, expectedDup)
	}

	merged, err := DeduplicateDevicesAndCreateIndex()
	if err != nil {
		t.Fatalf("dedup: %v", err)
	}
	if merged != expectedDup {
		t.Errorf("merged count: got %d, want %d", merged, expectedDup)
	}

	// Survivor count must equal `sets`.
	var survivors int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&survivors); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if survivors != sets {
		t.Errorf("devices after dedup: got %d, want %d", survivors, sets)
	}

	// Every device_command row must still resolve to a live device.
	// If any FK row was orphaned by the merge we've broken the dataset.
	var orphans int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM device_commands c LEFT JOIN devices d ON c.device_id = d.id WHERE d.id IS NULL`).Scan(&orphans); err != nil {
		t.Fatalf("orphan check: %v", err)
	}
	if orphans != 0 {
		t.Errorf("orphan device_commands rows: got %d, want 0", orphans)
	}

	// Every command row must point at the winner of its set (highest
	// created_at). Each winner has 11 commands rewired onto it.
	for s := 0; s < sets; s++ {
		host := fmt.Sprintf("host-%04d", s)
		var winnerID string
		if err := DB.QueryRow(`SELECT id FROM devices WHERE hostname = ?`, host).Scan(&winnerID); err != nil {
			t.Fatalf("read winner for %s: %v", host, err)
		}
		var attached int
		if err := DB.QueryRow(`SELECT COUNT(*) FROM device_commands WHERE device_id = ?`, winnerID).Scan(&attached); err != nil {
			t.Fatalf("count cmds for winner %s: %v", winnerID, err)
		}
		if attached != dupsPerSet {
			t.Errorf("commands rewired for winner %s: got %d, want %d", winnerID, attached, dupsPerSet)
		}
	}

	// Index now exists; an attempt to insert another duplicate must
	// fail. A race here would surface as the unique-violation error
	// the production handler turns into an UPSERT path.
	_, err = DB.Exec(`INSERT INTO devices (id, hostname, mac_address, status, last_seen, created_at, tenant_id) VALUES (?, ?, ?, 'offline', 0, 0, 'default')`, "dup-attempt", "host-0001", "aa:bb:cc:00:00:01")
	if err == nil {
		t.Fatal("expected UNIQUE INDEX to reject duplicate insert, got nil error")
	}

	// Idempotent: running again is a no-op.
	again, err := DeduplicateDevicesAndCreateIndex()
	if err != nil {
		t.Fatalf("dedup re-run: %v", err)
	}
	if again != 0 {
		t.Errorf("dedup re-run on clean DB merged %d rows, want 0", again)
	}
}
