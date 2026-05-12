// Package tailscale wraps the Tailscale REST + OAuth API surfaces
// VaporwareRMM needs for the integration designed in issue #18.
//
// The client is the single chokepoint for all Tailscale traffic from
// the server. No handler code is allowed to talk to api.tailscale.com
// directly — every API interaction flows through this package so
// rate-limit handling, error classification, and token caching live
// in one place.
//
// Phase 1 (this package's initial shape) supports:
//
//   - OAuth token exchange (client credentials grant) against
//     https://api.tailscale.com/api/v2/oauth/token
//   - Three-checkmark validation: authentication, auth-key minting
//     scope, device list scope.
//   - Tailnet enumeration so the setup wizard / settings page can
//     pick the tailnet the credential owns.
//   - Device listing for the Settings → Network page.
//   - MintAuthKey as a STUB — its API surface matches what Phase 2's
//     preauth endpoint will call. Wiring the agent install flow to
//     this method is explicitly out of scope for Phase 1.
//
// Sentinel error types let callers branch on the specific failure
// mode without parsing free-text. The Settings UI surfaces each
// error with a "Fix this" link to the relevant Tailscale admin page.
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiBase      = "https://api.tailscale.com"
	oauthTokenEP = "/api/v2/oauth/token"
)

// Sentinel errors. Callers use errors.Is(err, ErrTailscale*) to
// branch on specific failure modes. Each maps to a structured error
// code the dashboard surfaces in the three-checkmark validation UI.
var (
	// ErrTailscaleUnreachable: network-level failure (DNS, TCP, TLS,
	// timeout). The control plane was not reachable at all.
	ErrTailscaleUnreachable = errors.New("tailscale: control plane unreachable")

	// ErrTailscaleAuthFailed: HTTP 401 from any endpoint. The OAuth
	// client ID / secret pair is invalid.
	ErrTailscaleAuthFailed = errors.New("tailscale: authentication failed (invalid client_id/client_secret)")

	// ErrTailscaleScopeMissingAuthKeys: HTTP 403 when minting an
	// auth key. The OAuth client lacks the auth_keys (write) scope.
	ErrTailscaleScopeMissingAuthKeys = errors.New("tailscale: OAuth client missing auth_keys (write) scope")

	// ErrTailscaleScopeMissingDeviceList: HTTP 403 when listing
	// devices. The OAuth client lacks the devices (read) scope.
	ErrTailscaleScopeMissingDeviceList = errors.New("tailscale: OAuth client missing devices (read) scope")

	// ErrTailscaleRateLimited: HTTP 429. Caller should back off and
	// retry after RateLimitRetryAfter seconds (extract via
	// errors.As + *RateLimitedError).
	ErrTailscaleRateLimited = errors.New("tailscale: rate limited")
)

// RateLimitedError carries the Retry-After header value (seconds) so
// callers can schedule a precise retry rather than guess.
type RateLimitedError struct {
	RetryAfterSeconds int
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("tailscale: rate limited (retry after %ds)", e.RetryAfterSeconds)
}
func (e *RateLimitedError) Is(target error) bool {
	return target == ErrTailscaleRateLimited
}

// Client is the Tailscale API wrapper.
type Client struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client
	baseURL      string

	// Token cache. Tailscale's access tokens are typically short-
	// lived (~30 minutes); we refresh ~60 seconds before expiry so
	// validation flows don't fail mid-request.
	tokenMu  sync.Mutex
	token    string
	tokenExp time.Time
}

// NewClient builds a client with the given OAuth credentials. Use
// WithHTTPClient + WithBaseURL in tests to inject httptest servers.
func NewClient(clientID, clientSecret string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      apiBase,
	}
}

// WithHTTPClient overrides the default HTTP client. Test-only.
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	c.httpClient = h
	return c
}

// WithBaseURL overrides the API base URL. Test-only.
func (c *Client) WithBaseURL(u string) *Client {
	c.baseURL = strings.TrimRight(u, "/")
	return c
}

// Authenticate exchanges the OAuth client credentials for an access
// token. Subsequent API calls reuse the cached token until it nears
// expiry.
func (c *Client) Authenticate(ctx context.Context) error {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp.Add(-60*time.Second)) {
		return nil
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+oauthTokenEP, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("tailscale: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.clientID, c.clientSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTailscaleUnreachable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrTailscaleAuthFailed
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return rateLimitErrFromHeader(resp)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: oauth token endpoint HTTP %d", ErrTailscaleUnreachable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tailscale: oauth token unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("tailscale: decode token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return fmt.Errorf("tailscale: token response missing access_token")
	}
	c.token = tokenResp.AccessToken
	if tokenResp.ExpiresIn > 0 {
		c.tokenExp = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	} else {
		// Default to 30 minutes if Tailscale ever omits expires_in.
		c.tokenExp = time.Now().Add(30 * time.Minute)
	}
	return nil
}

// Tailnet is a tailnet the credential has access to.
type Tailnet struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

// ListTailnets enumerates the tailnets accessible to the OAuth
// client. Tailscale's API exposes tailnet membership via
// /api/v2/tailnet/-/ (the dash is a "self" alias) which returns the
// tailnet the credential is bound to. For OAuth clients that aren't
// bound to a single tailnet, the API returns multiple entries.
//
// This is intentionally generous about response shapes — Tailscale
// has shipped multiple variants over time. The setup wizard wants
// "give me the tailnet(s) so the operator can pick one", not a
// strict shape contract.
func (c *Client) ListTailnets(ctx context.Context) ([]Tailnet, error) {
	if err := c.Authenticate(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v2/tailnet/-/", nil)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTailscaleUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrTailscaleAuthFailed
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, rateLimitErrFromHeader(resp)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tailscale: list tailnets HTTP %d: %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Name        string `json:"name"`
		DisplayName string `json:"organization"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("tailscale: decode tailnet response: %w", err)
	}
	if raw.Name == "" {
		return []Tailnet{}, nil
	}
	return []Tailnet{{Name: raw.Name, DisplayName: raw.DisplayName}}, nil
}

// ValidateAuthKeyScope mints an immediately-expiring test auth key
// to prove the OAuth client carries the auth_keys (write) scope. The
// minted key is single-use, ephemeral, and lives for one minute — so
// the validation pass can never be used to grant network access.
func (c *Client) ValidateAuthKeyScope(ctx context.Context, tailnet string) error {
	if err := c.Authenticate(ctx); err != nil {
		return err
	}
	body := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"devices": map[string]interface{}{
				"create": map[string]interface{}{
					"reusable":      false,
					"ephemeral":     true,
					"preauthorized": false,
					"tags":          []string{},
				},
			},
		},
		"expirySeconds": 60,
		"description":   "VaporwareRMM scope validation probe",
	}
	bodyJSON, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v2/tailnet/"+url.PathEscape(tailnet)+"/keys",
		strings.NewReader(string(bodyJSON)))
	if err != nil {
		return err
	}
	c.applyAuth(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTailscaleUnreachable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusUnauthorized:
		return ErrTailscaleAuthFailed
	case http.StatusForbidden:
		return ErrTailscaleScopeMissingAuthKeys
	case http.StatusTooManyRequests:
		return rateLimitErrFromHeader(resp)
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tailscale: validate auth-key scope HTTP %d: %s", resp.StatusCode, string(body))
	}
}

// ValidateDeviceListScope calls the device-list endpoint to prove
// the OAuth client has the devices (read) scope. Discards the
// response body.
func (c *Client) ValidateDeviceListScope(ctx context.Context, tailnet string) error {
	if err := c.Authenticate(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v2/tailnet/"+url.PathEscape(tailnet)+"/devices", nil)
	if err != nil {
		return err
	}
	c.applyAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTailscaleUnreachable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return ErrTailscaleAuthFailed
	case http.StatusForbidden:
		return ErrTailscaleScopeMissingDeviceList
	case http.StatusTooManyRequests:
		return rateLimitErrFromHeader(resp)
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tailscale: validate device-list scope HTTP %d: %s", resp.StatusCode, string(body))
	}
}

// Device is a managed device on the tailnet.
type Device struct {
	Name      string   `json:"name"`
	Hostname  string   `json:"hostname"`
	Addresses []string `json:"addresses"`
	OS        string   `json:"os"`
	Tags      []string `json:"tags"`
	LastSeen  string   `json:"lastSeen"`
}

// ListDevices returns devices on the tailnet for display in the
// Settings → Network page.
func (c *Client) ListDevices(ctx context.Context, tailnet string) ([]Device, error) {
	if err := c.Authenticate(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v2/tailnet/"+url.PathEscape(tailnet)+"/devices", nil)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTailscaleUnreachable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, ErrTailscaleAuthFailed
	case http.StatusForbidden:
		return nil, ErrTailscaleScopeMissingDeviceList
	case http.StatusTooManyRequests:
		return nil, rateLimitErrFromHeader(resp)
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tailscale: list devices HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Devices []Device `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("tailscale: decode device list: %w", err)
	}
	return out.Devices, nil
}

// MintAuthKeyOptions is the Phase-2 surface for minting per-install
// auth keys. The Phase-2 preauth endpoint will populate these from
// the install request (tags=[tag:tenant-<id>], ephemeral=false,
// preauthorized=true, reusable=false, expirySeconds=300).
type MintAuthKeyOptions struct {
	Tailnet       string
	Tags          []string
	Reusable      bool
	Ephemeral     bool
	Preauthorized bool
	ExpirySeconds int
	Description   string
}

// AuthKey is the response shape from minting. The plaintext Key
// field is single-use — once the install script consumes it via
// `tailscale up --authkey=...`, Tailscale invalidates the key
// server-side.
type AuthKey struct {
	ID      string    `json:"id"`
	Key     string    `json:"key"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

// MintAuthKey is the Phase-2 surface. Phase-1 keeps the signature
// stable but the function is unused by any HTTP handler — the
// preauth endpoint that calls this lands in Phase 2.
//
// The implementation IS wired through to Tailscale's API, not a
// stub-returning-zero. That's intentional: keeping the call live
// means Phase 1 tests can exercise the real code path with mocked
// HTTP responses, so when Phase 2 wires the handler the contract
// is already known.
func (c *Client) MintAuthKey(ctx context.Context, opts MintAuthKeyOptions) (*AuthKey, error) {
	if err := c.Authenticate(ctx); err != nil {
		return nil, err
	}
	expiry := opts.ExpirySeconds
	if expiry <= 0 {
		expiry = 300
	}
	body := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"devices": map[string]interface{}{
				"create": map[string]interface{}{
					"reusable":      opts.Reusable,
					"ephemeral":     opts.Ephemeral,
					"preauthorized": opts.Preauthorized,
					"tags":          opts.Tags,
				},
			},
		},
		"expirySeconds": expiry,
		"description":   opts.Description,
	}
	bodyJSON, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v2/tailnet/"+url.PathEscape(opts.Tailnet)+"/keys",
		strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTailscaleUnreachable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
	case http.StatusUnauthorized:
		return nil, ErrTailscaleAuthFailed
	case http.StatusForbidden:
		return nil, ErrTailscaleScopeMissingAuthKeys
	case http.StatusTooManyRequests:
		return nil, rateLimitErrFromHeader(resp)
	default:
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tailscale: mint auth key HTTP %d: %s", resp.StatusCode, string(errBody))
	}
	var k AuthKey
	if err := json.NewDecoder(resp.Body).Decode(&k); err != nil {
		return nil, fmt.Errorf("tailscale: decode auth key: %w", err)
	}
	return &k, nil
}

func (c *Client) applyAuth(req *http.Request) {
	c.tokenMu.Lock()
	tok := c.token
	c.tokenMu.Unlock()
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

func rateLimitErrFromHeader(resp *http.Response) error {
	retry := 30
	if h := resp.Header.Get("Retry-After"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 {
			retry = n
		}
	}
	return &RateLimitedError{RetryAfterSeconds: retry}
}
