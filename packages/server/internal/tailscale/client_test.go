package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTailscale spins up an httptest.Server that mimics Tailscale's
// API for the endpoints the client touches. Tests drive it by
// installing per-route handlers via the routes map.
func fakeTailscale(t *testing.T, routes map[string]http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		if h, ok := routes[key]; ok {
			h(w, r)
			return
		}
		// Wildcard fallback: routes can register METHOD path-prefix
		// matches with a trailing /*.
		for k, h := range routes {
			if strings.HasSuffix(k, "/*") &&
				strings.HasPrefix(key, strings.TrimSuffix(k, "/*")) {
				h(w, r)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClient("test-client-id", "test-client-secret").WithBaseURL(srv.URL)
	return srv, c
}

func tokenResponse() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"expires_in":   1800,
			"scope":        "auth_keys,devices",
		})
	}
}

func TestClient_AuthenticationSuccess(t *testing.T) {
	_, c := fakeTailscale(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": tokenResponse(),
	})
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if c.token == "" {
		t.Fatal("token cache empty after successful auth")
	}
}

func TestClient_AuthenticationFailure(t *testing.T) {
	_, c := fakeTailscale(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		},
	})
	err := c.Authenticate(context.Background())
	if !errors.Is(err, ErrTailscaleAuthFailed) {
		t.Errorf("expected ErrTailscaleAuthFailed, got %v", err)
	}
}

func TestClient_AuthenticationUnreachable(t *testing.T) {
	c := NewClient("id", "secret").WithBaseURL("http://127.0.0.1:1") // closed port
	err := c.Authenticate(context.Background())
	if !errors.Is(err, ErrTailscaleUnreachable) {
		t.Errorf("expected ErrTailscaleUnreachable, got %v", err)
	}
}

func TestClient_ValidateAuthKeyScope_OK(t *testing.T) {
	_, c := fakeTailscale(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": tokenResponse(),
		"POST /api/v2/tailnet/acme.ts.net/keys": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "k1", "key": "tskey-..."})
		},
	})
	if err := c.ValidateAuthKeyScope(context.Background(), "acme.ts.net"); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestClient_ValidateAuthKeyScope_403(t *testing.T) {
	_, c := fakeTailscale(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": tokenResponse(),
		"POST /api/v2/tailnet/acme.ts.net/keys": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("missing scope auth_keys"))
		},
	})
	err := c.ValidateAuthKeyScope(context.Background(), "acme.ts.net")
	if !errors.Is(err, ErrTailscaleScopeMissingAuthKeys) {
		t.Errorf("expected ErrTailscaleScopeMissingAuthKeys, got %v", err)
	}
}

func TestClient_ValidateDeviceListScope_403(t *testing.T) {
	_, c := fakeTailscale(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": tokenResponse(),
		"GET /api/v2/tailnet/acme.ts.net/devices": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
	})
	err := c.ValidateDeviceListScope(context.Background(), "acme.ts.net")
	if !errors.Is(err, ErrTailscaleScopeMissingDeviceList) {
		t.Errorf("expected ErrTailscaleScopeMissingDeviceList, got %v", err)
	}
}

func TestClient_RateLimited(t *testing.T) {
	_, c := fakeTailscale(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": tokenResponse(),
		"GET /api/v2/tailnet/acme.ts.net/devices": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	})
	err := c.ValidateDeviceListScope(context.Background(), "acme.ts.net")
	if !errors.Is(err, ErrTailscaleRateLimited) {
		t.Errorf("expected ErrTailscaleRateLimited, got %v", err)
	}
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatal("error should unwrap to *RateLimitedError")
	}
	if rl.RetryAfterSeconds != 60 {
		t.Errorf("RetryAfterSeconds: got %d want 60", rl.RetryAfterSeconds)
	}
}

func TestClient_ListTailnets(t *testing.T) {
	_, c := fakeTailscale(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": tokenResponse(),
		"GET /api/v2/tailnet/-/": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"name":         "acme-corp.ts.net",
				"organization": "Acme Corp",
			})
		},
	})
	tn, err := c.ListTailnets(context.Background())
	if err != nil {
		t.Fatalf("ListTailnets: %v", err)
	}
	if len(tn) != 1 {
		t.Fatalf("expected 1 tailnet, got %d", len(tn))
	}
	if tn[0].Name != "acme-corp.ts.net" || tn[0].DisplayName != "Acme Corp" {
		t.Errorf("got %+v", tn[0])
	}
}

func TestClient_ListDevices(t *testing.T) {
	_, c := fakeTailscale(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": tokenResponse(),
		"GET /api/v2/tailnet/acme.ts.net/devices": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"devices": []map[string]interface{}{
					{"name": "host1", "hostname": "host1", "addresses": []string{"100.64.0.1"}, "os": "linux", "tags": []string{"tag:tenant-default"}, "lastSeen": "2026-05-12T00:00:00Z"},
				},
			})
		},
	})
	devs, err := c.ListDevices(context.Background(), "acme.ts.net")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devs) != 1 || devs[0].Name != "host1" {
		t.Errorf("got %+v", devs)
	}
}

// TestClient_MintAuthKey_StubReturnsExpectedShape exercises the
// Phase-2 surface against a mocked Tailscale. Wiring to a handler
// is Phase 2 work; the call path itself is exercised now so the
// shape contract is locked.
func TestClient_MintAuthKey_StubReturnsExpectedShape(t *testing.T) {
	_, c := fakeTailscale(t, map[string]http.HandlerFunc{
		"POST /api/v2/oauth/token": tokenResponse(),
		"POST /api/v2/tailnet/acme.ts.net/keys": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      "k-minted",
				"key":     "tskey-auth-minted-XXXXX",
				"created": "2026-05-12T00:00:00Z",
				"expires": "2026-05-12T00:05:00Z",
			})
		},
	})
	k, err := c.MintAuthKey(context.Background(), MintAuthKeyOptions{
		Tailnet:       "acme.ts.net",
		Tags:          []string{"tag:tenant-default"},
		Preauthorized: true,
		ExpirySeconds: 300,
		Description:   "phase2 install",
	})
	if err != nil {
		t.Fatalf("MintAuthKey: %v", err)
	}
	if k.ID != "k-minted" || k.Key != "tskey-auth-minted-XXXXX" {
		t.Errorf("got %+v", k)
	}
}
