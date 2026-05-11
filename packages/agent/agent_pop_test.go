package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// agent_pop_test.go covers the Codex #6 (agent-side) contract:
//
//  - TestAgent_PresentsPersistedToken: the agent persists its
//    bearer to disk and re-presents it in the
//    X-Existing-Agent-Token header on every subsequent register.
//  - TestAgent_HandlesRotationAck: VAPOR_ROTATE_TOKEN=1 makes the
//    agent generate a fresh bearer, present the persisted one as
//    PoP, and on a 200 response persist the NEW token so the next
//    restart presents the new one.

// withAgentStateFile redirects agentStateFile() at the OS level by
// putting the temp directory ahead of /etc/vaporrmm. The agent's
// agentStateFile() reads APPDATA on Windows and hardcodes
// /etc/vaporrmm/agent-state.json elsewhere. For test isolation we
// rely on the fact that os.WriteFile / os.ReadFile happily accept
// any path we hand them; we monkey-patch agentStateFile via a Go
// build-time test by overriding via env-derived path is not
// available here, so we write directly through saveAgentState
// which calls agentStateFile internally. To redirect cleanly, the
// agent state file path uses an env-overridable hook.

// agentStateFile is overridden by an env var when the test sets
// VAPOR_AGENT_STATE_FILE_OVERRIDE — see agentStateFileForTest below.

func agentStateFileForTest(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "agent-state.json")
}

func TestAgent_PresentsPersistedToken(t *testing.T) {
	statePath := agentStateFileForTest(t)

	// Pre-seed the persisted state: token "persisted-bearer-xyz"
	// belongs to this agent from a prior run.
	seed := agentState{
		DeviceID: "device-pop-agent-1",
		Hostname: "agent-host-1",
		APIToken: "persisted-bearer-xyz-1234567890",
	}
	body, _ := json.Marshal(seed)
	if err := os.WriteFile(statePath, body, 0600); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	t.Setenv("VAPOR_AGENT_STATE_FILE_OVERRIDE", statePath)
	t.Setenv("VAPOR_ROTATE_TOKEN", "")

	var mu sync.Mutex
	var gotAuth, gotPoP string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotPoP = r.Header.Get("X-Existing-Agent-Token")
		mu.Unlock()
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_id":"device-pop-agent-1","status":"refreshed"}`))
	}))
	defer srv.Close()

	agent, err := NewAgent(srv.URL, 47991, "")
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	if agent.apiToken != seed.APIToken {
		t.Errorf("expected agent to load persisted token, got %q", agent.apiToken)
	}

	if err := agent.registerWithServer(); err != nil {
		t.Fatalf("registerWithServer: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	wantAuth := "Bearer " + seed.APIToken
	if gotAuth != wantAuth {
		t.Errorf("Authorization header: want %q, got %q", wantAuth, gotAuth)
	}
	if gotPoP != seed.APIToken {
		t.Errorf("X-Existing-Agent-Token: want %q, got %q", seed.APIToken, gotPoP)
	}
}

func TestAgent_HandlesRotationAck(t *testing.T) {
	statePath := agentStateFileForTest(t)
	seed := agentState{
		DeviceID: "device-pop-agent-2",
		Hostname: "agent-host-2",
		APIToken: "old-bearer-abc-1234567890abcdef",
	}
	body, _ := json.Marshal(seed)
	if err := os.WriteFile(statePath, body, 0600); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	t.Setenv("VAPOR_AGENT_STATE_FILE_OVERRIDE", statePath)
	t.Setenv("VAPOR_ROTATE_TOKEN", "1")

	var mu sync.Mutex
	var gotAuth, gotPoP string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotPoP = r.Header.Get("X-Existing-Agent-Token")
		mu.Unlock()
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_id":"device-pop-agent-2","status":"refreshed"}`))
	}))
	defer srv.Close()

	agent, err := NewAgent(srv.URL, 47991, "")
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	if err := agent.registerWithServer(); err != nil {
		t.Fatalf("registerWithServer (rotation): %v", err)
	}

	mu.Lock()
	// Authorization carries the NEW bearer; PoP carries the OLD.
	if gotAuth == "Bearer "+seed.APIToken {
		t.Errorf("Authorization should carry new token on rotation; got old %q", gotAuth)
	}
	if gotPoP != seed.APIToken {
		t.Errorf("X-Existing-Agent-Token should carry persisted old token: want %q, got %q", seed.APIToken, gotPoP)
	}
	newToken := agent.apiToken
	if newToken == seed.APIToken {
		t.Errorf("rotation did not change agent.apiToken")
	}
	mu.Unlock()

	// Persist check: state file now holds the new token, so a
	// hypothetical restart would present it.
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var persisted agentState
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode state file: %v", err)
	}
	if persisted.APIToken != newToken {
		t.Errorf("state file token=%q want=%q", persisted.APIToken, newToken)
	}

	// Simulate restart by constructing a fresh agent reading the
	// just-written state. It should now present the new token in
	// both headers (rotation env still set; we clear it to simulate
	// a normal restart).
	t.Setenv("VAPOR_ROTATE_TOKEN", "")
	mu.Lock()
	gotAuth, gotPoP = "", ""
	mu.Unlock()

	a2, err := NewAgent(srv.URL, 47991, "")
	if err != nil {
		t.Fatalf("NewAgent restart: %v", err)
	}
	if a2.apiToken != newToken {
		t.Errorf("restarted agent did not load new persisted token: got %q want %q", a2.apiToken, newToken)
	}
	if err := a2.registerWithServer(); err != nil {
		t.Fatalf("registerWithServer restart: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer "+newToken {
		t.Errorf("restart Authorization: want Bearer %q, got %q", newToken, gotAuth)
	}
	if gotPoP != newToken {
		t.Errorf("restart PoP header: want %q, got %q", newToken, gotPoP)
	}
}

// TestAgent_RotationFailsAtomicallyOnPersistError covers the Codex
// #6 P2 fix at packages/agent/main.go:558: the server has accepted
// the new bearer, but saveAgentState fails (e.g., disk-full,
// unwritable state path). The agent must NOT update its in-memory
// bearer; otherwise the process runs with a token only the server
// knows about, and the next restart boots with the stale persisted
// token and gets PoP-rejected on re-register, lockout follows.
//
// We force the persist failure by pointing
// VAPOR_AGENT_STATE_FILE_OVERRIDE at a path inside a read-only
// directory so MkdirAll succeeds (idempotent) but the temp-file
// create / rename fails.
func TestAgent_RotationFailsAtomicallyOnPersistError(t *testing.T) {
	// Seed a valid state file at a different path so NewAgent can
	// read its persisted token; we'll then redirect the SAVE path to
	// a read-only directory.
	seedPath := filepath.Join(t.TempDir(), "agent-state.json")
	seed := agentState{
		DeviceID: "device-pop-persist-fail",
		Hostname: "agent-host-persist-fail",
		APIToken: "persisted-bearer-stays-this-1234567890",
	}
	body, _ := json.Marshal(seed)
	if err := os.WriteFile(seedPath, body, 0600); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	t.Setenv("VAPOR_AGENT_STATE_FILE_OVERRIDE", seedPath)
	t.Setenv("VAPOR_ROTATE_TOKEN", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_id":"device-pop-persist-fail","status":"refreshed"}`))
	}))
	defer srv.Close()

	agent, err := NewAgent(srv.URL, 47991, "")
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if agent.apiToken != seed.APIToken {
		t.Fatalf("NewAgent should load persisted token, got %q", agent.apiToken)
	}

	// Redirect the save path to a read-only directory so the rename
	// inside saveAgentState fails. We keep the read path the same so
	// the agent already holds its persisted state in memory.
	roDir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(roDir, 0500); err != nil {
		t.Fatalf("mkdir readonly: %v", err)
	}
	t.Cleanup(func() {
		// Restore permissions so t.TempDir cleanup can rm -r the dir.
		_ = os.Chmod(roDir, 0700)
	})
	t.Setenv("VAPOR_AGENT_STATE_FILE_OVERRIDE", filepath.Join(roDir, "agent-state.json"))

	err = agent.registerWithServer()
	if err == nil {
		t.Fatal("expected registerWithServer to error when persist fails, got nil")
	}
	if !strings.Contains(err.Error(), "persist") {
		t.Errorf("error should mention persist failure, got: %v", err)
	}

	// In-memory token MUST still be the persisted one — the
	// rotation was rolled back because the new token wasn't durable.
	if agent.apiToken != seed.APIToken {
		t.Errorf("in-memory token mutated despite persist failure: got %q want %q", agent.apiToken, seed.APIToken)
	}

	// And the original state file is intact.
	raw, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read seed state file: %v", err)
	}
	var persisted agentState
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode state file: %v", err)
	}
	if persisted.APIToken != seed.APIToken {
		t.Errorf("seed state file overwritten: got %q want %q", persisted.APIToken, seed.APIToken)
	}
}

// TestAgent_DiscardsPersistedStateOnIdentityMismatch covers the
// Codex #6 P2 fix at packages/agent/main.go:146: the golden-image
// case where one installed agent gets cloned to many VMs. The
// persisted token is bound to (hostname, MAC) at save time; if
// either has changed when the agent next boots, the token is
// discarded and the clone re-registers fresh.
//
// We can't change the live os.Hostname() / MAC at test time, so we
// seed the state file with deliberately-wrong fingerprint values
// and assert NewAgent ignores the persisted token+device_id.
func TestAgent_DiscardsPersistedStateOnIdentityMismatch(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	seed := agentState{
		DeviceID:          "device-clone-source",
		Hostname:          "clone-source-host",
		APIToken:          "golden-image-bearer-1234567890abcd",
		PersistedHostname: "different-host-than-current",
		PersistedMAC:      "ff:ff:ff:ff:ff:ff",
	}
	body, _ := json.Marshal(seed)
	if err := os.WriteFile(statePath, body, 0600); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	t.Setenv("VAPOR_AGENT_STATE_FILE_OVERRIDE", statePath)
	t.Setenv("VAPOR_ROTATE_TOKEN", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_id":"device-clone-source","status":"ok"}`))
	}))
	defer srv.Close()

	agent, err := NewAgent(srv.URL, 47991, "")
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// The persisted token MUST be discarded because the fingerprint
	// doesn't match this host. The agent generated a fresh token
	// instead, so apiToken should be non-empty and != the seed.
	if agent.apiToken == seed.APIToken {
		t.Errorf("agent kept the cloned-image bearer; expected discard. got %q", agent.apiToken)
	}
	if agent.apiToken == "" {
		t.Errorf("agent ended up with no token at all; should have generated a fresh one")
	}
	// device_id should also be discarded — a cloned image must
	// re-register fresh, not inherit the source's identity row.
	if agent.deviceID == seed.DeviceID {
		t.Errorf("agent inherited cloned-image device_id %q; expected fresh registration", agent.deviceID)
	}
}
