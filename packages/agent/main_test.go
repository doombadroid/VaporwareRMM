package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAgentRunRouteAbsent is the Codex #1 attack-path regression: a
// POST to /agent/run with no Authorization header must NOT execute a
// shell command. The exploit Codex confirmed was an unauthenticated
// POST against a committed pre-auth binary returning `id`'s output.
// After this commit the route is not registered at all, so an
// unauthenticated POST returns the mux's 404 default. A future
// contributor who re-registers it under authMiddleware would change
// the response to 401 — both 404 and 401 are acceptable; 200 is the
// only failure. We assert that explicitly.
func TestAgentRunRouteAbsent(t *testing.T) {
	a := &Agent{port: 47991, apiToken: "test-token"}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", a.authMiddleware(a.handleMetrics))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := bytes.NewBufferString(`{"type":"shell","command":"id"}`)
	resp, err := http.Post(srv.URL+"/agent/run", "application/json", body)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("/agent/run unauthenticated POST returned 200 — Codex #1 regressed")
	}
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 404 or 401, got %d", resp.StatusCode)
	}
}

// TestAgentMetricsStillBearerGated is the verification step from the
// Codex #1 scope: every other route on the agent's listener stays
// bearer-gated. /metrics is the only remaining route after the run
// path was deleted.
func TestAgentMetricsStillBearerGated(t *testing.T) {
	a := &Agent{port: 47991, apiToken: "test-token"}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", a.authMiddleware(a.handleMetrics))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 unauthenticated, got %d", resp.StatusCode)
	}
}

func TestGenerateToken(t *testing.T) {
	token1 := generateToken()
	token2 := generateToken()

	if token1 == "" {
		t.Error("generateToken returned empty string")
	}
	if token1 == token2 {
		t.Error("generateToken should produce unique tokens")
	}
	if !strings.HasPrefix(token1, "vapr_") {
		t.Errorf("token should start with 'vapr_': %s", token1)
	}
}

func TestGetCPUName(t *testing.T) {
	// Test with empty slice
	name := getCPUName(nil)
	if name != "Unknown" {
		t.Errorf("expected 'Unknown' for nil input, got %q", name)
	}
}
