package handlers

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"vaporrmm/server/internal/auth"

	"github.com/gofiber/fiber/v2"
)

// safeProbeClient returns an http.Client whose Transport refuses to dial
// loopback / private / link-local destinations. The check happens at dial
// time using net.Dialer.Control, so DNS rebinding (where LookupIP and the
// kernel's resolver return different addresses) cannot bypass it: every
// connection attempt re-validates the resolved IP just before connect.
func safeProbeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("dial: address %s is not an IP", address)
			}
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				return fmt.Errorf("dial: refusing to connect to non-public address %s", ip.String())
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:     dialer.DialContext,
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
		// Refuse to follow redirects: a public host can otherwise return
		// 307 Location: http://169.254.169.254/... and bypass the dial
		// check entirely (the redirected request goes through the same
		// Control function but the original URL was already validated).
		// Surfacing the redirect as the client response is the safe
		// behaviour — the caller can decide whether to chase it.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// rejectPrivateHost validates an URL's scheme + initial-resolution hosts.
// This catches obviously-bad URLs early; the dial-time Control function in
// safeProbeClient is the real defense against DNS rebinding.
func rejectPrivateHost(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url missing host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns lookup failed: %w", err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("url resolves to a non-public address (%s)", ip.String())
		}
	}
	return nil
}

// RegisterIntegrationProbes adds super_admin-only readiness probes for the
// out-of-server integrations that an MSP must verify before going live:
// Tailscale CLI, Sunshine release availability, and Moonlight web URL.
//
// Each probe returns:
//
//	{ ok: bool, detail: "...", checked_at: <unix> }
func RegisterIntegrationProbes(api fiber.Router) {
	probes := api.Group("/admin/probes", auth.SuperAdminMiddleware())

	probes.Get("/tailscale", func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()
		// `tailscale status --json` exits 0 when connected and authenticated.
		cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return c.JSON(fiber.Map{
				"ok":     false,
				"detail": "tailscale CLI not present, not authenticated, or not running on this host: " + truncate(string(out), 400),
				"hint":   "Install tailscale, then `sudo tailscale up` and grant the daemon a tagged auth key with `--tags` permission to issue keys.",
			})
		}
		// Probe key issuance permission with --reusable=false --ephemeral=true
		// (creates a one-shot key; we don't return it, just confirm the call works).
		ctx2, cancel2 := context.WithTimeout(c.Context(), 8*time.Second)
		defer cancel2()
		test := exec.CommandContext(ctx2, "tailscale", "auth-key", "create", "--ephemeral", "--reusable=false")
		testOut, testErr := test.CombinedOutput()
		if testErr != nil {
			return c.JSON(fiber.Map{
				"ok":     false,
				"detail": "tailscale CLI works but auth-key create failed: " + truncate(string(testOut), 400),
				"hint":   "Tailscale ACLs may not allow this node to create auth keys, or your account may need an OAuth client. See https://tailscale.com/kb/1085/auth-keys",
			})
		}
		return c.JSON(fiber.Map{
			"ok":         true,
			"detail":     "Tailscale CLI present, authenticated, can issue auth keys.",
			"checked_at": time.Now().Unix(),
		})
	})

	probes.Get("/sunshine", func(c *fiber.Ctx) error {
		// Verify the configured Sunshine release URL is reachable.
		// Doesn't actually download — HEAD only.
		version := os.Getenv("SUNSHINE_VERSION")
		if version == "" {
			version = "v2025.628.4510"
		}
		url := "https://github.com/LizardByte/Sunshine/releases/download/" + version + "/sunshine-ubuntu-24.04-amd64.deb"
		ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "HEAD", url, nil)
		client := safeProbeClient(8 * time.Second)
		resp, err := client.Do(req)
		if err != nil {
			return c.JSON(fiber.Map{
				"ok":     false,
				"detail": "Sunshine release URL unreachable: " + err.Error(),
				"hint":   "Server may have no outbound internet, or LizardByte's GitHub releases moved. Bump SUNSHINE_VERSION and retry.",
			})
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return c.JSON(fiber.Map{
				"ok":     false,
				"detail": "Sunshine release URL returned " + resp.Status,
				"url":    url,
			})
		}
		return c.JSON(fiber.Map{
			"ok":         true,
			"detail":     "Sunshine release URL reachable.",
			"version":    version,
			"url":        url,
			"checked_at": time.Now().Unix(),
		})
	})

	// POST so that CSRFMiddleware forces a valid CSRF token. Otherwise a malicious
	// page could trick an authenticated super_admin's browser into probing
	// internal-network URLs (SSRF).
	probes.Post("/moonlight", func(c *fiber.Ctx) error {
		var body struct {
			URL string `json:"url"`
		}
		_ = c.BodyParser(&body)
		if body.URL == "" {
			body.URL = os.Getenv("MOONLIGHT_WEB_URL")
		}
		if body.URL == "" {
			return c.JSON(fiber.Map{
				"ok":     false,
				"detail": "MOONLIGHT_WEB_URL not configured. POST {\"url\":\"...\"} to test a specific instance.",
				"hint":   "Set MOONLIGHT_WEB_URL=https://moonlight.example.com to enable in-browser streaming.",
			})
		}
		// Defense-in-depth: refuse anything that isn't http(s) with a public-looking host.
		if !strings.HasPrefix(body.URL, "http://") && !strings.HasPrefix(body.URL, "https://") {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "url must be http(s)://"})
		}
		if err := rejectPrivateHost(body.URL); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "HEAD", body.URL, nil)
		client := safeProbeClient(5 * time.Second)
		resp, err := client.Do(req)
		if err != nil {
			return c.JSON(fiber.Map{
				"ok":     false,
				"detail": "Moonlight URL unreachable: " + err.Error(),
			})
		}
		defer resp.Body.Close()
		return c.JSON(fiber.Map{
			"ok":         resp.StatusCode < 500,
			"status":     resp.StatusCode,
			"checked_at": time.Now().Unix(),
		})
	})
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
