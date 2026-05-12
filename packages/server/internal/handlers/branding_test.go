package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
)

// brandingTestEnv wires a Fiber app with branding routes plus a clean
// DB. AdminMiddleware reads user_role from locals; a pre-handler sets
// role=admin so the PUT path is reachable without minting a real JWT.
func brandingTestEnv(t *testing.T) *fiber.App {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_PATH", t.TempDir()+"/branding.db")
	}
	os.Setenv("SECRETS_ENCRYPTION_KEY", "fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY=")
	auth.JWTSecret = "branding-test-jwt-secret-needs-to-be-long-enough"
	if err := db.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	if err := db.ResetForTests(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	t.Cleanup(func() {
		if db.DB != nil && os.Getenv("DATABASE_URL") == "" {
			_ = db.DB.Close()
		}
	})

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	// Stub auth: drop a fake admin into locals so AdminMiddleware accepts
	// the PUT. The real AuthMiddleware would normally run before this
	// and set the same values from a JWT.
	api := app.Group("/api", func(c *fiber.Ctx) error {
		c.Locals("user_role", "admin")
		c.Locals("user_id", "test-admin")
		c.Locals("tenant_id", "default")
		return c.Next()
	})
	RegisterBrandingRoutes(app, api)
	return app
}

func putBranding(t *testing.T, app *fiber.App, payload map[string]interface{}) *http.Response {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/api/branding/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func getBranding(t *testing.T, app *fiber.App) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/branding/", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test GET: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode GET body: %v body=%s", err, string(raw))
	}
	return resp.StatusCode, out
}

// TestGetServerURL_PrefersPublicURL: when PUBLIC_URL is set, the
// install-script generator must use it verbatim and ignore whatever
// the request reports for hostname/port. Locks in the fix for the
// "c.Port() returned the client's ephemeral source port" bug —
// before the fix the generated URL looked like
// "https://rmm.example.com:57202" and every install failed because
// nothing was listening on that port.
func TestGetServerURL_PrefersPublicURL(t *testing.T) {
	app := brandingTestEnv(t)
	t.Setenv("PUBLIC_URL", "https://rmm.tcitsys.com")

	req := httptest.NewRequest(http.MethodGet, "/api/branding/install-links", nil)
	// Force the request to look like it came in via a different
	// hostname AND with a source-port that would have been appended
	// under the old buggy code.
	req.Host = "internal-proxy.local:8443"
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"server_url":"https://rmm.tcitsys.com"`) {
		t.Errorf("server_url should be the PUBLIC_URL verbatim; got body:\n%s", string(body))
	}
	if strings.Contains(string(body), "internal-proxy.local") {
		t.Errorf("server_url leaked the inbound Host header; got body:\n%s", string(body))
	}
	// No port should appear in the server_url field — neither the
	// client's source port nor any other.
	if idx := strings.Index(string(body), `"server_url":"`); idx >= 0 {
		tail := string(body)[idx+len(`"server_url":"`):]
		if end := strings.Index(tail, `"`); end > 0 {
			urlField := tail[:end]
			// Strip scheme so the colon there doesn't count.
			afterScheme := strings.TrimPrefix(strings.TrimPrefix(urlField, "https://"), "http://")
			if strings.Contains(afterScheme, ":") {
				t.Errorf("server_url contains a port after scheme: %q", urlField)
			}
		}
	}
}

// TestGetServerURL_TrimsTrailingSlash: operators sometimes set
// PUBLIC_URL with a trailing slash. The previous request-derived
// path produced clean URLs; the env-var path must too, otherwise
// rendered URLs become "https://example.com//api/..." and the
// install script's curl 404s.
func TestGetServerURL_TrimsTrailingSlash(t *testing.T) {
	app := brandingTestEnv(t)
	t.Setenv("PUBLIC_URL", "https://rmm.example.com/")

	req := httptest.NewRequest(http.MethodGet, "/api/branding/install-links", nil)
	resp, _ := app.Test(req, -1)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"server_url":"https://rmm.example.com"`) {
		t.Errorf("server_url should have trailing slash trimmed; got:\n%s", string(body))
	}
	if strings.Contains(string(body), "rmm.example.com//") {
		t.Errorf("server_url contains double-slash; got:\n%s", string(body))
	}
}

// TestGetServerURL_FallsBackToRequestHost: with PUBLIC_URL unset,
// the function falls back to the request's host (dev / test path).
// The fallback MUST NOT append c.Port() — that was the original
// bug — so the URL is the bare scheme://host with no port suffix.
func TestGetServerURL_FallsBackToRequestHost(t *testing.T) {
	app := brandingTestEnv(t)
	os.Unsetenv("PUBLIC_URL")

	req := httptest.NewRequest(http.MethodGet, "/api/branding/install-links", nil)
	req.Host = "localhost"
	resp, _ := app.Test(req, -1)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"server_url":"http://localhost"`) {
		t.Errorf("fallback server_url should be http://localhost (no port); got:\n%s", string(body))
	}
	// The bug-class assertion: even if the test client picks an
	// ephemeral source port, the response MUST NOT carry a
	// ":<digits>" suffix on the URL.
	if matched := regexpMatchAny(string(body), `"server_url":"https?://[^/"]+:\d+`); matched {
		t.Errorf("fallback server_url appended a port; got:\n%s", string(body))
	}
}

func regexpMatchAny(haystack, pattern string) bool {
	re := regexp.MustCompile(pattern)
	return re.MatchString(haystack)
}

// TestBranding_CompanyNameWithAmpersand asserts that real customer
// names like "T&C IT Systems" or "Smith & Jones IT" persist through
// the PUT/GET cycle. Before this fix, the input validation rejected
// "&" as a shell metacharacter — a paranoid input-side defense that
// blocked legitimate MSPs from setting their own company name.
func TestBranding_CompanyNameWithAmpersand(t *testing.T) {
	app := brandingTestEnv(t)
	cases := []string{
		"T&C IT Systems",
		"Smith & Jones IT",
		"O'Reilly Media",
		`Acme "Premium" Services`,
		"Foo; Bar | Baz",
		"Tesla, Inc. ($TSLA)",
		`Path\Like\Name`,
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			resp := putBranding(t, app, map[string]interface{}{
				"app_name":      "vaporrmm",
				"company_name":  name,
				"primary_color": "#3b82f6",
			})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("PUT company_name=%q expected 200, got %d body=%s", name, resp.StatusCode, string(body))
			}
			code, out := getBranding(t, app)
			if code != http.StatusOK {
				t.Fatalf("GET expected 200, got %d", code)
			}
			if got, _ := out["company_name"].(string); got != name {
				t.Errorf("round-trip company_name=%q want=%q", got, name)
			}
		})
	}
}

// TestBranding_CompanyNameRejectsNewlines asserts the one remaining
// input constraint: a display field has no business carrying line
// breaks, and stripping them keeps the install-script comment header
// valid.
func TestBranding_CompanyNameRejectsNewlines(t *testing.T) {
	app := brandingTestEnv(t)
	cases := []string{
		"Foo\nBar",
		"Foo\r\nBar",
		"Foo\rBar",
	}
	for _, name := range cases {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(name, "\r", `\r`), "\n", `\n`), func(t *testing.T) {
			resp := putBranding(t, app, map[string]interface{}{
				"app_name":     "vaporrmm",
				"company_name": name,
			})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("PUT company_name with newline expected 400, got %d body=%s", resp.StatusCode, string(body))
			}
		})
	}
}

// bashSyntaxCheck runs `bash -n` against the given script source and
// returns whether bash accepted it. Skips the test if no bash is
// available on the runner.
func bashSyntaxCheck(t *testing.T, script string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(tmp, []byte(script), 0600); err != nil {
		t.Fatalf("write tmp script: %v", err)
	}
	cmd := exec.Command(bash, "-n", tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n rejected generated script: %v\noutput:\n%s\n---\nscript:\n%s", err, string(out), script)
	}
}

// TestGenerateInstallScript_AmpersandIsSafe asserts the install
// script generated for a company_name containing & syntax-checks
// cleanly and embeds the literal string inside the comment header
// rather than as an interpolated shell expression.
func TestGenerateInstallScript_AmpersandIsSafe(t *testing.T) {
	const name = "T&C IT Systems"
	script := generateInstallScript("vaporrmm", name, "https://example.com/icon.png", "https://example.com")
	bashSyntaxCheck(t, script)

	// Literal name must appear in the comment header line.
	want := "# Generated by " + name
	if !strings.Contains(script, want) {
		t.Errorf("script does not contain literal comment %q\n---\n%s", want, script)
	}
	// No double-comment-header (would happen if a newline split the
	// company name into a fresh line that got `#`-prefixed elsewhere).
	if strings.Count(script, "# Generated by") != 1 {
		t.Errorf("expected exactly one '# Generated by' line, got %d", strings.Count(script, "# Generated by"))
	}
}

// TestGenerateInstallScript_TailscaleDefaultEnabled verifies the
// generated install script flips INSTALL_TAILSCALE on by default
// (rather than requiring --install-tailscale) and that the
// Tailscale install section runs BEFORE the agent download so a
// missing auth key fails before any state is written.
func TestGenerateInstallScript_TailscaleDefaultEnabled(t *testing.T) {
	s := generateInstallScript("vaporrmm", "Default Co", "https://example.com/icon.png", "https://rmm.example.com")
	bashSyntaxCheck(t, s)

	if !strings.Contains(s, `INSTALL_TAILSCALE="1"`) {
		t.Error("install script must default INSTALL_TAILSCALE to \"1\"")
	}
	// The Tailscale-required auth-key error message must appear
	// (so when no key is provided, the script aborts early).
	if !strings.Contains(s, "Tailscale is enabled by default but no auth key was provided") {
		t.Error("script missing the Tailscale-auth-key-required error message")
	}
	// Tailscale install must precede the agent download. Use the
	// first occurrence of each marker to assert order.
	tsIdx := strings.Index(s, "--- Installing Tailscale ---")
	dlIdx := strings.Index(s, "Downloading pre-built agent binary")
	if tsIdx == -1 || dlIdx == -1 {
		t.Fatalf("expected both markers in script (tsIdx=%d dlIdx=%d)", tsIdx, dlIdx)
	}
	if tsIdx > dlIdx {
		t.Errorf("Tailscale install (offset %d) must run BEFORE agent download (offset %d) so auth-key failures abort early", tsIdx, dlIdx)
	}
	// tailscale up must include --hostname / --accept-routes /
	// --accept-dns=false; the last one keeps managed endpoints'
	// DNS from being hijacked by the tailnet's MagicDNS.
	for _, want := range []string{`--authkey="$TAILSCALE_AUTH_KEY"`, `--hostname="$TAILSCALE_HOSTNAME"`, `--accept-routes`, `--accept-dns=false`} {
		if !strings.Contains(s, want) {
			t.Errorf("tailscale up call missing flag %q", want)
		}
	}
}

// TestGenerateInstallScript_NoTailscaleOptOut verifies the
// --no-tailscale flag flips INSTALL_TAILSCALE to empty, and the
// generated script's argument-handling block carries the new
// flag.
func TestGenerateInstallScript_NoTailscaleOptOut(t *testing.T) {
	s := generateInstallScript("vaporrmm", "Optout Co", "https://example.com/icon.png", "https://rmm.example.com")
	bashSyntaxCheck(t, s)

	// The case arm in the argument parser must clear
	// INSTALL_TAILSCALE.
	if !strings.Contains(s, "--no-tailscale)") {
		t.Error("script missing the --no-tailscale case arm")
	}
	// The arm sets INSTALL_TAILSCALE="" so the Tailscale section
	// is skipped on this branch.
	noTSBlock := s[strings.Index(s, "--no-tailscale)"):]
	if !strings.Contains(noTSBlock[:200], `INSTALL_TAILSCALE=""`) {
		t.Errorf("--no-tailscale arm should clear INSTALL_TAILSCALE; got first 200 chars after marker:\n%s", noTSBlock[:200])
	}

	// Simulate operator passing --no-tailscale by running bash with
	// the script + flag, and assert the Tailscale-auth-key error
	// does NOT fire. Use `set -n` would skip execution; instead
	// dry-run with a runtime stub: write the script to disk, prefix
	// with a stub that aborts before the install proper, but lets
	// arg-parsing run.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "install.sh")
	// Replace the rest of the script (everything past arg-parsing)
	// with `echo "INSTALL_TAILSCALE=$INSTALL_TAILSCALE"; exit 0` so
	// we observe the post-parse value without actually running an
	// install. Locate the "echo \"==\"" banner that starts the real
	// install and truncate there.
	cutMarker := "echo \"========================================\"\necho \"  Installing $APP_NAME agent\""
	cut := strings.Index(s, cutMarker)
	if cut == -1 {
		t.Fatalf("could not find banner marker to truncate script")
	}
	stub := s[:cut] + "echo \"INSTALL_TAILSCALE=$INSTALL_TAILSCALE\"\nexit 0\n"
	if err := os.WriteFile(tmp, []byte(stub), 0700); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	out, err := exec.Command(bash, tmp, "--no-tailscale").CombinedOutput()
	if err != nil {
		t.Fatalf("stub run failed: %v\noutput=%s", err, string(out))
	}
	if !strings.Contains(string(out), "INSTALL_TAILSCALE=\n") && !strings.HasSuffix(strings.TrimSpace(string(out)), "INSTALL_TAILSCALE=") {
		t.Errorf("--no-tailscale did not clear INSTALL_TAILSCALE; bash output: %q", string(out))
	}
}

// TestDownloadAgent_LinuxAmd64Succeeds verifies the /download path
// serves the bundled agent binary when the file is present. Skipped
// locally because the path /opt/agents/linux-amd64 only exists
// inside the built container; CI runs go test outside the image.
// The test would catch a regression where the handler stops
// serving the file even when staged correctly.
func TestDownloadAgent_LinuxAmd64Succeeds(t *testing.T) {
	app := brandingTestEnv(t)
	const path = "/opt/agents/linux-amd64"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("skipping: %s not present (expected outside the built container): %v", path, err)
	}

	req := httptest.NewRequest(http.MethodGet, "/download/agent-linux-amd64", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/octet-stream") {
		t.Errorf("Content-Type: want application/octet-stream prefix, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Errorf("download body is empty")
	}
}

// TestDownloadAgent_UnknownPlatformReturns404 verifies the allowlist
// rejects platforms that aren't in agentBinaryPaths. Critical for
// the future Windows/macOS rollout — adding a route entry without
// adding the file would otherwise silently serve a wrong file.
func TestDownloadAgent_UnknownPlatformReturns404(t *testing.T) {
	app := brandingTestEnv(t)
	for _, target := range []string{
		"/download/agent-darwin-arm64",
		"/download/agent-windows-amd64",
		"/download/agent-darwin-amd64",
		"/download/agent-linux-arm64",
	} {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			resp, err := app.Test(req, -1)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("%s expected 404, got %d body=%s", target, resp.StatusCode, string(body))
			}
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), "agent binary not available") {
				t.Errorf("%s body should mention the missing-platform error, got: %s", target, string(body))
			}
		})
	}
}

// TestDownloadAgent_PathTraversalBlocked locks in that the allowlist
// pattern is the security control. A request whose params concatenate
// into "..-..-passwd" (or any other non-allowlisted key) MUST 404 at
// the map lookup, never reach SendFile. If a future refactor reverts
// to templating the params into a path, this test catches it.
func TestDownloadAgent_PathTraversalBlocked(t *testing.T) {
	app := brandingTestEnv(t)
	traversals := []string{
		"/download/agent-..-..",
		"/download/agent-..%2Fetc-passwd",
		"/download/agent-linux-amd64.bak",
		"/download/agent-LINUX-AMD64",
	}
	for _, target := range traversals {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			resp, err := app.Test(req, -1)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("%s unexpectedly served 200 body-len=%d", target, len(body))
			}
		})
	}
}

// TestGenerateInstallScript_ShellInjectionAttemptIsSafe is the
// adversarial regression: a malicious company_name that would, if
// interpolated into a shell-significant context, break out and run
// arbitrary commands. The fix's safety property is that the value
// lives inside a `#` comment line; bash does not interpret
// metacharacters there. `bash -n` must accept the script and the
// payload must remain on the comment line (no real `rm -rf /` line
// can appear).
func TestGenerateInstallScript_ShellInjectionAttemptIsSafe(t *testing.T) {
	payloads := []string{
		`"; rm -rf / #`,
		`'; rm -rf / #`,
		"$(rm -rf /)",
		"`rm -rf /`",
		"foo && rm -rf /",
		"foo | rm -rf /",
		"foo; rm -rf /",
		"$IFS",
		"\\$(reboot)",
	}
	for _, payload := range payloads {
		t.Run(payload, func(t *testing.T) {
			script := generateInstallScript("vaporrmm", payload, "https://example.com/icon.png", "https://example.com")
			bashSyntaxCheck(t, script)

			// Find the comment-header line containing the payload.
			// scrubForComment may have scrubbed \r \n into spaces;
			// otherwise the payload survives verbatim. Either way,
			// the line containing the payload MUST start with '#'
			// after any leading whitespace — proving it's a comment.
			var found bool
			for _, line := range strings.Split(script, "\n") {
				if strings.Contains(line, "rm -rf") ||
					strings.Contains(line, "reboot") ||
					strings.Contains(line, "IFS") ||
					strings.Contains(line, payload) {
					found = true
					trimmed := strings.TrimLeft(line, " \t")
					if !strings.HasPrefix(trimmed, "#") {
						t.Errorf("payload %q escaped the comment context:\n%s", payload, line)
					}
				}
			}
			if !found {
				// Payload was fully scrubbed (e.g., bare newline
				// payloads get replaced with spaces). That's also
				// safe — no command can run.
				t.Logf("payload %q was scrubbed out of the script entirely (also safe)", payload)
			}
		})
	}
}
