package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// sunshinePairAddress returns the local Sunshine confui URL. Sunshine
// listens on 127.0.0.1:47990 by default. We deliberately don't expose
// this as a config option — pairing always goes through the local
// Sunshine instance the agent manages.
const sunshinePairAddress = "https://localhost:47990"

// submitSunshinePIN authenticates against Sunshine with the agent-set
// admin password (`configureSunshine` writes "vaporrmm") and POSTs the
// supplied PIN to /api/pin. Returns nil on success.
//
// This replaces the old "fetch PIN from logs" flow, which was always
// best-effort and depended on the user typing the PIN into Sunshine's
// own UI first. The correct pairing model: Moonlight (client) shows a
// PIN, the user enters it into vaporRMM dashboard, the server forwards
// to the agent which submits to Sunshine. Sunshine's own UI is bypassed.
func submitSunshinePIN(pin, otp string) error {
	pin = strings.TrimSpace(pin)
	if !validPIN(pin) {
		return fmt.Errorf("invalid PIN format (4-8 digits expected)")
	}
	jar, _ := cookiejar.New(nil)
	// Sunshine ships with a self-signed cert by default. We talk only
	// to localhost so InsecureSkipVerify is acceptable; an attacker
	// reaching loopback already has a foothold.
	client := &http.Client{
		Timeout: 10 * time.Second,
		Jar:     jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	// Step 1: log in to obtain a session cookie.
	loginBody, _ := json.Marshal(map[string]string{
		"username": "vaporrmm",
		"password": "vaporrmm",
	})
	loginReq, err := http.NewRequest(http.MethodPost, sunshinePairAddress+"/api/auth/login", bytes.NewReader(loginBody))
	if err != nil {
		return err
	}
	loginReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(loginReq)
	if err != nil {
		return fmt.Errorf("sunshine login: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Older Sunshine versions used /api/password instead. Retry.
		return retrySubmitWithLegacyAuth(client, pin, otp, body)
	}

	// Step 2: POST the PIN.
	pinBody := map[string]string{"pin": pin}
	if otp != "" {
		pinBody["name"] = otp // Sunshine accepts an optional client-name field
	}
	pinJSON, _ := json.Marshal(pinBody)
	pinReq, err := http.NewRequest(http.MethodPost, sunshinePairAddress+"/api/pin", bytes.NewReader(pinJSON))
	if err != nil {
		return err
	}
	pinReq.Header.Set("Content-Type", "application/json")
	pinResp, err := client.Do(pinReq)
	if err != nil {
		return fmt.Errorf("sunshine pin submit: %w", err)
	}
	defer pinResp.Body.Close()
	if pinResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(pinResp.Body)
		return fmt.Errorf("sunshine rejected pin: %d %s", pinResp.StatusCode, string(body))
	}
	return nil
}

// retrySubmitWithLegacyAuth handles older Sunshine builds (pre-2024)
// that exposed `/api/password` instead of the newer `/api/auth/login`.
// We treat both as best-effort fallbacks since the agent doesn't know
// the exact Sunshine version up front.
func retrySubmitWithLegacyAuth(client *http.Client, pin, otp string, prevBody []byte) error {
	loginBody, _ := json.Marshal(map[string]string{"password": "vaporrmm"})
	req, err := http.NewRequest(http.MethodPost, sunshinePairAddress+"/api/password", bytes.NewReader(loginBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sunshine legacy login: %w (initial body: %s)", err, string(prevBody))
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sunshine login failed (modern: %s, legacy: %d)", string(prevBody), resp.StatusCode)
	}
	pinBody := map[string]string{"pin": pin}
	if otp != "" {
		pinBody["name"] = otp
	}
	pinJSON, _ := json.Marshal(pinBody)
	pinReq, _ := http.NewRequest(http.MethodPost, sunshinePairAddress+"/api/pin", bytes.NewReader(pinJSON))
	pinReq.Header.Set("Content-Type", "application/json")
	pinResp, err := client.Do(pinReq)
	if err != nil {
		return fmt.Errorf("sunshine pin submit (legacy): %w", err)
	}
	defer pinResp.Body.Close()
	if pinResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(pinResp.Body)
		return fmt.Errorf("sunshine rejected pin (legacy): %d %s", pinResp.StatusCode, string(body))
	}
	return nil
}

// validPIN matches Moonlight pairing PIN format. Moonlight currently
// shows 4-digit PINs; some clients emit longer tokens — accept 4-8
// digits to be forward-compatible.
func validPIN(s string) bool {
	if len(s) < 4 || len(s) > 8 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// handlePairSunshine is the HTTP handler the server-side proxy hits to
// forward a Moonlight pairing PIN to the local Sunshine instance.
func (a *Agent) handlePairSunshine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PIN  string `json:"pin"`
		Name string `json:"name,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid body"})
		return
	}
	if err := submitSunshinePIN(req.PIN, req.Name); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "paired"})
}
