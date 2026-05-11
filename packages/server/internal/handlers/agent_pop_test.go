package handlers

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vaporrmm/models"
	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
)

// permissiveOutboundClient returns an http.Client without the
// SSRF guard — used only by the webhook rate-limit tests, which
// need to reach an httptest.Server bound to 127.0.0.1.
func permissiveOutboundClient(d time.Duration) *http.Client {
	return &http.Client{
		Timeout:   d,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
	}
}

// popTestEnv wires a Fiber app with only the agent-registration
// route plus a clean DB for each test. Registration secret is set
// to "test-reg" so the auth precondition is satisfied without
// touching tenants table state.
func popTestEnv(t *testing.T) *fiber.App {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/agent_pop.db")
	}
	os.Setenv("SECRETS_ENCRYPTION_KEY", "fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY=")
	os.Setenv("REGISTRATION_SECRET", "test-reg")
	os.Unsetenv("VAPOR_REFUSE_LEGACY_BYPASS_AFTER")
	auth.JWTSecret = "agent-pop-test-jwt-secret-needs-to-be-long-enough"
	if err := db.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	if err := db.ResetForTests(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	// Reset rate-limit state between tests; the registration route
	// has a 10/min per-IP gate and the table tests blow past it.
	auth.ResetRateLimitStoreForTests()
	t.Cleanup(func() {
		// Don't close db.DB — async audit/webhook goroutines from
		// this test may still be in flight when the cleanup runs.
		// The next test's popTestEnv overwrites DB anyway; the leak
		// is bounded to one connection per test.
		auth.TokenMu.Lock()
		auth.RegisteredTokens = make(map[string]*models.AgentToken)
		auth.TokenMu.Unlock()
		events.ResetRegistrationConflictWebhookBucketsForTests()
		auth.ResetRateLimitStoreForTests()
	})

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	RegisterAgentRoutes(app, Config{})
	return app
}

func registerOnce(t *testing.T, app *fiber.App, bearer, existing, hostname, mac string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"hostname":      hostname,
		"mac_address":   mac,
		"os":            "linux",
		"os_version":    "test",
		"local_ip":      "10.0.0.1",
		"cpu":           "test-cpu",
		"agent_version": "test-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	if existing != "" {
		req.Header.Set("X-Existing-Agent-Token", existing)
	}
	req.Header.Set("X-Registration-Secret", "test-reg")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func deviceIDFor(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode register body: %v body=%s", err, string(raw))
	}
	id, _ := out["device_id"].(string)
	if id == "" {
		t.Fatalf("no device_id in response: %s", string(raw))
	}
	return id
}

// TestReRegister_RequiresPoP: a second register with the same
// hostname+MAC but NO X-Existing-Agent-Token header returns 409.
func TestReRegister_RequiresPoP(t *testing.T) {
	app := popTestEnv(t)

	t1 := strings.Repeat("a", 40)
	resp1 := registerOnce(t, app, t1, "", "host-pop-1", "aa:bb:cc:dd:ee:01")
	if resp1.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp1.Body)
		t.Fatalf("first register expected 200, got %d body=%s", resp1.StatusCode, string(body))
	}
	deviceIDFor(t, resp1)

	t2 := strings.Repeat("b", 40)
	resp2 := registerOnce(t, app, t2, "", "host-pop-1", "aa:bb:cc:dd:ee:01")
	if resp2.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("second register without PoP expected 409, got %d body=%s", resp2.StatusCode, string(body))
	}
	resp2.Body.Close()
}

// TestReRegister_WrongTokenReturns409: header carrying a token
// that doesn't match current or previous returns 409 and writes an
// audit row with verdict=rejected.
func TestReRegister_WrongTokenReturns409(t *testing.T) {
	app := popTestEnv(t)

	t1 := strings.Repeat("a", 40)
	resp1 := registerOnce(t, app, t1, "", "host-pop-2", "aa:bb:cc:dd:ee:02")
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first register expected 200, got %d", resp1.StatusCode)
	}
	resp1.Body.Close()

	t2 := strings.Repeat("b", 40)
	wrong := strings.Repeat("z", 40)
	resp2 := registerOnce(t, app, t2, wrong, "host-pop-2", "aa:bb:cc:dd:ee:02")
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("wrong-PoP register expected 409, got %d", resp2.StatusCode)
	}
	resp2.Body.Close()

	// Give async audit log goroutine a moment.
	time.Sleep(100 * time.Millisecond)
	var n int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'device.register.pop_rejected'`).Scan(&n); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 device.register.pop_rejected audit row, got %d", n)
	}
}

// TestReRegister_CurrentTokenAccepted: re-register presenting the
// current token in X-Existing-Agent-Token succeeds.
func TestReRegister_CurrentTokenAccepted(t *testing.T) {
	app := popTestEnv(t)
	tok := strings.Repeat("a", 40)
	resp1 := registerOnce(t, app, tok, "", "host-pop-3", "aa:bb:cc:dd:ee:03")
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first register expected 200, got %d", resp1.StatusCode)
	}
	resp1.Body.Close()

	// Same token in both bearer and PoP header — no rotation but
	// PoP succeeds via the current-row hash match.
	resp2 := registerOnce(t, app, tok, tok, "host-pop-3", "aa:bb:cc:dd:ee:03")
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("re-register with current token expected 200, got %d body=%s", resp2.StatusCode, string(body))
	}
	resp2.Body.Close()
}

// TestReRegister_PreviousTokenWithinGraceWindow: register T1, rotate
// to T2, then re-register with T1 as PoP. Outcome should be 200,
// audit tagged grace_window_used_rotation_ack (we touch
// devices.last_seen post-rotation to simulate a heartbeat).
func TestReRegister_PreviousTokenWithinGraceWindow(t *testing.T) {
	app := popTestEnv(t)
	t1 := strings.Repeat("a", 40)
	t2 := strings.Repeat("b", 40)
	t3 := strings.Repeat("c", 40)

	r1 := registerOnce(t, app, t1, "", "host-pop-4", "aa:bb:cc:dd:ee:04")
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first register expected 200, got %d", r1.StatusCode)
	}
	deviceID := deviceIDFor(t, r1)

	// Rotate: T1 -> T2 (PoP with T1, bearer T2). previous_token_hash
	// is now hash(T1).
	r2 := registerOnce(t, app, t2, t1, "host-pop-4", "aa:bb:cc:dd:ee:04")
	if r2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(r2.Body)
		t.Fatalf("rotation expected 200, got %d body=%s", r2.StatusCode, string(body))
	}
	r2.Body.Close()

	// Simulate a heartbeat under T2 — bumps devices.last_seen past
	// previous_token_rotated_at, so the next previous-token PoP is
	// classified rotation_ack (in-flight stale request after a
	// rotation the agent did successfully observe).
	now := time.Now().Unix() + 1
	if _, err := db.DB.Exec(`UPDATE devices SET last_seen = ? WHERE id = ?`, now, deviceID); err != nil {
		t.Fatalf("bump last_seen: %v", err)
	}

	// Re-register with T1 still as PoP and T3 as the new bearer.
	r3 := registerOnce(t, app, t3, t1, "host-pop-4", "aa:bb:cc:dd:ee:04")
	if r3.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(r3.Body)
		t.Fatalf("grace-window register expected 200, got %d body=%s", r3.StatusCode, string(body))
	}
	r3.Body.Close()

	time.Sleep(100 * time.Millisecond)
	var details string
	if err := db.DB.QueryRow(`SELECT details FROM audit_logs WHERE action = 'device.register.pop_grace' ORDER BY id DESC LIMIT 1`).Scan(&details); err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if !strings.Contains(details, "grace_window_used_rotation_ack") {
		t.Errorf("expected rotation_ack tag in audit details, got %q", details)
	}

	// crash_recovery sub-case: rotate again, do NOT bump last_seen,
	// then re-present the previous token. devices.last_seen should
	// still be older than the rotation that just happened, so the
	// verdict comes back crash_recovery.
	t4 := strings.Repeat("d", 40)
	t5 := strings.Repeat("e", 40)
	// Rewind last_seen so we're sure it's BEFORE the rotation
	// we're about to do.
	if _, err := db.DB.Exec(`UPDATE devices SET last_seen = ? WHERE id = ?`, 1, deviceID); err != nil {
		t.Fatalf("rewind last_seen: %v", err)
	}
	rR := registerOnce(t, app, t4, t3, "host-pop-4", "aa:bb:cc:dd:ee:04")
	if rR.StatusCode != http.StatusOK {
		t.Fatalf("crash-recovery rotation expected 200, got %d", rR.StatusCode)
	}
	rR.Body.Close()
	// Force last_seen to pre-rotation again — the rotation Exec
	// above did UPDATE last_seen = now, which would make the next
	// grace-window register look like rotation_ack.
	if _, err := db.DB.Exec(`UPDATE devices SET last_seen = ? WHERE id = ?`, 1, deviceID); err != nil {
		t.Fatalf("rewind last_seen after rotation: %v", err)
	}
	rC := registerOnce(t, app, t5, t3, "host-pop-4", "aa:bb:cc:dd:ee:04")
	if rC.StatusCode != http.StatusOK {
		t.Fatalf("crash-recovery PoP expected 200, got %d", rC.StatusCode)
	}
	rC.Body.Close()

	time.Sleep(100 * time.Millisecond)
	if err := db.DB.QueryRow(`SELECT details FROM audit_logs WHERE action = 'device.register.pop_grace' ORDER BY id DESC LIMIT 1`).Scan(&details); err != nil {
		t.Fatalf("audit lookup crash: %v", err)
	}
	if !strings.Contains(details, "grace_window_used_crash_recovery") {
		t.Errorf("expected crash_recovery tag in audit details, got %q", details)
	}
}

// TestReRegister_PreviousTokenAfterGraceWindow: rewind
// previous_token_rotated_at to >60s ago and assert PoP with the
// previous token is rejected.
func TestReRegister_PreviousTokenAfterGraceWindow(t *testing.T) {
	app := popTestEnv(t)
	t1 := strings.Repeat("a", 40)
	t2 := strings.Repeat("b", 40)

	r1 := registerOnce(t, app, t1, "", "host-pop-5", "aa:bb:cc:dd:ee:05")
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first register expected 200, got %d", r1.StatusCode)
	}
	r1.Body.Close()

	r2 := registerOnce(t, app, t2, t1, "host-pop-5", "aa:bb:cc:dd:ee:05")
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("rotation expected 200, got %d", r2.StatusCode)
	}
	r2.Body.Close()

	// Backdate previous_token_rotated_at past the grace window.
	long := time.Now().Unix() - int64(auth.AgentPoPGraceWindow.Seconds()) - 10
	if _, err := db.DB.Exec(
		`UPDATE agent_tokens SET previous_token_rotated_at = ? WHERE hostname = ? AND previous_token_hash IS NOT NULL`,
		long, "host-pop-5",
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	t3 := strings.Repeat("c", 40)
	r3 := registerOnce(t, app, t3, t1, "host-pop-5", "aa:bb:cc:dd:ee:05")
	if r3.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(r3.Body)
		t.Fatalf("post-window register expected 409, got %d body=%s", r3.StatusCode, string(body))
	}
	r3.Body.Close()
}

// TestReRegister_LegacyBypass: device row exists, all its agent_tokens
// rows have been pruned. First re-register with no PoP header is
// accepted via the legacy bypass; second re-register from same device
// requires PoP.
func TestReRegister_LegacyBypass(t *testing.T) {
	app := popTestEnv(t)
	t1 := strings.Repeat("a", 40)

	r1 := registerOnce(t, app, t1, "", "host-pop-6", "aa:bb:cc:dd:ee:06")
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first register expected 200, got %d", r1.StatusCode)
	}
	deviceID := deviceIDFor(t, r1)

	// Simulate pre-Codex-#6 state: device row exists but every
	// token row for it has been pruned. Clear in-memory cache too so
	// VerifyAgentPoP sees the truly-empty state.
	if _, err := db.DB.Exec(`DELETE FROM agent_tokens WHERE device_id = ?`, deviceID); err != nil {
		t.Fatalf("clear tokens: %v", err)
	}
	auth.TokenMu.Lock()
	auth.RegisteredTokens = make(map[string]*models.AgentToken)
	auth.TokenMu.Unlock()

	t2 := strings.Repeat("b", 40)
	r2 := registerOnce(t, app, t2, "", "host-pop-6", "aa:bb:cc:dd:ee:06")
	if r2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(r2.Body)
		t.Fatalf("legacy-bypass register expected 200, got %d body=%s", r2.StatusCode, string(body))
	}
	r2.Body.Close()

	time.Sleep(100 * time.Millisecond)
	var bypassRows int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'legacy_agent_pop_bypass'`).Scan(&bypassRows); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if bypassRows < 1 {
		t.Errorf("expected legacy_agent_pop_bypass audit row, got %d", bypassRows)
	}

	// Second re-register from same device WITHOUT PoP must 409 —
	// the bypass latch is consumed.
	t3 := strings.Repeat("c", 40)
	r3 := registerOnce(t, app, t3, "", "host-pop-6", "aa:bb:cc:dd:ee:06")
	if r3.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(r3.Body)
		t.Fatalf("second register after bypass expected 409, got %d body=%s", r3.StatusCode, string(body))
	}
	r3.Body.Close()
}

// TestReRegister_LegacyBypassRefusedAfterCutoff: same scenario as
// above but with VAPOR_REFUSE_LEGACY_BYPASS_AFTER set to a past
// timestamp. First re-register without PoP must 409.
func TestReRegister_LegacyBypassRefusedAfterCutoff(t *testing.T) {
	app := popTestEnv(t)
	t1 := strings.Repeat("a", 40)

	r1 := registerOnce(t, app, t1, "", "host-pop-7", "aa:bb:cc:dd:ee:07")
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first register expected 200, got %d", r1.StatusCode)
	}
	deviceID := deviceIDFor(t, r1)

	if _, err := db.DB.Exec(`DELETE FROM agent_tokens WHERE device_id = ?`, deviceID); err != nil {
		t.Fatalf("clear tokens: %v", err)
	}
	auth.TokenMu.Lock()
	auth.RegisteredTokens = make(map[string]*models.AgentToken)
	auth.TokenMu.Unlock()

	os.Setenv("VAPOR_REFUSE_LEGACY_BYPASS_AFTER", time.Now().Add(-1*time.Hour).UTC().Format(time.RFC3339))
	defer os.Unsetenv("VAPOR_REFUSE_LEGACY_BYPASS_AFTER")

	t2 := strings.Repeat("b", 40)
	r2 := registerOnce(t, app, t2, "", "host-pop-7", "aa:bb:cc:dd:ee:07")
	if r2.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(r2.Body)
		t.Fatalf("post-cutoff bypass register expected 409, got %d body=%s", r2.StatusCode, string(body))
	}
	r2.Body.Close()
}

// TestConflictWebhookRateLimit: 10 conflicts on the same device
// within 1 hour fire exactly 1 webhook + 10 audit rows.
func TestConflictWebhookRateLimit(t *testing.T) {
	app := popTestEnv(t)
	events.SetWebhookOutboundForTests(func(string) error { return nil }, permissiveOutboundClient)
	t.Cleanup(func() { events.SetWebhookOutboundForTests(nil, nil) })

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-VaporRMM-Event") == "device.registration_conflict" {
			hits++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	now := time.Now().Unix()
	if _, err := db.DB.Exec(
		`INSERT INTO webhooks (id, url, secret, events, enabled, tenant_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"webhook-pop-2", srv.URL, "", "device.registration_conflict", 1, "default", now,
	); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}

	t1 := strings.Repeat("a", 40)
	r1 := registerOnce(t, app, t1, "", "host-pop-8", "aa:bb:cc:dd:ee:08")
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first register expected 200, got %d", r1.StatusCode)
	}
	r1.Body.Close()

	// The /agent/register handler is rate-limited 10/min per IP.
	// Reset so the 10-conflict burst below doesn't get throttled
	// before all 10 conflict-handler paths execute.
	auth.ResetRateLimitStoreForTests()

	wrong := strings.Repeat("z", 40)
	for i := 0; i < 10; i++ {
		bearer := fmt.Sprintf("%040d", i)
		rr := registerOnce(t, app, bearer, wrong, "host-pop-8", "aa:bb:cc:dd:ee:08")
		if rr.StatusCode != http.StatusConflict {
			t.Fatalf("conflict #%d expected 409, got %d", i, rr.StatusCode)
		}
		rr.Body.Close()
	}

	time.Sleep(300 * time.Millisecond)
	if hits != 1 {
		t.Errorf("expected exactly 1 webhook fire, got %d", hits)
	}
	var auditRows int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'device.register.pop_rejected'`).Scan(&auditRows); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditRows != 10 {
		t.Errorf("expected 10 audit rows, got %d", auditRows)
	}
}

// TestConflictWebhookSecondHour: conflict, advance bucket window
// by 1h, conflict again, assert 2 webhook fires total.
func TestConflictWebhookSecondHour(t *testing.T) {
	app := popTestEnv(t)
	events.SetWebhookOutboundForTests(func(string) error { return nil }, permissiveOutboundClient)
	t.Cleanup(func() { events.SetWebhookOutboundForTests(nil, nil) })

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-VaporRMM-Event") == "device.registration_conflict" {
			hits++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	now := time.Now().Unix()
	if _, err := db.DB.Exec(
		`INSERT INTO webhooks (id, url, secret, events, enabled, tenant_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"webhook-pop-3", srv.URL, "", "device.registration_conflict", 1, "default", now,
	); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}

	t1 := strings.Repeat("a", 40)
	r1 := registerOnce(t, app, t1, "", "host-pop-9", "aa:bb:cc:dd:ee:09")
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first register expected 200, got %d", r1.StatusCode)
	}
	deviceID := deviceIDFor(t, r1)

	wrong := strings.Repeat("z", 40)
	rB := strings.Repeat("b", 40)
	rr := registerOnce(t, app, rB, wrong, "host-pop-9", "aa:bb:cc:dd:ee:09")
	if rr.StatusCode != http.StatusConflict {
		t.Fatalf("first conflict expected 409, got %d", rr.StatusCode)
	}
	rr.Body.Close()
	time.Sleep(200 * time.Millisecond)
	if hits != 1 {
		t.Fatalf("expected 1 webhook after first conflict, got %d", hits)
	}

	// Simulate >1h elapsed.
	events.AdvanceConflictWebhookWindowStartForTests("default", deviceID, 3601)

	rC := strings.Repeat("c", 40)
	rr2 := registerOnce(t, app, rC, wrong, "host-pop-9", "aa:bb:cc:dd:ee:09")
	if rr2.StatusCode != http.StatusConflict {
		t.Fatalf("second-window conflict expected 409, got %d", rr2.StatusCode)
	}
	rr2.Body.Close()
	time.Sleep(200 * time.Millisecond)
	if hits != 2 {
		t.Errorf("expected 2 webhook fires across two windows, got %d", hits)
	}
}
