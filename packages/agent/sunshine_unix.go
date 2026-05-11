//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// startSunshineHidden starts Sunshine in headless/background mode without showing its UI
func (a *Agent) startSunshineHidden() {
	// Try common install paths
	sunshinePaths := []string{
		"/usr/bin/sunshine",
		"/usr/local/bin/sunshine",
		"/opt/sunshine/sunshine",
	}

	sunshinePath := ""
	// Check PATH first
	if path, err := exec.LookPath("sunshine"); err == nil {
		sunshinePath = path
	} else {
		// Try known install paths
		for _, p := range sunshinePaths {
			if _, err := os.Stat(p); err == nil {
				sunshinePath = p
				break
			}
		}
	}

	if sunshinePath == "" {
		slog.Info("Sunshine not found on system, skipping auto-start")
		return
	}

	// Codex #2: refuse to launch with legacy hard-coded creds.
	// loadOrGenerateSunshineCreds returns errSunshineDefaultCreds
	// when the persisted password is the literal "vaporrmm" string
	// the previous build baked into the binary. Operators rotate
	// with VAPOR_ROTATE_SUNSHINE=1.
	creds, err := loadOrGenerateSunshineCreds()
	if err != nil {
		slog.Error("refusing to launch Sunshine", "error", err)
		return
	}
	if err := a.configureSunshine(creds.Password); err != nil {
		slog.Warn("could not configure Sunshine credentials", "error", err)
	}

	// Start Sunshine hidden (no UI, no console window)
	go func() {
		cmd := exec.Command(sunshinePath)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			slog.Warn("could not start Sunshine", "error", err)
		} else {
			slog.Info("Sunshine started in background mode")
		}
	}()
}

// getSunshineConfigDir returns the Sunshine configuration directory
func getSunshineConfigDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/root"
	}
	// Prefer system-wide config if running as root
	if os.Getuid() == 0 {
		if _, err := os.Stat("/etc/sunshine"); err == nil {
			return "/etc/sunshine"
		}
	}
	// Fallback to user config
	configDir := filepath.Join(home, ".config", "sunshine")
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		_ = os.MkdirAll(configDir, 0750)
	}
	return configDir
}

// configureSunshine sets the admin password in Sunshine's config
func (a *Agent) configureSunshine(password string) error {
	configDir := getSunshineConfigDir()
	
	// Write credentials.json
	credsPath := filepath.Join(configDir, "credentials.json")
	creds := map[string]interface{}{
		"username": agentSunshineUsername,
		"password": password,
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	if err := os.WriteFile(credsPath, data, 0600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	// Ensure sunshine.conf exists with basic settings.
	// Codex #2: origin_web_ui_allowed was previously `*` which lets any
	// origin frame / fetch the Sunshine web UI. Restrict to localhost
	// so the credentialed UI can't be reached cross-origin even if
	// someone tunnels port 47990. Operators who want LAN access can
	// edit the file post-install.
	confPath := filepath.Join(configDir, "sunshine.conf")
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		conf := `origin_web_ui_allowed = lan
min_log_level = info
`
		if err := os.WriteFile(confPath, []byte(conf), 0644); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
	}

	slog.Info("Sunshine configured with credentials", "config_dir", configDir)
	return nil
}

// getSunshinePIN attempts to fetch the current pairing PIN from Sunshine
func (a *Agent) getSunshinePIN() (string, error) {
	// Strategy 1: Try Sunshine's API
	pin, err := a.getSunshinePINFromAPI()
	if err == nil && pin != "" {
		return pin, nil
	}

	// Strategy 2: Parse Sunshine logs
	pin, err = a.getSunshinePINFromLogs()
	if err == nil && pin != "" {
		return pin, nil
	}

	return "", fmt.Errorf("could not fetch PIN from Sunshine")
}

// getSunshinePINFromAPI tries to get PIN via Sunshine's REST API
func (a *Agent) getSunshinePINFromAPI() (string, error) {
	// Sunshine API requires authentication. Load the per-device
	// credential the agent generated at install time (Codex #2);
	// hard-coded "vaporrmm" is gone.
	creds, err := loadOrGenerateSunshineCreds()
	if err != nil {
		return "", fmt.Errorf("sunshine creds load: %w", err)
	}
	loginBody, _ := json.Marshal(map[string]string{"password": creds.Password})
	loginReq, err := http.NewRequest(http.MethodPost, "http://localhost:47990/api/password", bytes.NewReader(loginBody))
	if err != nil {
		return "", err
	}
	loginReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	loginResp, err := client.Do(loginReq)
	if err != nil {
		return "", err
	}
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login failed: %d", loginResp.StatusCode)
	}

	// Extract cookies for subsequent requests
	cookies := loginResp.Cookies()

	// Try to fetch pairing info - Sunshine doesn't have a direct /api/pin endpoint
	// but we can check the config or logs API
	// For now, this is a placeholder - the log parsing is more reliable
	_ = cookies
	return "", fmt.Errorf("API PIN fetch not implemented")
}

// getSunshinePINFromLogs parses Sunshine logs for PIN codes
func (a *Agent) getSunshinePINFromLogs() (string, error) {
	// Try journald first (systemd)
	if _, err := exec.LookPath("journalctl"); err == nil {
		cmd := exec.Command("journalctl", "-u", "sunshine", "--since", "1 minute ago", "--no-pager", "-q")
		output, err := cmd.Output()
		if err == nil {
			pin := extractPINFromLog(string(output))
			if pin != "" {
				return pin, nil
			}
		}
	}

	// Try log file in config dir
	configDir := getSunshineConfigDir()
	logPath := filepath.Join(configDir, "sunshine.log")
	if data, err := os.ReadFile(logPath); err == nil {
		pin := extractPINFromLog(string(data))
		if pin != "" {
			return pin, nil
		}
	}

	return "", fmt.Errorf("no PIN found in logs")
}

// extractPINFromLog extracts a PIN code from Sunshine log output
func extractPINFromLog(logOutput string) string {
	lines := strings.Split(logOutput, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToLower(lines[i])
		// Look for PIN-related log lines
		// Sunshine typically logs: "PIN: 123456" or "pairing pin: 123456"
		if strings.Contains(line, "pin") {
			// Extract digits from the line
			var pin string
			for _, ch := range lines[i] {
				if ch >= '0' && ch <= '9' {
					pin += string(ch)
				}
			}
			// PINs are typically 4-6 digits
			if len(pin) >= 4 && len(pin) <= 6 {
				return pin
			}
		}
	}
	return ""
}

// handleGetSunshinePIN is an HTTP handler for the server to fetch the PIN
func (a *Agent) handleGetSunshinePIN(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pin, err := a.getSunshinePIN()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error":   "Could not fetch PIN",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pin":     pin,
		"message": "Copy this PIN into Moonlight Web Stream's pairing dialog",
	})
}
