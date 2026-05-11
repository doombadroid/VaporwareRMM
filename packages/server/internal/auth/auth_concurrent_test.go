package auth

import (
	"os"
	"sync"
	"testing"

	"vaporrmm/server/internal/db"
)

// TestRegisterAgentToken_ConcurrentReregisterSerializes is the Codex #6
// followup (item #5) regression: two goroutines re-register the same
// (tenant, device, hostname) tuple simultaneously. Without the
// mutex-wrapped tx, both reads of the prior-hash could see the same
// row and both writes would record the same previous_token_hash —
// losing one agent's true previous state and causing a spurious 409
// inside the 60s PoP grace window. With the fix, the second writer
// observes the first's row as the prior, so the active row's
// previous_token_hash is always the loser's token_hash.
func TestRegisterAgentToken_ConcurrentReregisterSerializes(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/token_concurrent.db")
	}
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
		resetTokenCache()
	})

	const tenantID = "default"
	const deviceID = "device-concurrent-1"
	const hostname = "host-concurrent-1"

	// Seed an initial token so both racers have a real prior to read
	// (they each pick a different new token; one of those becomes the
	// loser's contribution to previous_token_hash).
	RegisterAgentToken("initial-token-seed-1234567890", deviceID, hostname, tenantID)

	tokenA := "concurrent-token-A-abcdefghij1234567890"
	tokenB := "concurrent-token-B-zyxwvutsrq0987654321"
	hashA := HashToken(tokenA)
	hashB := HashToken(tokenB)

	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	go func() {
		defer wg.Done()
		<-start
		RegisterAgentToken(tokenA, deviceID, hostname, tenantID)
	}()
	go func() {
		defer wg.Done()
		<-start
		RegisterAgentToken(tokenB, deviceID, hostname, tenantID)
	}()
	close(start)
	wg.Wait()

	// Exactly one active row for the device.
	var active int
	if err := db.DB.QueryRow(
		`SELECT COUNT(*) FROM agent_tokens WHERE device_id = ? AND (superseded_at IS NULL OR superseded_at = 0)`,
		deviceID,
	).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 1 {
		t.Fatalf("expected exactly 1 active row, got %d", active)
	}

	// Fetch the active row's token_hash and previous_token_hash. The
	// winner is whichever goroutine finished second (the mutex
	// serialized them); the loser's hash is recorded as the active
	// row's previous_token_hash.
	var activeHash, activePrev string
	if err := db.DB.QueryRow(
		`SELECT token_hash, COALESCE(previous_token_hash, '') FROM agent_tokens
		   WHERE device_id = ? AND (superseded_at IS NULL OR superseded_at = 0)`,
		deviceID,
	).Scan(&activeHash, &activePrev); err != nil {
		t.Fatalf("read active row: %v", err)
	}

	if activeHash != hashA && activeHash != hashB {
		t.Fatalf("active token_hash %q is neither A nor B", activeHash)
	}

	wantPrev := hashA
	if activeHash == hashA {
		wantPrev = hashB
	}
	if activePrev != wantPrev {
		t.Errorf("active row previous_token_hash=%q, want loser=%q (active=%q)", activePrev, wantPrev, activeHash)
	}

	// No orphan rows: every row for this device is either the active
	// one or carries a superseded_at > 0.
	var orphan int
	if err := db.DB.QueryRow(
		`SELECT COUNT(*) FROM agent_tokens WHERE device_id = ? AND token_hash <> ? AND (superseded_at IS NULL OR superseded_at = 0)`,
		deviceID, activeHash,
	).Scan(&orphan); err != nil {
		t.Fatalf("count orphan: %v", err)
	}
	if orphan != 0 {
		t.Errorf("expected 0 orphan active rows besides the winner, got %d", orphan)
	}
}
