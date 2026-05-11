package handlers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	httputilv "vaporrmm/server/internal/httputil"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/gofiber/fiber/v2"
)

// TestOIDC_E2E stands up a fake OIDC provider on httptest.Server,
// configures the server-side OIDC routes to use it, and walks the
// full SSO flow: discovery -> authorize redirect -> token exchange ->
// JWKS signature verification -> claims parsing -> session row
// creation -> /me reuse. It is the test class the cleanup pass
// deferred. Running it requires test-only injection of the OIDC
// outbound client + issuer validator because production refuses
// loopback addresses (correctly); the injection points are
// package-private vars in oidc.go, set only from this _test.go.
//
// Runs on the SQLite default and on the Postgres CI lane when
// DATABASE_URL is set. The original bug — user_sessions INSERT
// silently failing on Postgres — fires here as a hard failure on the
// Postgres lane.
func TestOIDC_E2E(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/oidc_e2e_test.db")
	}
	os.Setenv("SECRETS_ENCRYPTION_KEY", "fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY=")
	auth.JWTSecret = "oidc-e2e-jwt-secret-which-is-long-enough-for-tests"
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

	priv, pub := newRSAKey(t)
	idp := startMockOIDC(t, priv, pub)
	defer idp.server.Close()

	// Test-only injection so OIDC outbound calls can reach the
	// httptest server on 127.0.0.1. No env var, no production
	// escape hatch — the production binary keeps using
	// httputil.SafeOutboundClient + RejectPrivateHost.
	origClient := oidcOutboundClient
	origValidator := oidcIssuerValidator
	oidcOutboundClient = func(d time.Duration) *http.Client {
		return &http.Client{
			Timeout:   d,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
			// No CheckRedirect override: the mock IdP returns 302 for
			// /authorize, which is the contract.
		}
	}
	oidcIssuerValidator = func(string) error { return nil }
	t.Cleanup(func() {
		oidcOutboundClient = origClient
		oidcIssuerValidator = origValidator
	})

	// Seed the OIDC config row directly. Going through the admin PUT
	// would also work; the row form is cheaper and we already cover
	// the validator branch in a different test.
	const tenantID = "default"
	const clientID = "vapor-test-client"
	const clientSecret = "vapor-test-secret"
	enc, err := crypto.Encrypt(clientSecret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	now := time.Now().Unix()
	if _, err := db.DB.Exec(
		`INSERT INTO tenant_oidc_configs (tenant_id, issuer_url, client_id, client_secret_enc, default_role, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tenantID, idp.issuerURL, clientID, enc, "user", 1, now, now,
	); err != nil {
		t.Fatalf("seed oidc config: %v", err)
	}

	// Spin up a Fiber app with just the OIDC public routes wired up.
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	publicAPI := app.Group("/api", auth.RateLimiter(60, time.Minute))
	api := app.Group("/api/v1", auth.AuthMiddleware(), auth.CSRFMiddleware())
	RegisterOIDCRoutes(app, publicAPI, api)

	// 1. /api/auth/oidc/login — server fetches discovery + builds
	//    the auth URL, persists state, redirects.
	loginReq := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/login?tenant="+tenantID, nil)
	loginReq.Host = "example.com" // c.Hostname()
	loginResp, err := app.Test(loginReq, -1)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("login expected 302, got %d", loginResp.StatusCode)
	}
	authURL := loginResp.Header.Get("Location")
	if authURL == "" {
		t.Fatal("login response missing Location header")
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authURL: %v", err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("authorize URL missing state param")
	}

	// 2. Look up the issued state from the DB so the mock IdP can
	//    use the nonce it stored alongside.
	var nonce, redirectURI string
	if err := db.DB.QueryRow(`SELECT nonce, redirect_uri FROM oidc_states WHERE state = ?`, state).Scan(&nonce, &redirectURI); err != nil {
		t.Fatalf("read state row: %v", err)
	}

	// 3. The IdP would normally redirect the user to redirect_uri
	//    with ?state=...&code=... after the user authenticates. We
	//    simulate the callback directly. The mock /token endpoint
	//    will issue an id_token with the SAME nonce.
	idp.nextNonce = nonce
	idp.nextSub = "subject-42"
	idp.nextEmail = "alice@example.com"
	idp.nextName = "Alice Tester"
	// Codex #4: email_verified must be present and true for the
	// success path. The existing E2E continues to verify the full
	// happy flow; the three-branch matrix lives in the dedicated
	// TestOIDC_EmailVerifiedBranches test below.
	verified := true
	idp.nextEmailVerified = &verified

	callbackReq := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/auth/oidc/callback?state=%s&code=any-code", state), nil)
	callbackReq.Host = "example.com"
	callbackResp, err := app.Test(callbackReq, -1)
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	if callbackResp.StatusCode != http.StatusFound {
		body, _ := readAllBody(callbackResp)
		t.Fatalf("callback expected 302, got %d body=%s", callbackResp.StatusCode, body)
	}
	// Cookie should be set.
	var authCookie *http.Cookie
	for _, c := range callbackResp.Cookies() {
		if c.Name == "auth_token" {
			authCookie = c
			break
		}
	}
	if authCookie == nil || authCookie.Value == "" {
		t.Fatal("callback did not set auth_token cookie")
	}

	// 4. Verify the user_sessions row has every NOT NULL column
	//    populated. This is the original bug: a partial INSERT
	//    silently passed on SQLite, hard-failed on Postgres. The
	//    cookie value's SHA-256 hex hash is the token_hash.
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(authCookie.Value)))
	var sid, userID string
	var createdAt, lastSeen int64
	var ipAddr, userAgent string
	if err := db.DB.QueryRow(
		`SELECT id, user_id, ip_address, user_agent, created_at, last_seen FROM user_sessions WHERE token_hash = ?`,
		tokenHash,
	).Scan(&sid, &userID, &ipAddr, &userAgent, &createdAt, &lastSeen); err != nil {
		t.Fatalf("session row lookup: %v", err)
	}
	if sid == "" || userID == "" || createdAt == 0 || lastSeen == 0 {
		t.Fatalf("session row has empty required column: id=%q user_id=%q created_at=%d last_seen=%d", sid, userID, createdAt, lastSeen)
	}

	// 5. JIT-provisioned user exists.
	var email string
	if err := db.DB.QueryRow(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
		t.Fatalf("user lookup: %v", err)
	}
	if email != "alice@example.com" {
		t.Errorf("expected JIT-provisioned email alice@example.com, got %q", email)
	}

	// 6. Reuse the cookie for a subsequent authenticated request.
	//    AuthMiddleware's stateful session check should accept it —
	//    the original Postgres bug manifested here, not at callback
	//    time, because the broken INSERT happened-but-failed and
	//    the cookie was set anyway, then the next request 401'd
	//    "Session revoked".
	app.Get("/api/v1/__test_authed", auth.AuthMiddleware(), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true, "uid": c.Locals("user_id")})
	})
	reuseReq := httptest.NewRequest(http.MethodGet, "/api/v1/__test_authed", nil)
	reuseReq.AddCookie(authCookie)
	reuseResp, err := app.Test(reuseReq, -1)
	if err != nil {
		t.Fatalf("reuse request: %v", err)
	}
	if reuseResp.StatusCode != http.StatusOK {
		body, _ := readAllBody(reuseResp)
		t.Fatalf("reuse expected 200, got %d body=%s", reuseResp.StatusCode, body)
	}
}

// --- mock OIDC provider ------------------------------------------------

type mockIdp struct {
	server    *httptest.Server
	issuerURL string
	priv      *rsa.PrivateKey
	pub       *rsa.PublicKey
	clientID  string
	nextNonce string
	nextSub   string
	nextEmail string
	nextName  string
	// nextEmailVerified controls the email_verified claim sent in the
	// id_token. Codex #4 introduced this knob so tests can exercise
	// the absent/false/true branches. Default zero value (nil pointer)
	// means "omit the claim entirely" — the IdP shape an unmodified
	// /token response uses.
	nextEmailVerified *bool
}

func startMockOIDC(t *testing.T, priv *rsa.PrivateKey, pub *rsa.PublicKey) *mockIdp {
	t.Helper()
	m := &mockIdp{priv: priv, pub: pub, clientID: "vapor-test-client"}
	mux := http.NewServeMux()
	m.server = httptest.NewServer(mux)
	m.issuerURL = m.server.URL

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                                m.issuerURL,
			"authorization_endpoint":                m.issuerURL + "/authorize",
			"token_endpoint":                        m.issuerURL + "/token",
			"jwks_uri":                              m.issuerURL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		nB64 := base64.RawURLEncoding.EncodeToString(m.pub.N.Bytes())
		eB := big.NewInt(int64(m.pub.E)).Bytes()
		eB64 := base64.RawURLEncoding.EncodeToString(eB)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kty": "RSA",
					"alg": "RS256",
					"use": "sig",
					"kid": "test-key-1",
					"n":   nB64,
					"e":   eB64,
				},
			},
		})
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		// We do NOT validate the code or client_secret here — the
		// test's purpose is to exercise the server-side flow, not
		// to act as a conformant IdP. Production calls oauth2.Config.
		// Exchange which sends client_secret in form params; the mock
		// accepts any.
		now := time.Now()
		claims := jwt.MapClaims{
			"iss":   m.issuerURL,
			"aud":   m.clientID,
			"sub":   m.nextSub,
			"email": m.nextEmail,
			"name":  m.nextName,
			"nonce": m.nextNonce,
			"iat":   now.Unix(),
			"exp":   now.Add(5 * time.Minute).Unix(),
		}
		// Codex #4: include email_verified iff the test set it. Absent
		// pointer = absent claim, mirroring IdPs that don't issue
		// email_verified at all.
		if m.nextEmailVerified != nil {
			claims["email_verified"] = *m.nextEmailVerified
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "test-key-1"
		idToken, err := token.SignedString(m.priv)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "test-access-token",
			"id_token":     idToken,
			"token_type":   "Bearer",
			"expires_in":   300,
		})
	})

	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		// Not exercised in the test — we drive the callback directly.
		http.Error(w, "authorize: not used by E2E test", http.StatusNotFound)
	})

	return m
}

func newRSAKey(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	return priv, &priv.PublicKey
}

// readAllBody is a tiny helper since net/http's Response.Body is io.Reader.
func readAllBody(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			if err.Error() == "EOF" || err.Error() == "io: read on closed response body" {
				break
			}
			return sb.String(), err
		}
	}
	return sb.String(), nil
}

// Silence the unused-import lint when these are only referenced in
// httptest setup. context + httputilv kept here for completeness — the
// outbound override above already touches the package's variables but
// the linter doesn't see that as a top-level use until the test runs.
var _ = context.TODO
var _ = httputilv.SafeOutboundClient

// TestOIDC_EmailVerifiedBranches is the Codex #4 attack-path
// regression. The OIDC callback must reject id_tokens that do not
// carry email_verified=true; without that check, any IdP account
// that lets the user pick their own email claim pivots to "log in
// as <admin email>". Codex's spec named three cases that must
// reject (missing, false) and one that must succeed (true).
func TestOIDC_EmailVerifiedBranches(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/oidc_email_verified.db")
	}
	os.Setenv("SECRETS_ENCRYPTION_KEY", "fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY=")
	auth.JWTSecret = "oidc-emailver-jwt-secret-which-is-long-enough-for-tests"
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

	priv, pub := newRSAKey(t)
	idp := startMockOIDC(t, priv, pub)
	defer idp.server.Close()

	origClient := oidcOutboundClient
	origValidator := oidcIssuerValidator
	oidcOutboundClient = func(d time.Duration) *http.Client {
		return &http.Client{
			Timeout:   d,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
		}
	}
	oidcIssuerValidator = func(string) error { return nil }
	t.Cleanup(func() {
		oidcOutboundClient = origClient
		oidcIssuerValidator = origValidator
	})

	const tenantID = "default"
	enc, err := crypto.Encrypt("vapor-test-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	now := time.Now().Unix()
	if _, err := db.DB.Exec(
		`INSERT INTO tenant_oidc_configs (tenant_id, issuer_url, client_id, client_secret_enc, default_role, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tenantID, idp.issuerURL, "vapor-test-client", enc, "user", 1, now, now,
	); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	publicAPI := app.Group("/api", auth.RateLimiter(60, time.Minute))
	api := app.Group("/api/v1", auth.AuthMiddleware(), auth.CSRFMiddleware())
	RegisterOIDCRoutes(app, publicAPI, api)

	truth := true
	falsy := false
	for _, tc := range []struct {
		name        string
		verified    *bool
		wantStatus  int
		wantSession bool
	}{
		{"absent_claim_rejected", nil, http.StatusUnauthorized, false},
		{"verified_false_rejected", &falsy, http.StatusUnauthorized, false},
		{"verified_true_accepted", &truth, http.StatusFound, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Burn fresh state per subtest so a session from the
			// success case doesn't bleed into the rejection cases.
			if _, err := db.DB.Exec(`DELETE FROM oidc_states`); err != nil {
				t.Fatalf("clear states: %v", err)
			}
			if _, err := db.DB.Exec(`DELETE FROM user_sessions`); err != nil {
				t.Fatalf("clear sessions: %v", err)
			}
			// /login to issue a fresh state row.
			loginReq := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/login?tenant="+tenantID, nil)
			loginReq.Host = "example.com"
			loginResp, err := app.Test(loginReq, -1)
			if err != nil {
				t.Fatalf("login: %v", err)
			}
			if loginResp.StatusCode != http.StatusFound {
				t.Fatalf("login expected 302, got %d", loginResp.StatusCode)
			}
			authURL := loginResp.Header.Get("Location")
			parsed, err := url.Parse(authURL)
			if err != nil {
				t.Fatalf("parse authURL: %v", err)
			}
			state := parsed.Query().Get("state")
			var nonce, redirectURI string
			if err := db.DB.QueryRow(`SELECT nonce, redirect_uri FROM oidc_states WHERE state = ?`, state).Scan(&nonce, &redirectURI); err != nil {
				t.Fatalf("read state: %v", err)
			}

			// Configure the mock IdP for this branch.
			idp.nextNonce = nonce
			idp.nextSub = "subject-" + tc.name
			idp.nextEmail = "branch-" + tc.name + "@example.com"
			idp.nextName = "Branch " + tc.name
			idp.nextEmailVerified = tc.verified

			cbReq := httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/api/auth/oidc/callback?state=%s&code=any", state), nil)
			cbReq.Host = "example.com"
			cbResp, err := app.Test(cbReq, -1)
			if err != nil {
				t.Fatalf("callback: %v", err)
			}
			if cbResp.StatusCode != tc.wantStatus {
				body, _ := readAllBody(cbResp)
				t.Fatalf("callback status = %d, want %d body=%s",
					cbResp.StatusCode, tc.wantStatus, body)
			}
			// Sanity: a rejection must NOT create a session row.
			var n int
			if err := db.DB.QueryRow(`SELECT COUNT(*) FROM user_sessions`).Scan(&n); err != nil {
				t.Fatalf("count sessions: %v", err)
			}
			gotSession := n > 0
			if gotSession != tc.wantSession {
				t.Errorf("session created = %v, want %v", gotSession, tc.wantSession)
			}
		})
	}
}
