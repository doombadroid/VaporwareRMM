package handlers

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"vaporrmm/server/internal/db"
)

func computeTokenHashForTest(jwt string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(jwt)))
}

// TestOIDCCreateSessionPopulatesAllNotNull is the regression guard for
// the bug that broke SSO entirely on Postgres: the original OIDC
// callback INSERT omitted user_sessions.id (PRIMARY KEY) and last_seen
// (NOT NULL). Postgres rejected the INSERT, the error was swallowed,
// and the cookie was set anyway — so the next request failed
// AuthMiddleware's stateful session check and returned "Session
// revoked".
//
// The test runs createOIDCSession against whichever dialect the
// environment chooses (default SQLite on :memory:; Postgres if
// DATABASE_URL=postgres://...). On Postgres the missing-column case
// fails at INSERT time; on SQLite the constraints are weaker so we
// also explicitly assert every schema NOT NULL column is non-zero.
//
// Any future schema change to user_sessions must update both
// createOIDCSession and this test in the same commit.
func TestOIDCCreateSessionPopulatesAllNotNull(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/oidc_session_test.db")
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

	const (
		fakeJWT = "fake.jwt.value-that-just-needs-to-hash-to-something"
		userID  = "user-test-1"
		ip      = "10.0.0.42"
		ua      = "test-ua/1.0"
	)

	// Seed a user row to satisfy potential FK joins downstream. The
	// session table doesn't actually FK to users on the schema today
	// but a real OIDC callback would have just JIT-provisioned this.
	_, _ = db.DB.Exec(
		`INSERT INTO users (id, email, password_hash, name, role, created_at, tenant_id) VALUES (?, ?, '', ?, 'admin', 0, 'default')`,
		userID, "oidc-test@example.com", "OIDC Test",
	)

	if err := createOIDCSession(fakeJWT, userID, ip, ua); err != nil {
		t.Fatalf("createOIDCSession: %v", err)
	}

	// Assert every NOT NULL column the schema declares is populated.
	// Schema (db.go:1311):
	//   id          TEXT PRIMARY KEY
	//   user_id     TEXT NOT NULL
	//   token_hash  TEXT NOT NULL
	//   created_at  INTEGER NOT NULL
	//   last_seen   INTEGER NOT NULL
	// The two nullable columns (ip_address, user_agent) we still assert
	// non-empty because the production handler always populates them
	// from c.IP() / c.Get("User-Agent") and a regression that drops
	// those would also be a forensics loss.
	var (
		gotID, gotUserID, gotTokenHash, gotIP, gotUA string
		gotCreatedAt, gotLastSeen                    int64
	)
	if err := db.DB.QueryRow(
		`SELECT id, user_id, token_hash, ip_address, user_agent, created_at, last_seen FROM user_sessions WHERE user_id = ?`,
		userID,
	).Scan(&gotID, &gotUserID, &gotTokenHash, &gotIP, &gotUA, &gotCreatedAt, &gotLastSeen); err != nil {
		t.Fatalf("read back session row: %v", err)
	}

	if gotID == "" {
		t.Error("user_sessions.id was empty — this is the original Postgres-breaking bug")
	}
	if gotUserID != userID {
		t.Errorf("user_id = %q, want %q", gotUserID, userID)
	}
	if gotTokenHash == "" {
		t.Error("token_hash was empty")
	}
	if gotIP != ip {
		t.Errorf("ip_address = %q, want %q", gotIP, ip)
	}
	if gotUA != ua {
		t.Errorf("user_agent = %q, want %q", gotUA, ua)
	}
	if gotCreatedAt == 0 {
		t.Error("created_at was zero")
	}
	if gotLastSeen == 0 {
		t.Error("last_seen was zero — this is the original Postgres-breaking bug")
	}

	// Stateful check: AuthMiddleware verifies token_hash exists in
	// user_sessions before honouring a JWT. If the row didn't land,
	// the next request would 401 with "Session revoked". Re-query
	// using the hash that the production middleware would compute.
	expectedHash := computeTokenHashForTest(fakeJWT)
	var found int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM user_sessions WHERE token_hash = ?`, expectedHash).Scan(&found); err != nil {
		t.Fatalf("re-lookup by token hash: %v", err)
	}
	if found != 1 {
		t.Errorf("AuthMiddleware-shape lookup returned %d rows, want 1", found)
	}
}
