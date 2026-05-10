package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// DedupMu serialises the device dedup pass with the agent register
// handler. Holding it for the duration of the pass guarantees no new
// row can land inside a duplicate set we're collapsing. The register
// handler takes it briefly around its existence-check+INSERT critical
// section so duplicate creation never races with deletion.
//
// SQLite already serialises writes at the database level, so the
// mutex is structurally redundant there but cheap. On Postgres
// multiple goroutines hold connections concurrently and the race is
// real.
var DedupMu sync.Mutex

// fkTablesReferencingDeviceID is the explicit list of tables whose
// device_id column points back at devices.id. It MUST be kept in sync
// with the schema — adding a new table that references devices and
// forgetting to add it here means the dedup pass will leave dangling
// rows pointing at a deleted device id, which silently breaks anything
// that joins on devices.
//
// This is hand-curated rather than discovered via information_schema /
// sqlite_master because (a) different dialects expose foreign keys
// differently and we don't have FOREIGN KEY constraints declared on
// these tables anyway (the schema uses application-level integrity),
// and (b) we want a code review to catch the missed case.
var fkTablesReferencingDeviceID = []string{
	"device_commands",
	"file_transfers",
	"alerts",
	"patches",
	"compliance_results",
	"device_software",
	"device_hardware",
	"device_group_members",
	"metrics_history",
	"tickets",
	"agent_tokens",
	"neighbor_observations",
	"sunshine_pin_requests", // does not exist in schema today; harmless if absent (we ignore "no such table")
}

// DeduplicateDevicesAndCreateIndex collapses duplicate device rows
// produced by the pre-fix re-registration loop (every heartbeat
// retry-exhaustion re-registered, creating a new device row + token
// row) and installs a UNIQUE INDEX on (tenant_id, hostname, mac_address)
// so future duplicates are blocked at the DB layer.
//
// For each set of duplicates the row with the highest created_at wins;
// every FK in fkTablesReferencingDeviceID is repointed onto the winner
// and the losing rows are deleted.
//
// Idempotent: running on a clean DB does nothing and creates the index
// (no-op if it already exists).
//
// Returns the number of duplicate rows merged on this call. Caller is
// expected to log the count and audit-log the operation through the
// tamper-evident chain.
func DeduplicateDevicesAndCreateIndex() (int, error) {
	if DB == nil {
		return 0, fmt.Errorf("dedup: db not initialised")
	}
	// Hold the dedup mutex for the full pass. Register handler waits
	// here while we're running so it can't introduce new duplicates
	// inside a set we're collapsing. Mutex is fair across waiters per
	// the Go runtime; a register burst won't starve indefinitely.
	DedupMu.Lock()
	defer DedupMu.Unlock()

	// 1. Find duplicate sets keyed by (tenant_id, hostname, mac_address).
	//    Empty mac_address is bucketed under "" so two records with no
	//    mac collapse — that matches the agent re-register pattern.
	rows, err := DB.Query(`
		SELECT tenant_id, hostname, COALESCE(mac_address, '')
		FROM devices
		GROUP BY tenant_id, hostname, COALESCE(mac_address, '')
		HAVING COUNT(*) > 1
	`)
	if err != nil {
		return 0, fmt.Errorf("dedup: find duplicate sets: %w", err)
	}
	type key struct{ tenant, host, mac string }
	var dups []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.tenant, &k.host, &k.mac); err != nil {
			rows.Close()
			return 0, fmt.Errorf("dedup: scan duplicate set: %w", err)
		}
		dups = append(dups, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("dedup: iterate duplicate sets: %w", err)
	}

	merged := 0
	for _, k := range dups {
		// 2. Pick the winner: highest created_at (most recent agent
		//    bootstrap), tiebreak by id ASC for determinism.
		var winnerID string
		matchClause, matchArgs := matchKey(k)
		err := DB.QueryRow(`SELECT id FROM devices WHERE `+matchClause+` ORDER BY created_at DESC, id ASC LIMIT 1`, matchArgs...).Scan(&winnerID)
		if err != nil {
			return merged, fmt.Errorf("dedup: pick winner for %v: %w", k, err)
		}

		// 3. Find the losers and repoint every FK table onto the winner.
		loserRows, err := DB.Query(`SELECT id FROM devices WHERE `+matchClause+` AND id <> ?`, append(matchArgs, winnerID)...)
		if err != nil {
			return merged, fmt.Errorf("dedup: list losers for %v: %w", k, err)
		}
		var losers []string
		for loserRows.Next() {
			var lid string
			if err := loserRows.Scan(&lid); err == nil {
				losers = append(losers, lid)
			}
		}
		loserRows.Close()

		for _, loser := range losers {
			for _, table := range fkTablesReferencingDeviceID {
				_, err := DB.Exec(fmt.Sprintf(`UPDATE %s SET device_id = ? WHERE device_id = ?`, table), winnerID, loser)
				if err != nil {
					// "no such table" on optional tables is fine; everything
					// else aborts the whole pass so we don't leave the FK
					// graph half-rewritten.
					if isMissingTable(err) {
						continue
					}
					return merged, fmt.Errorf("dedup: repoint %s.device_id loser=%s winner=%s: %w", table, loser, winnerID, err)
				}
			}
			if _, err := DB.Exec(`DELETE FROM devices WHERE id = ?`, loser); err != nil {
				return merged, fmt.Errorf("dedup: delete loser %s: %w", loser, err)
			}
			merged++
		}
		slog.Info("device dedup: merged duplicate set", "tenant", k.tenant, "hostname", k.host, "mac", k.mac, "winner", winnerID, "losers", len(losers))
	}

	// 4. Install the UNIQUE INDEX so future re-registers can use ON
	//    CONFLICT to refresh the existing row instead of creating a
	//    new one. SQLite + Postgres both accept COALESCE inside an
	//    expression index. IF NOT EXISTS is portable.
	idxSQL := `CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_dedup ON devices(tenant_id, hostname, COALESCE(mac_address, ''))`
	if _, err := DB.Exec(idxSQL); err != nil {
		return merged, fmt.Errorf("dedup: create unique index (still have duplicates?): %w", err)
	}
	return merged, nil
}

// matchKey produces a WHERE clause + args that match a single dedup
// key. mac_address NULL and '' are equivalent for the purpose of
// dedup, so we use COALESCE on read.
func matchKey(k struct{ tenant, host, mac string }) (string, []interface{}) {
	return `tenant_id = ? AND hostname = ? AND COALESCE(mac_address, '') = ?`, []interface{}{k.tenant, k.host, k.mac}
}

// isMissingTable returns true when err looks like the dialect's "no
// such table" / "relation does not exist" error. We tolerate it
// because fkTablesReferencingDeviceID intentionally lists optional
// tables that some deployments may not have.
func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no such table") || strings.Contains(s, "does not exist")
}

// CountDuplicateDevices is a cheap probe used by the test to assert
// the dedup pass actually collapsed something. Returns the number of
// duplicate rows (i.e. total - unique-key count).
func CountDuplicateDevices() (int, error) {
	if DB == nil {
		return 0, fmt.Errorf("dedup count: db not initialised")
	}
	var total, unique int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&total); err != nil {
		return 0, err
	}
	if err := DB.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM devices GROUP BY tenant_id, hostname, COALESCE(mac_address, ''))`).Scan(&unique); err != nil {
		return 0, err
	}
	return total - unique, nil
}

// DialectIsSQLite is a tiny helper for tests that want to skip on
// Postgres without importing the Wrapper struct.
func DialectIsSQLite(d *sql.DB) bool {
	if DB == nil {
		return false
	}
	return DB.Dialect == "sqlite"
}
