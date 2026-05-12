package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
)

// TestHeartbeatPersistsKernelMemoryDisk verifies the heartbeat
// handler reads memory, disk_size, and kernel_version from the body
// and writes them to the devices row. Includes a > 2^31 memory case
// to prove the int4 -> bigint widening migration (051) actually
// took effect — without the migration the column would silently
// truncate at 2,147,483,647 on Postgres.
func TestHeartbeatPersistsKernelMemoryDisk(t *testing.T) {
	app := popTestEnv(t)

	const wantMemory = int64(137_438_953_472) // 128 GB — well above int4
	const wantDisk = int64(2_199_023_255_552)  // 2 TB
	const wantKernel = "7.0.3-cachyos-gentoo-dist"

	// First-register so the agent_tokens row exists and the
	// heartbeat handler can resolve a device_id.
	bearer := strings.Repeat("a", 40)
	r1 := registerOnce(t, app, bearer, "", "host-mem-disk-1", "aa:bb:cc:dd:ee:30")
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("register expected 200, got %d", r1.StatusCode)
	}
	var regBody struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r1.Body).Decode(&regBody); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	r1.Body.Close()

	// Heartbeat with the new fields. Numbers go in as float64 over
	// the JSON wire — the handler casts back to int64.
	hbPayload := map[string]interface{}{
		"status":         "online",
		"cpu_usage":      12.5,
		"memory_usage":   34.0,
		"disk_usage":     56.0,
		"memory":         float64(wantMemory),
		"disk_size":      float64(wantDisk),
		"kernel_version": wantKernel,
	}
	hbBody, _ := json.Marshal(hbPayload)
	hb := httptest.NewRequest(http.MethodPost, "/agent/heartbeat", bytes.NewReader(hbBody))
	hb.Header.Set("Content-Type", "application/json")
	hb.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := app.Test(hb, -1)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("heartbeat expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// Read the row back.
	var gotMemory, gotDisk int64
	var gotKernel string
	if err := db.DB.QueryRow(
		`SELECT COALESCE(memory, 0), COALESCE(disk_size, 0), COALESCE(kernel_version, '') FROM devices WHERE id = ?`,
		regBody.DeviceID,
	).Scan(&gotMemory, &gotDisk, &gotKernel); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if gotMemory != wantMemory {
		t.Errorf("memory: got %d, want %d (delta=%d) — int4 truncation would give 0 or negative", gotMemory, wantMemory, gotMemory-wantMemory)
	}
	if gotDisk != wantDisk {
		t.Errorf("disk_size: got %d, want %d", gotDisk, wantDisk)
	}
	if gotKernel != wantKernel {
		t.Errorf("kernel_version: got %q, want %q", gotKernel, wantKernel)
	}
}

// TestHeartbeatDoesNotClobberValuesWithZero verifies the
// COALESCE+NULLIF guard: a heartbeat that omits the new fields (or
// sends 0 / empty string) must NOT wipe out a previously-persisted
// value. Important for the rollout window where some agents have
// shipped the new payload and others have not.
func TestHeartbeatDoesNotClobberValuesWithZero(t *testing.T) {
	app := popTestEnv(t)

	const wantMemory = int64(8_589_934_592) // 8 GB
	const wantDisk = int64(500_000_000_000)  // ~500 GB
	const wantKernel = "5.15.0-ubuntu"

	bearer := strings.Repeat("b", 40)
	r1 := registerOnce(t, app, bearer, "", "host-mem-disk-2", "aa:bb:cc:dd:ee:31")
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("register: %d", r1.StatusCode)
	}
	var regBody struct {
		DeviceID string `json:"device_id"`
	}
	json.NewDecoder(r1.Body).Decode(&regBody)
	r1.Body.Close()

	// First heartbeat lands the values.
	hb1, _ := json.Marshal(map[string]interface{}{
		"status":         "online",
		"memory":         float64(wantMemory),
		"disk_size":      float64(wantDisk),
		"kernel_version": wantKernel,
	})
	req1 := httptest.NewRequest(http.MethodPost, "/agent/heartbeat", bytes.NewReader(hb1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Authorization", "Bearer "+bearer)
	resp1, _ := app.Test(req1, -1)
	resp1.Body.Close()

	// Second heartbeat omits the fields (simulates an older agent).
	hb2, _ := json.Marshal(map[string]interface{}{"status": "online"})
	req2 := httptest.NewRequest(http.MethodPost, "/agent/heartbeat", bytes.NewReader(hb2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+bearer)
	resp2, _ := app.Test(req2, -1)
	resp2.Body.Close()

	var gotMemory, gotDisk int64
	var gotKernel string
	if err := db.DB.QueryRow(
		`SELECT COALESCE(memory, 0), COALESCE(disk_size, 0), COALESCE(kernel_version, '') FROM devices WHERE id = ?`,
		regBody.DeviceID,
	).Scan(&gotMemory, &gotDisk, &gotKernel); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if gotMemory != wantMemory {
		t.Errorf("memory clobbered by zero heartbeat: got %d, want %d", gotMemory, wantMemory)
	}
	if gotDisk != wantDisk {
		t.Errorf("disk_size clobbered by zero heartbeat: got %d, want %d", gotDisk, wantDisk)
	}
	if gotKernel != wantKernel {
		t.Errorf("kernel_version clobbered by empty heartbeat: got %q, want %q", gotKernel, wantKernel)
	}

	// Keep the auth var live so the lint doesn't whine.
	_ = auth.TokenMu
}
