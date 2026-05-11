//go:build windows

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
	"syscall"
	"time"
)

// startSunshineHidden starts Sunshine in headless/background mode without showing its UI
func (a *Agent) startSunshineHidden() {
	// Try common install paths
	sunshinePaths := []string{
		`C:\Program Files\Sunshine\sunshine.exe`,
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
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			slog.Warn("could not start Sunshine", "error", err)
		} else {
			slog.Info("Sunshine started in background mode")
		}
	}()
}

// getSunshineConfigDir returns the Sunshine configuration directory on Windows
func getSunshineConfigDir() string {
	appData := os.Getenv("LOCALAPPDATA")
	if appData == "" {
		appData = os.Getenv("APPDATA")
	}
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = home
	}
	configDir := filepath.Join(appData, "Sunshine")
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

	// Ensure sunshine.conf exists with basic settings
	confPath := filepath.Join(configDir, "sunshine.conf")
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		// Codex #2: origin_web_ui_allowed was previously `*`. Restrict
		// to LAN so the credentialed UI can't be reached cross-origin.
		conf := "origin_web_ui_allowed = lan\nmin_log_level = info\n"
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
	// Codex #2: load the per-device credential generated at install
	// time; the literal "vaporrmm" password is gone.
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

	return "", fmt.Errorf("API PIN fetch not implemented")
}

// getSunshinePINFromLogs parses Sunshine logs for PIN codes
func (a *Agent) getSunshinePINFromLogs() (string, error) {
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
		if strings.Contains(line, "pin") {
			var pin string
			for _, ch := range lines[i] {
				if ch >= '0' && ch <= '9' {
					pin += string(ch)
				}
			}
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
