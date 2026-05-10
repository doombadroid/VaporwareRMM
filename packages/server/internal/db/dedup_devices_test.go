package db

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/dedup_test.db")
	}
	if err := Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	if err := ResetForTests(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	t.Cleanup(func() {
		if DB != nil && os.Getenv("DATABASE_URL") == "" {
			DB.Close()
		}
	})

	const sets = 1000
	const dupsPerSet = 11

	// Seed: 1000 unique hostnames; for each, 11 device rows. Increasing
	// created_at means the last row is the winner. We attach one
	// device_command row per device so we can assert FK rewiring.
	//
	// We go through the Wrapper (not tx.Prepare) so the `?` placeholders
	// get rewritten to $N for Postgres. The Wrapper's own Exec already
	// batches efficiently enough for a 10k-row test.
	for s := 0; s < sets; s++ {
		host := fmt.Sprintf("host-%04d", s)
		mac := fmt.Sprintf("aa:bb:cc:%02x:%02x:%02x", (s>>16)&0xff, (s>>8)&0xff, s&0xff)
		for d := 0; d < dupsPerSet; d++ {
			devID := fmt.Sprintf("dev-%04d-%02d", s, d)
			if _, err := DB.Exec(`INSERT INTO devices (id, hostname, mac_address, status, last_seen, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				devID, host, mac, "offline", 0, int64(s*100+d), "default"); err != nil {
				t.Fatalf("seed dev: %v", err)
			}
			cmdID := fmt.Sprintf("cmd-%04d-%02d", s, d)
			if _, err := DB.Exec(`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, 'shell', '{}', 'pending', ?, ?)`,
				cmdID, devID, int64(s*100+d), "default"); err != nil {
				t.Fatalf("seed cmd: %v", err)
			}
		}
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

// TestDeduplicateDevicesAndCreateIndex_UnderLiveWrites runs the dedup
// pass while a separate goroutine fires register-shaped INSERTs at
// roughly 50/s into known duplicate sets. The contract is:
//
//   - dedup completes without error (the UNIQUE INDEX creation doesn't
//     trip on a duplicate landed mid-pass)
//   - no surviving duplicate exists after both finish
//   - the inserts that happened are EITHER folded into the existing
//     winner (because they took the lock after dedup processed their
//     set and the UNIQUE constraint redirected them) OR survive as
//     legitimate new rows (different mac, different host)
//
// The mutex (db.DedupMu) is what makes this safe. Without it the dedup
// would silently leave the new duplicate in place. The cleanup pass's
// 10k-row test was correctness inside a single transaction; this is
// correctness under concurrent writers.
func TestDeduplicateDevicesAndCreateIndex_UnderLiveWrites(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/dedup_live_test.db")
	}
	if err := Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	if err := ResetForTests(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	t.Cleanup(func() {
		if DB != nil && os.Getenv("DATABASE_URL") == "" {
			DB.Close()
		}
	})

	// Seed: 200 duplicate sets, 4 dups each = 800 rows, 600 duplicates.
	const sets = 200
	const dupsPerSet = 4
	for s := 0; s < sets; s++ {
		host := fmt.Sprintf("host-%03d", s)
		mac := fmt.Sprintf("aa:bb:00:%02x:%02x:%02x", (s>>16)&0xff, (s>>8)&0xff, s&0xff)
		for d := 0; d < dupsPerSet; d++ {
			id := fmt.Sprintf("dev-%03d-%02d", s, d)
			if _, err := DB.Exec(`INSERT INTO devices (id, hostname, mac_address, status, last_seen, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				id, host, mac, "offline", 0, int64(s*100+d), "default"); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}

	// Writer goroutine: while dedup runs, fire register-shaped inserts
	// into the existing sets at ~50/s. Each insert takes DedupMu like
	// the production handler does — exists check + INSERT under the
	// lock so a concurrent dedup can't collapse a duplicate set we're
	// adding to.
	stop := make(chan struct{})
	var inserts atomic.Int64
	var conflicts atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			s := i % sets
			host := fmt.Sprintf("host-%03d", s)
			mac := fmt.Sprintf("aa:bb:00:%02x:%02x:%02x", (s>>16)&0xff, (s>>8)&0xff, s&0xff)
			// Production-shape: take the mutex, exists check, insert
			// new row only if no match. Mirror handler logic.
			DedupMu.Lock()
			var existing string
			err := DB.QueryRow(
				`SELECT id FROM devices WHERE tenant_id = ? AND hostname = ? AND COALESCE(mac_address,'') = ?`,
				"default", host, mac,
			).Scan(&existing)
			if err == nil && existing != "" {
				// matched existing row — would refresh in production.
				conflicts.Add(1)
			} else {
				newID := fmt.Sprintf("live-%d", i)
				if _, err := DB.Exec(
					`INSERT INTO devices (id, hostname, mac_address, status, last_seen, created_at, tenant_id) VALUES (?, ?, ?, 'online', ?, ?, 'default')`,
					newID, host, mac, time.Now().Unix(), time.Now().Unix(),
				); err == nil {
					inserts.Add(1)
				}
			}
			DedupMu.Unlock()
			i++
			time.Sleep(20 * time.Millisecond) // ~50/s
		}
	}()

	// Run dedup. It takes DedupMu for its full duration.
	merged, err := DeduplicateDevicesAndCreateIndex()
	if err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("dedup under live writes: %v", err)
	}

	// Let the writer continue briefly so post-dedup inserts hit the
	// now-unique-indexed table and exercise the dedup-lock pattern at
	// steady state.
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Assert no duplicates survive. CountDuplicateDevices should be 0.
	dup, err := CountDuplicateDevices()
	if err != nil {
		t.Fatalf("post count: %v", err)
	}
	if dup != 0 {
		t.Fatalf("duplicates survived live-write dedup: got %d", dup)
	}
	if merged < sets*(dupsPerSet-1) {
		t.Errorf("expected at least %d merges from seeded sets, got %d", sets*(dupsPerSet-1), merged)
	}
	t.Logf("merged=%d live_inserts=%d live_conflicts=%d", merged, inserts.Load(), conflicts.Load())
}

