package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
)

// Config holds agent configuration loaded from file
type Config struct {
	ServerURL         string `mapstructure:"server_url"`
	TailscaleIP       string `mapstructure:"tailscale_ip"`
	MachineID         string `mapstructure:"machine_id"`
	PairingToken      string `mapstructure:"pairing_token"`
	AutoStartSunshine bool   `mapstructure:"auto_start_sunshine"`
	SunshinePath      string `mapstructure:"sunshine_path"`
	ConfigDir         string
	LogFile           string
}

// AgentStatus represents the current state of the agent
type AgentStatus struct {
	ClientID       string    `json:"client_id"`
	Connected      bool      `json:"connected"`
	Status         string    `json:"status"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
	Version        string    `json:"version"`
	MachineID      string    `json:"machine_id"`
	TailscaleIP    string    `json:"tailscale_ip,omitempty"`
	SunshineStatus *SunshineStatus `json:"sunshine_status,omitempty"`
}

// SunshineStatus represents the current status from Sunshine API
type SunshineStatus struct {
	Version        string   `json:"version"`
	PublicKey      string   `json:"public_key"`
	Name           string   `json:"name"`
	LocalIPs       []string `json:"local_ips"`
	ConnectionUUID string   `json:"connection_uuid"`
	ActiveClients  int      `json:"active_clients"`
}

// Agent represents the main agent struct
type Agent struct {
	config     Config
	clientID   string
	mu         sync.RWMutex
	status     AgentStatus
	httpClient *http.Client
}

const (
	Version       = "0.1.0"
	ConfigFileName  = "vapor-agent"
	SunshinePort    = "47990"
)

// NewAgent creates a new agent instance
func NewAgent(config Config) *Agent {
	return &Agent{
		config:     config,
		clientID:   generateClientID(),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		status: AgentStatus{
			Version:     Version,
			MachineID:   config.MachineID,
			TailscaleIP: config.TailscaleIP,
		},
	}
}

// generateClientID creates a unique client ID
func generateClientID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// loadConfig loads configuration from file and environment variables
func loadConfig() Config {
	v := viper.New()
	
	// Set defaults
	v.SetDefault("server_url", "http://localhost:3001")
	v.SetDefault("auto_start_sunshine", true)
	v.SetDefault("sunshine_path", "")
	v.SetDefault("log_file", "")
	v.SetDefault("config_dir", getConfigDir())
	
	// Determine config file path
	configPath := filepath.Join(v.GetString("config_dir"), ConfigFileName)
	if envConfig := os.Getenv("VAPOR_CONFIG"); envConfig != "" {
		configPath = envConfig
	}
	
	// Set config file type
	v.SetConfigType("yaml")
	
	// Read config file if exists
	if err := v.ReadInConfig(); err == nil {
		log.Printf("Loaded config from: %s", v.ConfigFileUsed())
	} else if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
		log.Printf("Warning: Could not read config file: %v", err)
	}
	
	// Override with environment variables
	if url := os.Getenv("VAPOR_SERVER_URL"); url != "" {
		v.Set("server_url", url)
	}
	if token := os.Getenv("VAPOR_PAIRING_TOKEN"); token != "" {
		v.Set("pairing_token", token)
	}
	if machineID := os.Getenv("VAPOR_MACHINE_ID"); machineID != "" {
		v.Set("machine_id", machineID)
	}
	
	var config Config
	if err := v.Unmarshal(&config); err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}
	
	return config
}

// getConfigDir returns the platform-specific config directory
func getConfigDir() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "vaporRMM")
	default:
		return filepath.Join(homeDir(), ".config", "vaporrmm")
	}
}

// homeDir returns the user's home directory
func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	// Fallback for Windows
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		return filepath.Join(os.Getenv("HOMEDRIVE"), os.Getenv("HOMEPATH"))
	}
	return userProfile
}

// getMachineID returns the system's unique machine ID
func getMachineID() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return getWindowsMachineID()
	case "darwin":
		return getDarwinMachineID()
	default:
		return getLinuxMachineID()
	}
}

// getLinuxMachineID tries multiple methods to get Linux machine ID
func getLinuxMachineID() (string, error) {
	// Try /etc/machine-id first
	if id, err := os.ReadFile("/etc/machine-id"); err == nil && len(strings.TrimSpace(string(id))) > 0 {
		return strings.TrimSpace(string(id)), nil
	}
	
	// Try systemd's machine-id
	if id, err := os.ReadFile("/var/lib/dbus/machine-id"); err == nil && len(strings.TrimSpace(string(id))) > 0 {
		return strings.TrimSpace(string(id)), nil
	}
	
	// Generate a fallback ID
	b := make([]byte, 16)
	rand.Read(b)
	id := base64.URLEncoding.EncodeToString(b)
	return id, nil
}

// getWindowsMachineID gets Windows machine ID via WMI or registry
func getWindowsMachineID() (string, error) {
	// Try to read from registry (requires admin on some systems)
	return "", fmt.Errorf("not implemented")
}

// getDarwinMachineID gets macOS machine ID
func getDarwinMachineID() (string, error) {
	return "", fmt.Errorf("not implemented")
}

// isSunshineRunning checks if Sunshine is running by attempting to connect to its API
func (a *Agent) isSunshineRunning() bool {
	resp, err := a.httpClient.Get("http://127.0.0.1:" + SunshinePort + "/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// startSunshine attempts to start the Sunshine server
func (a *Agent) startSunshine() error {
	path := a.config.SunshinePath
	
	switch runtime.GOOS {
	case "windows":
		// Try to start Sunshine service or executable
		if path == "" {
			path = "C:\\Program Files\\Sunshine\\sunshine.exe"
		}
		cmd := exec.Command(path, "--daemon")
		return cmd.Start()
		
	default:
		// For Linux/macOS, try systemctl first
		cmd := exec.Command("systemctl", "start", "sunshine")
		if err := cmd.Run(); err == nil {
			return nil
		}
		
		// Fall back to direct execution if no service
		if path != "" {
			cmd = exec.Command(path, "--daemon")
			return cmd.Start()
		}
		
		return fmt.Errorf("Sunshine not installed and no path specified")
	}
}

// ensureSunshineRunning checks and starts Sunshine if configured
func (a *Agent) ensureSunshineRunning() error {
	if !a.config.AutoStartSunshine {
		return nil
	}
	
	if a.isSunshineRunning() {
		log.Println("Sunshine is already running")
		return nil
	}
	
	log.Println("Starting Sunshine server...")
	return a.startSunshine()
}

// getSunshineStatus fetches status from local Sunshine API
func (a *Agent) getSunshineStatus() (*SunshineStatus, error) {
	resp, err := a.httpClient.Get("http://127.0.0.1:" + SunshinePort + "/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var status SunshineStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("failed to parse Sunshine response: %v", err)
	}

	return &status, nil
}

// registerWithServer registers the agent with the central server
func (a *Agent) registerWithServer() error {
	machineID, _ := getMachineID()
	
	status, err := a.getSunshineStatus()
	if err != nil {
		log.Printf("Warning: Could not fetch Sunshine status during registration: %v", err)
	}
	
	payload := map[string]interface{}{
		"machine_id":    machineID,
		"client_id":     a.clientID,
		"tailscale_ip":  a.config.TailscaleIP,
		"version":       Version,
		"name":          status.Name,
	}

	if status != nil {
		payload["sunshine_version"] = status.Version
		payload["public_key"] = status.PublicKey
	}

	if a.config.PairingToken != "" {
		payload["pairing_token"] = a.config.PairingToken
	}

	jsonData, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", a.config.ServerURL+"/agents/register", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to register: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		log.Println("Agent registered with server successfully")
	} else if resp.StatusCode == 401 {
		log.Printf("Registration requires pairing token. Agent not yet paired.")
		return fmt.Errorf("unauthorized - invalid or missing pairing token")
	}

	return nil
}

// sendHeartbeat sends heartbeat to the central server
func (a *Agent) sendHeartbeat() error {
	machineID, _ := getMachineID()
	
	sunshineStatus, err := a.getSunshineStatus()
	if err != nil {
		log.Printf("Warning: Could not fetch Sunshine status: %v", err)
	}

	a.mu.Lock()
	a.status = AgentStatus{
		ClientID:    a.clientID,
		Connected:   true,
		Status:      "online",
		LastHeartbeat: time.Now(),
		Version:     Version,
		MachineID:   machineID,
		TailscaleIP: a.config.TailscaleIP,
		SunshineStatus: sunshineStatus,
	}
	a.mu.Unlock()

	jsonData, _ := json.Marshal(a.status)

	req, err := http.NewRequest("POST", a.config.ServerURL+"/agents/heartbeat", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.config.PairingToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.config.PairingToken)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send heartbeat: %v", err)
	}
	defer resp.Body.Close()

	log.Println("Heartbeat sent successfully")
	return nil
}

// handleMetrics returns Prometheus metrics
func (a *Agent) handleMetrics(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	metrics := fmt.Sprintf(`# HELP vapor_agent_uptime_seconds Agent uptime in seconds
# TYPE vapor_agent_uptime_seconds gauge
vapor_agent_uptime_seconds %d
# HELP vapor_agent_connected Whether agent is connected to server
# TYPE vapor_agent_connected gauge
vapor_agent_connected %t
# HELP vapor_agent_sunshine_running Whether Sunshine is running locally
# TYPE vapor_agent_sunshine_running gauge
vapor_agent_sunshine_running %t
`,
		int64(time.Since(startTime).Seconds()),
		a.status.Connected,
		a.isSunshineRunning(),
	)

	if a.status.SunshineStatus != nil {
		metrics += fmt.Sprintf(`# HELP vapor_sunshine_active_clients Number of active Sunshine clients
# TYPE vapor_sunshine_active_clients gauge
vapor_sunshine_active_clients %d
`,
			a.status.SunshineStatus.ActiveClients,
		)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, metrics)
}

// handleStatus returns agent status as JSON
func (a *Agent) handleStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.status)
}

// startMetricsServer starts the HTTP server for metrics and status endpoints
func (a *Agent) startMetricsServer() error {
	http.HandleFunc("/metrics", a.handleMetrics)
	http.HandleFunc("/status", a.handleStatus)

	log.Println("Starting metrics server on :8080")
	return http.ListenAndServe(":8080", nil)
}

// runForever runs the agent's main loop
func (a *Agent) runForever() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := a.sendHeartbeat(); err != nil {
			log.Printf("Heartbeat failed: %v", err)
		}
		
		// Check Sunshine every minute
		if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
			if !a.isSunshineRunning() && a.config.AutoStartSunshine {
				go func() {
					if err := a.startSunshine(); err != nil {
						log.Printf("Failed to auto-start Sunshine: %v", err)
					}
				}()
			}
		}
	}
}

var startTime = time.Now()

func main() {
	fmt.Println(" vaporRMM Agent v" + Version)
	fmt.Println("=========================")

	config := loadConfig()
	
	log.Printf("Configuration loaded from: %s", config.ConfigDir)
	log.Printf("Server URL: %s", config.ServerURL)
	log.Printf("Machine ID: %s", config.MachineID)
	if config.TailscaleIP != "" {
		log.Printf("Tailscale IP: %s", config.TailscaleIP)
	}

	agent := NewAgent(config)

	// Ensure Sunshine is running
	if err := agent.ensureSunshineRunning(); err != nil {
		log.Printf("Warning: Could not ensure Sunshine is running: %v", err)
	} else {
		log.Println("Sunshine is available")
	}

	// Try to register with server (non-blocking if fails)
	go func() {
		time.Sleep(2 * time.Second) // Wait a bit for network
		if err := agent.registerWithServer(); err != nil {
			log.Printf("Registration info: %v", err)
		}
	}()

	// Start metrics server in background
	go func() {
		if err := agent.startMetricsServer(); err != nil {
			log.Fatalf("Failed to start metrics server: %v", err)
		}
	}()

	fmt.Println("Agent running. Press Ctrl+C to stop.")
	
	// Run main loop
	agent.runForever()
}