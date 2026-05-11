package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
