package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// AgentPatchEntry is one available-update record posted to the server.
// kb_id is the Microsoft KB number on Windows (e.g. "KB5034441") or the
// package name on unix-like systems. Source identifies the package
// manager so the server can template the right install command later.
type AgentPatchEntry struct {
	KBID        string `json:"kb_id"`
	Source      string `json:"source"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Severity    string `json:"severity,omitempty"`
	CVE         string `json:"cve,omitempty"`
}

const (
	// PatchSyncInterval is the cadence for available-update enumeration.
	// Slower than heartbeat (cheap), faster than inventory (cheaper to
	// query than full software list).
	PatchSyncInterval = 1 * time.Hour
	// patchCommandTimeout caps the per-collector exec runtime. apt
	// list --upgradable should finish in <2s; we allow more headroom.
	patchCommandTimeout = 30 * time.Second
)

// patchSyncLoop runs collectAvailablePatches once at startup (after a
// short settling delay) then on a ticker.
func (a *Agent) patchSyncLoop() {
	time.Sleep(90 * time.Second)
	a.collectAndPostPatches()

	ticker := time.NewTicker(PatchSyncInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !a.registered || a.deviceID == "" {
			continue
		}
		a.collectAndPostPatches()
	}
}

func (a *Agent) collectAndPostPatches() {
	if a.deviceID == "" {
		return
	}
	patches := collectAvailablePatches()
	body := map[string]interface{}{"patches": patches}
	data, err := json.Marshal(body)
	if err != nil {
		slog.Warn("patch sync marshal failed", "error", err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, a.serverURL+"/agent/patches/sync/"+a.deviceID, bytes.NewBuffer(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiToken)
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		slog.Warn("patch sync post failed", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("patch sync rejected", "status", resp.StatusCode)
		return
	}
	slog.Info("patches synced", "count", len(patches))
}

// runWithTimeout shells out and aborts after patchCommandTimeout. On
// timeout we log debug and return empty stdout — callers continue.
func runWithTimeout(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), patchCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("%s timed out", name)
	}
	return out, err
}

// hasCommand returns true if `name` resolves on PATH. Lets us skip
// collectors for package managers that aren't installed instead of
// shelling out and parsing the resulting error.
func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// reportPatchError logs at debug level. Per-collector failures are
// expected (no apt on Fedora etc.); we only escalate at the loop level.
func reportPatchError(source string, err error) {
	slog.Debug("patch collector skipped", "source", source, "error", fmt.Sprint(err))
}

// trimEmpty drops empty lines so collectors don't have to repeat the
// check.
func trimEmpty(in []string) []string {
	out := in[:0]
	for _, l := range in {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
