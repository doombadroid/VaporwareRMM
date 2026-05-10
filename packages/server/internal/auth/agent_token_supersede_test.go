package auth

import (
	"fmt"
	"os"
	"testing"
	"time"

	"vaporrmm/models"
	"vaporrmm/server/internal/db"
)

func resetTokenCache() {
	TokenMu.Lock()
	RegisteredTokens = make(map[string]*models.AgentToken)
	TokenMu.Unlock()
}

// TestAgentTokenRowCountBoundedAcrossReRegisters is the third-pass
// guard: re-register the same (tenant, device, hostname) tuple 100
// times and assert the supersede mark is set on every old row. Before
// the supersede fix, this loop grew the table to 100 active rows;
// with supersede only one row stays "active" at a time. The pruner
// sweeps the superseded rows past their grace window.
func TestAgentTokenRowCountBoundedAcrossReRegisters(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/token_supersede.db")
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

	const N = 100
	const tenantID = "default"
	const deviceID = "device-supersede-1"
	const hostname = "host-supersede-1"

	for i := 0; i < N; i++ {
		tok := fmt.Sprintf("agent-token-supersede-%d", i)
		RegisterAgentToken(tok, deviceID, hostname, tenantID)
	}

	// Exactly one non-superseded row for this device.
	var active int
	if err := db.DB.QueryRow(
		`SELECT COUNT(*) FROM agent_tokens WHERE device_id = ? AND (superseded_at IS NULL OR superseded_at = 0)`,
		deviceID,
	).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 1 {
		t.Errorf("expected exactly 1 active token for device, got %d", active)
	}

	// In-memory cache: at least N-1 entries flagged superseded so
	// AuthMiddleware rejects them as soon as the grace window elapses.
	var supersededCache int
	TokenMu.RLock()
	for _, tok := range RegisteredTokens {
		if tok.DeviceID == deviceID && tok.SupersededAt > 0 {
			supersededCache++
		}
	}
	TokenMu.RUnlock()
	if supersededCache < N-1 {
		t.Errorf("expected at least %d superseded entries in cache, got %d", N-1, supersededCache)
	}
}

// TestAgentTokenSupersedeWindowHonoursInflight asserts that inside the
// supersede grace window the OLD token still validates. The window is
// what prevents an in-flight heartbeat from 401-flapping a healthy
// agent during a re-registration; without it, the moment a new
// register lands the old token would be dead.
func TestAgentTokenSupersedeWindowHonoursInflight(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/token_window.db")
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

	RegisterAgentToken("old-token", "dev-window", "host-window", "default")
	oldHash := HashToken("old-token")

	RegisterAgentToken("new-token", "dev-window", "host-window", "default")

	TokenMu.RLock()
	oldTok, ok := RegisteredTokens[oldHash]
	TokenMu.RUnlock()
	if !ok {
		t.Fatal("old token absent from cache after rotation; supersede should have kept it for the grace window")
	}
	if oldTok.SupersededAt == 0 {
		t.Fatal("old token has SupersededAt=0 after rotation; should be in grace window")
	}
	if oldTok.SupersededAt <= time.Now().Unix() {
		t.Fatalf("supersede TTL already past: %d <= %d", oldTok.SupersededAt, time.Now().Unix())
	}
}
