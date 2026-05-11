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
