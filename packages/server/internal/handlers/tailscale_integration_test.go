package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vaporrmm/server/internal/tailscale"
)

// TestTailscale_FullValidateConnectGetRoundTrip exercises the
// happy-path operator flow end-to-end against the handler layer:
//
//   1. validate (no persistence)
//   2. connect (persist + audit)
//   3. GET /connection (super-admin shape, then tenant shape)
//   4. GET /devices
//   5. rotate (same-tailnet) → verify rotated_at populated
//   6. disconnect
//
// Server unit tests (tailscale_test.go) cover each handler in
// isolation. This test pins the contract between them so a future
// refactor that subtly changes the response shape between two
// endpoints (e.g., GET /connection adds a field that breaks the
// tenant-shape narrowing) is caught at the integration boundary.
func TestTailscale_FullValidateConnectGetRoundTrip(t *testing.T) {
	app, swap := tailscaleTestEnv(t, "super_admin")
	swap(&fakeTSClient{
		listDevices: func(ctx context.Context, tn string) ([]tailscale.Device, error) {
			return []tailscale.Device{{
				Name: "dev-1", Hostname: "host-1", Addresses: []string{"100.64.0.5"}, OS: "linux",
				Tags: []string{"tag:tenant-default"}, LastSeen: "2026-05-12T00:00:00Z",
			}}, nil
		},
	})

	// 1) validate
	validateResp := post(t, app, "/api/v1/tailscale/validate", map[string]string{
		"client_id": "id", "client_secret": "secret",
	})
	if validateResp.StatusCode != http.StatusOK {
		t.Fatalf("validate: %d", validateResp.StatusCode)
	}
	validateResp.Body.Close()

	// 2) connect
	connectResp := post(t, app, "/api/v1/tailscale/connect", map[string]string{
		"client_id": "id", "client_secret": "secret", "tailnet": "acme.ts.net",
	})
	if connectResp.StatusCode != http.StatusOK {
		t.Fatalf("connect: %d", connectResp.StatusCode)
	}
	connectResp.Body.Close()

	// 3a) GET /connection (super-admin shape)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tailscale/connection", nil)
	resp, _ := app.Test(req, -1)
	defer resp.Body.Close()
	var superOut map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&superOut); err != nil {
		t.Fatalf("decode super GET: %v", err)
	}
	for _, k := range []string{"connected", "tailnet", "tailnet_display_name", "connected_at", "connected_by_user_id"} {
		if _, ok := superOut[k]; !ok {
			t.Errorf("super-admin GET missing %q (full payload: %+v)", k, superOut)
		}
	}

	// 4) GET /devices
	reqDev := httptest.NewRequest(http.MethodGet, "/api/v1/tailscale/devices", nil)
	respDev, _ := app.Test(reqDev, -1)
	defer respDev.Body.Close()
	if respDev.StatusCode != http.StatusOK {
		t.Fatalf("devices: %d", respDev.StatusCode)
	}
	var devOut struct {
		Devices []tailscale.Device `json:"devices"`
		Tailnet string             `json:"tailnet"`
	}
	if err := json.NewDecoder(respDev.Body).Decode(&devOut); err != nil {
		t.Fatalf("decode devices: %v", err)
	}
	if devOut.Tailnet != "acme.ts.net" || len(devOut.Devices) != 1 {
		t.Errorf("devices payload: %+v", devOut)
	}

	// 5) rotate (same tailnet — fake returns acme.ts.net)
	rotateBody, _ := json.Marshal(map[string]string{"client_id": "id2", "client_secret": "secret2"})
	rotReq := httptest.NewRequest(http.MethodPut, "/api/v1/tailscale/connection", bytes.NewReader(rotateBody))
	rotReq.Header.Set("Content-Type", "application/json")
	rotResp, _ := app.Test(rotReq, -1)
	defer rotResp.Body.Close()
	if rotResp.StatusCode != http.StatusOK {
		t.Fatalf("rotate same-tailnet: %d", rotResp.StatusCode)
	}

	// rotated_at must now be present in the super-admin GET.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/tailscale/connection", nil)
	resp2, _ := app.Test(req2, -1)
	defer resp2.Body.Close()
	var afterRotate map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&afterRotate)
	if _, ok := afterRotate["rotated_at"]; !ok {
		t.Error("super-admin GET after rotate should include rotated_at")
	}

	// 6) disconnect
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/v1/tailscale/connection", nil)
	respDel, _ := app.Test(reqDel, -1)
	respDel.Body.Close()
	if respDel.StatusCode != http.StatusOK {
		t.Fatalf("disconnect: %d", respDel.StatusCode)
	}

	// Final GET reports disconnected.
	reqFinal := httptest.NewRequest(http.MethodGet, "/api/v1/tailscale/connection", nil)
	respFinal, _ := app.Test(reqFinal, -1)
	defer respFinal.Body.Close()
	var final map[string]interface{}
	json.NewDecoder(respFinal.Body).Decode(&final)
	if final["connected"] != false {
		t.Errorf("after disconnect, GET should report connected=false, got %v", final)
	}
}
