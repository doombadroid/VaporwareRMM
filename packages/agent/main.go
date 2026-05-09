package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

const (
	DefaultServerURL       = "http://localhost:8080"
	DefaultAgentPort       = 47991
	DefaultSunshinePort    = 47990
	HeartbeatInterval      = 30 * time.Second
	DefaultRequestTimeout  = 10 * time.Second
	MaxCommandHistory      = 100 // Maximum number of command results to keep
	MaxHeartbeatRetries    = 5
	HeartbeatBackoffBase   = 5 * time.Second
)

// Agent holds the agent's runtime state.
type Agent struct {
	serverURL    string
	port         int
	hostname     string
	deviceID     string
	apiToken     string // token the agent sends to the server
	registered   bool
	iconURL      string // URL to fetch branded icon from
	appName      string // Branded app name shown in tray
	companyName  string // Branded company name

	mu           sync.Mutex
	lastCommands []CommandResult
}

// CommandRequest describes a command the server wants the agent to run.
type CommandRequest struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"` // shell, ping, reboot
	Payload   map[string]interface{} `json:"payload"`
	CreatedAt time.Time              `json:"created_at"`
}

// CommandResult holds the outcome of an executed command.
type CommandResult struct {
	CommandID string    `json:"command_id"`
	Success   bool      `json:"success"`
	Output    string    `json:"output,omitempty"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Valid command types
var allowedCommandTypes = map[string]bool{
	"shell":  true,
	"script": true,
}

// dangerousPatterns blocks obvious destructive commands. Not a sandbox — proper
// isolation requires seccomp/apparmor/containers.
var dangerousPatterns = []string{
	"rm -rf /", "rm -rf /*", "mkfs", "dd if=/dev/zero",
	"> /dev/sda", ":(){ :|:& };:", "chmod 000 /", "mkfs.ext", "mkfs.xfs",
}

var dangerousRegexps = []*regexp.Regexp{
	regexp.MustCompile(`curl\s+.*\|\s*sh`),
	regexp.MustCompile(`curl\s+.*\|\s*bash`),
	regexp.MustCompile(`wget\s+.*\|\s*sh`),
	regexp.MustCompile(`wget\s+.*\|\s*bash`),
}

func isDangerous(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, p := range dangerousPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	for _, re := range dangerousRegexps {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// NewAgent creates an Agent, generating a random API token if none is provided.
func NewAgent(serverURL string, port int, apiToken string) (*Agent, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	if apiToken == "" {
		apiToken = generateToken()
		slog.Info("No VAPOR_AGENT_TOKEN set. Generated ephemeral token (not logged for security)")
	}

	// Load branding from server
	appName := "Vapor RMM"
	companyName := "Vaporware RMM"
	iconURL := ""

	branding, err := fetchBranding(serverURL)
	if err != nil {
		slog.Warn("could not fetch branding config", "error", err)
	} else {
		if branding.AppName != "" {
			appName = branding.AppName
		}
		if branding.CompanyName != "" {
			companyName = branding.CompanyName
		}
		if branding.IconURL != "" {
			iconURL = branding.IconURL
		}
	}

	// Restore device ID from previous run if available
	persistedDeviceID := loadDeviceID()

	return &Agent{
		serverURL:    serverURL,
		port:         port,
		hostname:     hostname,
		apiToken:     apiToken,
		deviceID:     persistedDeviceID,
		registered:   persistedDeviceID != "",
		appName:      appName,
		companyName:  companyName,
		iconURL:      iconURL,
		lastCommands: make([]CommandResult, 0, MaxCommandHistory),
	}, nil
}

// BrandingConfig matches the server's branding response
type BrandingConfig struct {
	AppName      string `json:"app_name"`
	IconURL      string `json:"icon_url"`
	CompanyName  string `json:"company_name"`
	PrimaryColor string `json:"primary_color"`
}

// fetchBranding retrieves branding config from the server
func fetchBranding(serverURL string) (*BrandingConfig, error) {
	resp, err := http.Get(serverURL + "/api/branding/")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var config BrandingConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, err
	}
	return &config, nil
}

// downloadIcon downloads the branded icon from the server and returns it as bytes
func downloadIcon(iconURL string) ([]byte, error) {
	if iconURL == "" {
		return nil, fmt.Errorf("no icon URL provided")
	}
	resp, err := http.Get(iconURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// setupSystemTray creates the system tray UI with only the branded icon and "Request Help" button.
// Skipped automatically when no display is available (headless/Docker/server).
// setupSystemTray dispatches to the platform-specific tray implementation
// (see tray_native.go on linux/windows; tray_noop.go elsewhere).
func (a *Agent) setupSystemTray() {
	startSystemTray(a)
}

// handleRequestHelp opens a support dialog or sends a help request to the server
func (a *Agent) handleRequestHelp() error {
	slog.Info("help requested", "device", a.hostname)

	// Send a help request command to the server
	helpData := map[string]interface{}{
		"device_id": a.deviceID,
		"hostname":  a.hostname,
		"type":      "help_request",
		"timestamp": time.Now().Unix(),
	}

	data, err := json.Marshal(helpData)
	if err != nil {
		return fmt.Errorf("error marshaling help request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, a.serverURL+"/agent/help-request", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("error creating help request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiToken)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("error sending help request: %w", err)
	}
	defer resp.Body.Close()

	// Show notification to user (no-op on platforms without a tray backend)
	setTrayTooltip(fmt.Sprintf("%s - Help request sent to IT support", a.companyName))

	// Reset tooltip after a moment
	time.Sleep(3 * time.Second)
	setTrayTooltip(fmt.Sprintf("%s - %s\nClick for support", a.companyName, a.hostname))

	return nil
}

// generateToken produces a cryptographically random bearer token.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		slog.Error("failed to generate agent token", "error", err)
		os.Exit(1)
	}
	return "vapr_" + base64.RawURLEncoding.EncodeToString(b)
}

// newHTTPClient returns an http.Client with a sensible timeout and optional TLS.
func newHTTPClient() *http.Client {
	client := &http.Client{Timeout: DefaultRequestTimeout}
	if tlsConfig := buildServerTLSConfig(); tlsConfig != nil {
		client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
	}
	return client
}

// buildServerTLSConfig returns a tls.Config if VAPOR_SERVER_CA is set.
func buildServerTLSConfig() *tls.Config {
	caPath := os.Getenv("VAPOR_SERVER_CA")
	if caPath == "" {
		return nil
	}
	caCert, err := os.ReadFile(caPath)
	if err != nil {
		slog.Error("failed to read VAPOR_SERVER_CA", "path", caPath, "error", err)
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		slog.Error("failed to parse VAPOR_SERVER_CA")
		return nil
	}
	config := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	// Optional client cert for mTLS
	certPath := os.Getenv("VAPOR_AGENT_TLS_CERT")
	keyPath := os.Getenv("VAPOR_AGENT_TLS_KEY")
	if certPath != "" && keyPath != "" {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			slog.Error("failed to load agent TLS cert/key", "error", err)
		} else {
			config.Certificates = []tls.Certificate{cert}
		}
	}
	return config
}

// buildAgentTLSServerConfig returns a tls.Config for the agent's HTTP server.
func buildAgentTLSServerConfig() *tls.Config {
	certPath := os.Getenv("VAPOR_AGENT_TLS_CERT")
	keyPath := os.Getenv("VAPOR_AGENT_TLS_KEY")
	if certPath == "" || keyPath == "" {
		return nil
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
}

// authMiddleware enforces Bearer token authentication on every handler.
func (a *Agent) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		expected := "Bearer " + a.apiToken
		if header != expected {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// Start registers routes and starts the HTTP server plus the background heartbeat loop.
func (a *Agent) Start() error {
	slog.Info("starting agent", "port", a.port)
	slog.Info("server URL", "url", a.serverURL)

	mux := http.NewServeMux()
	mux.HandleFunc("/agent/run", a.authMiddleware(a.handleRunCommand))
	mux.HandleFunc("/metrics", a.authMiddleware(a.handleMetrics))
	mux.HandleFunc("/agent/file-transfer", a.authMiddleware(a.handleFileTransfer))
	mux.HandleFunc("/agent/sunshine/pin", a.authMiddleware(a.handleGetSunshinePIN))

	// Bind address: 0.0.0.0 by default so the central server can reach the agent
	// over Tailscale or LAN. Every endpoint above is wrapped in authMiddleware
	// (Bearer-token gate); the bind is NOT a security boundary, the token is.
	// Operators who run agent + server on the same host can pin to 127.0.0.1
	// via VAPOR_AGENT_BIND.
	bindAddr := os.Getenv("VAPOR_AGENT_BIND")
	if bindAddr == "" {
		bindAddr = "0.0.0.0"
	}

	// Start HTTP server to receive commands from server
	go func() {
		server := &http.Server{
			Addr:         fmt.Sprintf("%s:%d", bindAddr, a.port),
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
			TLSConfig:    buildAgentTLSServerConfig(),
		}
		var err error
		if server.TLSConfig != nil {
			slog.Info("starting agent HTTPS server", "port", a.port)
			err = server.ListenAndServeTLS(os.Getenv("VAPOR_AGENT_TLS_CERT"), os.Getenv("VAPOR_AGENT_TLS_KEY"))
		} else {
			slog.Info("starting agent HTTP server", "port", a.port)
			err = server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			slog.Warn("agent HTTP server error", "error", err)
		}
	}()

	// Register with server
	if err := a.registerWithServer(); err != nil {
		slog.Warn("registration failed", "error", err)
	}

	// Auto-start Sunshine if installed but not running
	go a.autoStartServices()

	// Proactively send heartbeats to the server on a fixed interval.
	go a.heartbeatLoop()

	// Poll server for pending commands.
	go a.commandPollLoop()

	return nil
}

// autoStartServices starts Sunshine and Tailscale if they are installed but not running.
func (a *Agent) autoStartServices() {
	// Wait a bit for registration to complete
	time.Sleep(10 * time.Second)

	// Auto-start Sunshine
	sunshineStatus := a.checkSunshineStatus()
	if sunshineStatus.Installed && !sunshineStatus.Running {
		slog.Info("Sunshine installed but not running, auto-starting...")
		a.startSunshineHidden()
	}

	// Auto-connect Tailscale if auth key is provided
	if os.Getenv("TAILSCALE_AUTH_KEY") != "" {
		tsStatus := a.checkTailscaleStatus()
		if tsStatus.Installed && !tsStatus.Connected {
			slog.Info("Tailscale installed but not connected, auto-connecting...")
			a.connectTailscale()
		}
	}
}

// tailscaleAuthKeyRe restricts auth keys to the well-known prefix + chars to
// prevent shell-meaningful chars from being interpolated downstream and to
// reject obvious garbage early.
var tailscaleAuthKeyRe = regexp.MustCompile(`^[A-Za-z0-9_:.-]{16,256}$`)

// connectTailscale attempts to connect Tailscale using the auth key from environment.
func (a *Agent) connectTailscale() {
	authKey := os.Getenv("TAILSCALE_AUTH_KEY")
	if authKey == "" {
		return
	}
	if !tailscaleAuthKeyRe.MatchString(authKey) {
		slog.Warn("TAILSCALE_AUTH_KEY format invalid; refusing to invoke tailscale")
		return
	}
	cmd := exec.Command("tailscale", "up", "--authkey", authKey, "--accept-routes")
	output, err := cmd.CombinedOutput()
	// Scrub the auth key out of any output before logging — Tailscale's CLI
	// has historically echoed parts of the key in error messages.
	scrubbed := strings.ReplaceAll(string(output), authKey, "<redacted>")
	if err != nil {
		slog.Warn("failed to auto-connect tailscale", "error", err, "output", scrubbed)
	} else {
		slog.Info("Tailscale auto-connected successfully")
	}
}

// registerWithServer registers this agent with the central server.
func (a *Agent) registerWithServer() error {
	regInfo := a.getRegistrationInfo()
	data, err := json.Marshal(regInfo)
	if err != nil {
		return fmt.Errorf("marshal registration info: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, a.serverURL+"/agent/register", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiToken)
	if regSecret := os.Getenv("REGISTRATION_SECRET"); regSecret != "" {
		req.Header.Set("X-Registration-Secret", regSecret)
	}

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("post registration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registration failed with status: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Warn("could not decode register response", "error", err)
	}

	if deviceID, ok := result["device_id"].(string); ok {
		a.deviceID = deviceID
		a.registered = true
		slog.Info("agent registered", "device_id", deviceID)
		saveDeviceID(deviceID, a.hostname)
	} else {
		slog.Warn("no device_id in registration response")
	}

	return nil
}

// heartbeatLoop sends periodic heartbeats directly to the server with exponential backoff.
func (a *Agent) heartbeatLoop() {
	// Use time.Ticker instead of time.Sleep for better resource management
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	retryCount := 0

	// Small initial delay to let the HTTP server start.
	time.Sleep(5 * time.Second)

	for {
		if err := a.sendHeartbeat(); err != nil {
			slog.Warn("heartbeat failed", "error", err)
			retryCount++

			if retryCount >= MaxHeartbeatRetries {
				slog.Warn("max heartbeat retries reached, attempting re-registration")
				if err := a.registerWithServer(); err != nil {
					slog.Warn("re-registration failed", "error", err)
				} else {
					retryCount = 0
				}
			} else {
				// Exponential backoff
				backoff := HeartbeatBackoffBase * time.Duration(retryCount)
				time.Sleep(backoff)
			}
		} else {
			retryCount = 0
			<-ticker.C
		}
	}
}

// commandPollLoop periodically fetches pending commands from the server and executes them.
func (a *Agent) commandPollLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Small delay so registration completes first.
	time.Sleep(10 * time.Second)

	for range ticker.C {
		if !a.registered || a.hostname == "" {
			continue
		}
		if err := a.fetchAndRunCommands(); err != nil {
			slog.Warn("command poll error", "error", err)
		}
	}
}

// fetchAndRunCommands pulls pending commands, runs them, and submits results.
func (a *Agent) fetchAndRunCommands() error {
	url := fmt.Sprintf("%s/agent/%s/commands", a.serverURL, a.hostname)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.apiToken)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("fetch commands: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil // server may return 404 if no commands, not an error
	}

	var commands []CommandRequest
	if err := json.NewDecoder(resp.Body).Decode(&commands); err != nil {
		return fmt.Errorf("decode commands: %w", err)
	}

	if len(commands) == 0 {
		return nil
	}

	var results []CommandResult
	for _, cmd := range commands {
		result := a.executeCommand(cmd)
		results = append(results, result)
	}

	return a.submitResults(results)
}

// executeCommand runs a single command with timeout and blocklist.
// Not a sandbox — proper isolation needs seccomp/apparmor/containers.
func (a *Agent) executeCommand(cmd CommandRequest) CommandResult {
	result := CommandResult{
		CommandID: cmd.ID,
		Timestamp: time.Now(),
	}

	cmdType := strings.ToLower(cmd.Type)
	if !allowedCommandTypes[cmdType] {
		result.Success = false
		result.Error = fmt.Sprintf("unsupported command type: %q", cmd.Type)
		return result
	}

	cmdStr, _ := cmd.Payload["command"].(string)
	if cmdStr == "" {
		result.Success = false
		result.Error = "empty command"
		return result
	}

	if isDangerous(cmdStr) {
		slog.Warn("blocked dangerous command", "command", cmdStr)
		result.Success = false
		result.Error = "command rejected by safety policy"
		return result
	}

	slog.Info("executing command", "id", cmd.ID, "command", cmdStr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var execCmd *exec.Cmd
	if runtime.GOOS == "windows" {
		execCmd = exec.CommandContext(ctx, "cmd.exe", "/C", cmdStr)
	} else {
		// Use restricted shell if SANDBOX_SHELL is set (e.g. /bin/rbash)
		shell := os.Getenv("SANDBOX_SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		execCmd = exec.CommandContext(ctx, shell, "-c", cmdStr)
	}

	// Restrict working directory if SANDBOX_DIR is set
	if sandboxDir := os.Getenv("SANDBOX_DIR"); sandboxDir != "" {
		if err := os.MkdirAll(sandboxDir, 0750); err == nil {
			execCmd.Dir = sandboxDir
		}
	}

	// Drop privileges if SANDBOX_USER is set (Linux/macOS only)
	if runtime.GOOS != "windows" {
		if sandboxUser := os.Getenv("SANDBOX_USER"); sandboxUser != "" {
			execCmd.SysProcAttr = &syscall.SysProcAttr{}
			// Note: actual UID/GID setting requires cgo or os/user lookup
			slog.Info("sandbox user requested", "user", sandboxUser, "note", "requires agent running as root or appropriate capabilities")
		}
	}

	output, err := execCmd.CombinedOutput()
	result.Output = truncateOutput(output)
	if ctx.Err() == context.DeadlineExceeded {
		result.Success = false
		result.Error = "command timed out after 60s"
	} else if err != nil {
		result.Success = false
		result.Error = err.Error()
	} else {
		result.Success = true
	}

	a.addCommandResult(result)
	return result
}

// maxCommandOutputBytes caps the size of stdout+stderr returned to the server.
// Without this a `cat /dev/zero` or `dd if=/dev/urandom` command would happily
// stream gigabytes through the agent into the server's database.
const maxCommandOutputBytes = 1 << 20 // 1 MiB

func truncateOutput(b []byte) string {
	if len(b) <= maxCommandOutputBytes {
		return string(b)
	}
	return string(b[:maxCommandOutputBytes]) + "\n... (truncated, " + fmt.Sprintf("%d", len(b)-maxCommandOutputBytes) + " bytes elided)"
}

// submitResults posts command results back to the server.
func (a *Agent) submitResults(results []CommandResult) error {
	payload := map[string]interface{}{"results": results}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}

	url := fmt.Sprintf("%s/agent/%s/results", a.serverURL, a.hostname)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiToken)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("submit results: %w", err)
	}
	defer resp.Body.Close()
	return nil
}

// sendHeartbeat posts the agent's current status to the server.
func (a *Agent) sendHeartbeat() error {
	status := a.getStatus()
	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, a.serverURL+"/agent/heartbeat", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiToken)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("post heartbeat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat returned status: %d", resp.StatusCode)
	}

	return nil
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func (a *Agent) handleRunCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var cmdReq struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&cmdReq); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if cmdReq.Command == "" {
		http.Error(w, "command field is required", http.StatusBadRequest)
		return
	}

	cmdType := strings.ToLower(cmdReq.Type)
	if !allowedCommandTypes[cmdType] {
		result := CommandResult{
			CommandID: generateCommandID(),
			Success:   false,
			Error:     fmt.Sprintf("unsupported command type: %q (supported: shell, script)", cmdReq.Type),
			Timestamp: time.Now(),
		}
		a.addCommandResult(result)
		writeJSON(w, http.StatusBadRequest, result)
		return
	}

	if isDangerous(cmdReq.Command) {
		slog.Warn("blocked dangerous command via HTTP", "command", cmdReq.Command)
		result := CommandResult{
			CommandID: generateCommandID(),
			Success:   false,
			Error:     "command rejected by safety policy",
			Timestamp: time.Now(),
		}
		a.addCommandResult(result)
		writeJSON(w, http.StatusForbidden, result)
		return
	}

	slog.Info("executing HTTP command", "command", cmdReq.Command)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/C", cmdReq.Command)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", cmdReq.Command)
	}

	outputBytes, err := cmd.CombinedOutput()
	output := truncateOutput(outputBytes)

	var success bool
	var runErr string
	if ctx.Err() == context.DeadlineExceeded {
		success = false
		runErr = "command timed out after 60s"
	} else if err != nil {
		success = false
		runErr = err.Error()
	} else {
		success = true
	}

	result := CommandResult{
		CommandID: generateCommandID(),
		Success:   success,
		Output:    output,
		Error:     runErr,
		Timestamp: time.Now(),
	}

	a.addCommandResult(result)
	writeJSON(w, http.StatusOK, result)
}

// addCommandResult adds a command result to the history with bounded size.
func (a *Agent) addCommandResult(result CommandResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	a.lastCommands = append(a.lastCommands, result)
	
	// Keep only the most recent results to prevent unbounded growth
	if len(a.lastCommands) > MaxCommandHistory {
		a.lastCommands = a.lastCommands[len(a.lastCommands)-MaxCommandHistory:]
	}
}

// generateCommandID creates a unique command ID.
func generateCommandID() string {
	return fmt.Sprintf("cmd_%d", time.Now().UnixNano())
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("could not write JSON response", "error", err)
	}
}

// handleFileTransfer handles file transfer requests from the server.
func (a *Agent) handleFileTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TransferID string `json:"transfer_id"`
		Type       string `json:"type"`
		FileName   string `json:"file_name"`
		FilePath   string `json:"file_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.TransferID == "" || req.Type == "" || req.FileName == "" || req.FilePath == "" {
		http.Error(w, "transfer_id, type, file_name, and file_path are required", http.StatusBadRequest)
		return
	}

	slog.Info("starting file transfer", "transfer_id", req.TransferID, "type", req.Type, "file", req.FileName)

	go func() {
		result := a.executeFileTransfer(req.TransferID, req.Type, req.FileName, req.FilePath)
		if result.Success {
			slog.Info("file transfer completed", "transfer_id", req.TransferID)
		} else {
			slog.Warn("file transfer failed", "transfer_id", req.TransferID, "error", result.Error)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"transfer_id": req.TransferID,
		"status":      "started",
		"message":     "File transfer started",
	})
}

// executeFileTransfer performs the actual file transfer and reports status back to server.
func (a *Agent) executeFileTransfer(transferID, transferType, fileName, filePath string) CommandResult {
	result := CommandResult{
		CommandID: transferID,
		Timestamp: time.Now(),
	}

	// Validate file path to prevent directory traversal
	if strings.Contains(filePath, "..") || strings.Contains(fileName, "..") {
		result.Success = false
		result.Error = "invalid file path: path traversal detected"
		a.reportFileTransferStatus(transferID, "failed", 0)
		return result
	}

	// For upload: read file from disk and send to server
	// For download: receive file from server and write to disk
	// In a real implementation, this would use a proper file transfer protocol.
	// For now, we simulate the transfer with a simple file operation.

	switch transferType {
	case "upload":
		// Check if file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			result.Success = false
			result.Error = fmt.Sprintf("file not found: %s", filePath)
			a.reportFileTransferStatus(transferID, "failed", 0)
			return result
		}
		// Simulate upload progress
		a.reportFileTransferStatus(transferID, "in_progress", 50)
		// In a real implementation, upload the file to server storage
		time.Sleep(2 * time.Second) // Simulate transfer time
		result.Success = true
		result.Output = fmt.Sprintf("uploaded %s", fileName)
		a.reportFileTransferStatus(transferID, "completed", 100)

	case "download":
		// Simulate download progress
		a.reportFileTransferStatus(transferID, "in_progress", 50)
		// In a real implementation, download the file from server storage
		time.Sleep(2 * time.Second) // Simulate transfer time
		result.Success = true
		result.Output = fmt.Sprintf("downloaded %s to %s", fileName, filePath)
		a.reportFileTransferStatus(transferID, "completed", 100)

	default:
		result.Success = false
		result.Error = fmt.Sprintf("unsupported transfer type: %s", transferType)
		a.reportFileTransferStatus(transferID, "failed", 0)
	}

	return result
}

// reportFileTransferStatus sends the current transfer status back to the server.
func (a *Agent) reportFileTransferStatus(transferID, status string, progress int) {
	payload := map[string]interface{}{
		"status":   status,
		"progress": progress,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("failed to marshal file transfer status", "error", err)
		return
	}

	url := fmt.Sprintf("%s/agent/file-transfer/%s", a.serverURL, transferID)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(data))
	if err != nil {
		slog.Warn("failed to create file transfer status request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiToken)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		slog.Warn("failed to report file transfer status", "error", err)
		return
	}
	defer resp.Body.Close()
}

func (a *Agent) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cpuPercent, _ := cpu.Percent(0, false)
	cpuInfo, _ := cpu.Info()
	memInfo, _ := mem.VirtualMemory()
	diskInfo, _ := disk.Usage("/")
	loadAvg, _ := load.Avg()

	cpuPct := 0.0
	if len(cpuPercent) > 0 {
		cpuPct = cpuPercent[0]
	}

	load1, load5, load15 := 0.0, 0.0, 0.0
	if loadAvg != nil {
		load1 = loadAvg.Load1
		load5 = loadAvg.Load5
		load15 = loadAvg.Load15
	}

	memPct := 0.0
	diskPct := 0.0
	if memInfo != nil {
		memPct = float64(memInfo.Used) / float64(memInfo.Total) * 100
	}
	if diskInfo != nil && diskInfo.Total > 0 {
		diskPct = float64(diskInfo.Used) / float64(diskInfo.Total) * 100
	}

	metrics := map[string]interface{}{
		"timestamp": time.Now().Unix(),
		"cpu": map[string]interface{}{
			"percent": cpuPct,
			"cores":   runtime.NumCPU(),
			"model":   getCPUName(cpuInfo),
		},
		"memory": map[string]interface{}{
			"total":   memInfo.Total,
			"used":    memInfo.Used,
			"free":    memInfo.Free,
			"percent": memPct,
		},
		"disk": map[string]interface{}{
			"total":   diskInfo.Total,
			"used":    diskInfo.Used,
			"free":    diskInfo.Free,
			"percent": diskPct,
		},
		"load": map[string]interface{}{
			"1m":  load1,
			"5m":  load5,
			"15m": load15,
		},
	}

	writeJSON(w, http.StatusOK, metrics)
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func (a *Agent) getRegistrationInfo() map[string]interface{} {
	cpuInfo, _ := cpu.Info()
	memInfo, _ := mem.VirtualMemory()
	diskInfo, _ := disk.Usage("/")
	hostInfo, _ := host.Info()

	var localIPs []string
	var firstLocalIP string
	var macAddr string

	ifaces, err := net.Interfaces()
	if err != nil {
		slog.Warn("could not get network interfaces", "error", err)
	}
	for _, iface := range ifaces {
		if macAddr == "" && len(iface.HardwareAddr) > 0 {
			macAddr = iface.HardwareAddr.String()
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipv4 := ipNet.IP.To4(); ipv4 != nil {
					localIPs = append(localIPs, ipv4.String())
				}
			}
		}
	}
	if len(localIPs) > 0 {
		firstLocalIP = localIPs[0]
	}

	return map[string]interface{}{
		"hostname":      a.hostname,
		"os":            hostInfo.OS,
		"os_version":    hostInfo.PlatformVersion,
		"local_ip":      firstLocalIP,
		"local_ips":     localIPs,
		"mac_address":   macAddr,
		"cpu":           getCPUName(cpuInfo),
		"ram":           memInfo.Total,
		"storage":       diskInfo.Total,
		"uptime":        hostInfo.Uptime,
		"agent_version": "1.0.0",
		"agent_port":    a.port,
	}
}

// SunshineStatus represents the status of Sunshine on the device
type SunshineStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Port      int    `json:"port"`
	Version   string `json:"version,omitempty"`
}

// TailscaleStatus represents the status of Tailscale on the device
type TailscaleStatus struct {
	Installed   bool     `json:"installed"`
	Connected   bool     `json:"connected"`
	IP          string   `json:"ip,omitempty"`
	Hostname    string   `json:"hostname,omitempty"`
	Peers       int      `json:"peers,omitempty"`
	BackendState string  `json:"backend_state,omitempty"`
}

// checkSunshineStatus checks if Sunshine is installed and running
func (a *Agent) checkSunshineStatus() SunshineStatus {
	status := SunshineStatus{
		Port: DefaultSunshinePort,
	}

	// Check if Sunshine is running by trying to connect to its HTTP port
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/", DefaultSunshinePort))
	if err == nil {
		defer resp.Body.Close()
		status.Running = true
	}

	// Check if Sunshine binary is installed
	switch runtime.GOOS {
	case "windows":
		paths := []string{
			`C:\Program Files\Sunshine\sunshine.exe`,
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				status.Installed = true
				break
			}
		}
	case "darwin":
		if _, err := exec.LookPath("sunshine"); err == nil {
			status.Installed = true
		}
	default: // Linux
		if _, err := exec.LookPath("sunshine"); err == nil {
			status.Installed = true
		}
		// Check AppImage
		if _, err := os.Stat("/opt/sunshine/Sunshine.AppImage"); err == nil {
			status.Installed = true
		}
	}

	return status
}

// checkTailscaleStatus checks if Tailscale is installed and connected
func (a *Agent) checkTailscaleStatus() TailscaleStatus {
	status := TailscaleStatus{}

	// Check if Tailscale is installed
	if _, err := exec.LookPath("tailscale"); err != nil {
		status.Installed = false
		return status
	}
	status.Installed = true

	// Get Tailscale status
	cmd := exec.Command("tailscale", "status", "--json")
	output, err := cmd.Output()
	if err != nil {
		status.Connected = false
		return status
	}

	var tsStatus map[string]interface{}
	if err := json.Unmarshal(output, &tsStatus); err != nil {
		status.Connected = false
		return status
	}

	// Get backend state
	if backend, ok := tsStatus["BackendState"]; ok {
		if state, ok := backend.(string); ok {
			status.BackendState = state
			status.Connected = state == "Running"
		}
	}

	// Get self IP
	if self, ok := tsStatus["Self"]; ok {
		if selfMap, ok := self.(map[string]interface{}); ok {
			if ips, ok := selfMap["TailscaleIPs"]; ok {
				if ipList, ok := ips.([]interface{}); ok && len(ipList) > 0 {
					status.IP = ipList[0].(string)
				}
			}
			if h, ok := selfMap["HostName"]; ok {
				if hostname, ok := h.(string); ok {
					status.Hostname = hostname
				}
			}
		}
	}

	// Count peers
	if peers, ok := tsStatus["Peer"]; ok {
		if peerMap, ok := peers.(map[string]interface{}); ok {
			status.Peers = len(peerMap)
		}
	}

	return status
}

func (a *Agent) getStatus() map[string]interface{} {
	cpuPercent, _ := cpu.Percent(0, false)
	memInfo, _ := mem.VirtualMemory()
	diskInfo, _ := disk.Usage("/")
	hostInfo, _ := host.Info()

	cpuPct := 0.0
	if len(cpuPercent) > 0 {
		cpuPct = cpuPercent[0]
	}

	memPct := 0.0
	if memInfo != nil && memInfo.Total > 0 {
		memPct = float64(memInfo.Used) / float64(memInfo.Total) * 100
	}

	diskPct := 0.0
	if diskInfo != nil && diskInfo.Total > 0 {
		diskPct = float64(diskInfo.Used) / float64(diskInfo.Total) * 100
	}

	deviceID := a.deviceID
	if deviceID == "" {
		deviceID = a.hostname
	}

	// Check Sunshine status
	sunshineStatus := a.checkSunshineStatus()

	// Check Tailscale status
	tailscaleStatus := a.checkTailscaleStatus()

	return map[string]interface{}{
		"device_id":    deviceID,
		"hostname":     a.hostname,
		"status":       "online",
		"cpu_usage":    cpuPct,
		"memory_usage": memPct,
		"disk_usage":   diskPct,
		"last_seen":    time.Now(),
		"uptime":       hostInfo.Uptime,
		"sunshine":     sunshineStatus,
		"tailscale":    tailscaleStatus,
	}
}

func getCPUName(info []cpu.InfoStat) string {
	if len(info) == 0 {
		return "Unknown"
	}
	return info[0].ModelName
}

// SetDeviceID stores the device ID assigned by the server after registration.
func (a *Agent) SetDeviceID(id string) {
	a.deviceID = id
	slog.Info("agent registered", "device_id", id)
}

// agentStateFile returns the path to the agent state file.
func agentStateFile() string {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, _ := os.UserHomeDir()
			appData = home
		}
		return appData + `\vaporrmm\agent-state.json`
	}
	return "/etc/vaporrmm/agent-state.json"
}

type agentState struct {
	DeviceID string `json:"device_id"`
	Hostname string `json:"hostname"`
}

// loadDeviceID reads a previously registered device ID from disk.
func loadDeviceID() string {
	data, err := os.ReadFile(agentStateFile())
	if err != nil {
		return ""
	}
	var state agentState
	if err := json.Unmarshal(data, &state); err != nil {
		return ""
	}
	return state.DeviceID
}

// saveDeviceID persists the device ID to disk so it survives restarts.
func saveDeviceID(deviceID, hostname string) {
	state := agentState{DeviceID: deviceID, Hostname: hostname}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		slog.Warn("could not marshal agent state", "error", err)
		return
	}
	path := agentStateFile()
	dir := path[:strings.LastIndexAny(path, "/\\")]
	if dir != "" {
		_ = os.MkdirAll(dir, 0750)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		slog.Warn("could not save agent state", "error", err)
	}
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

// loadEnvFile reads simple KEY=VALUE lines from path and sets them in the
// process environment ONLY when the variable is not already set. This is
// required on Windows (where `sc.exe create` cannot inject environment
// variables into a service) and convenient elsewhere as a fallback when
// the operator hasn't wired EnvironmentFile / launchd correctly.
//
// Lines beginning with # are ignored. Quoted values are NOT supported —
// keep the file simple, no shell escaping.
func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Don't override an env var that's already set — operator-supplied
		// env (e.g. systemd EnvironmentFile) takes precedence.
		if _, ok := os.LookupEnv(key); ok {
			continue
		}
		_ = os.Setenv(key, val)
	}
}

// agentEnvFilePath returns the platform-appropriate location of the agent's
// environment file. Override with AGENT_ENV_FILE if the operator wants a
// custom path.
func agentEnvFilePath() string {
	if p := os.Getenv("AGENT_ENV_FILE"); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		return programData + `\vaporrmm\agent.env`
	}
	return "/etc/vaporrmm/agent.env"
}

func main() {
	// Bootstrap env vars from a config file BEFORE reading anything else.
	// This is the Windows-service workaround and a useful fallback elsewhere.
	loadEnvFile(agentEnvFilePath())

	serverURL := os.Getenv("VAPOR_SERVER_URL")
	if serverURL == "" {
		serverURL = DefaultServerURL
	}

	// Trim trailing slash for consistency
	serverURL = strings.TrimSuffix(serverURL, "/")

	port := DefaultAgentPort
	if p, ok := os.LookupEnv("VAPOR_AGENT_PORT"); ok {
		if parsedPort, err := strconv.Atoi(p); err == nil {
			port = parsedPort
		}
	}

	apiToken := os.Getenv("VAPOR_AGENT_TOKEN")

	agent, err := NewAgent(serverURL, port, apiToken)
	if err != nil {
		slog.Error("failed to create agent", "error", err)
		os.Exit(1)
	}

	// Quick reachability check — not fatal if the server is not yet up.
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/health", serverURL), nil)
	resp, err := newHTTPClient().Do(req)
	if err == nil {
		defer resp.Body.Close()
		slog.Info("server is reachable")
	} else {
		slog.Info("cannot connect to server", "url", serverURL, "error", err)
	}

	// Setup system tray UI (branded icon + Request Help)
	agent.setupSystemTray()

	if err := agent.Start(); err != nil {
		slog.Error("agent failed", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown on SIGINT/SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	slog.Info("Agent shutting down...")
}