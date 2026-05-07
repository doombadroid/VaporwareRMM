package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config holds the agent configuration written to disk.
type Config struct {
	ServerURL string `json:"server_url"`
	AgentID   string `json:"agent_id"`
	AgentKey  string `json:"agent_key"`
	Name      string `json:"name,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "install-agent":
		runInstallAgent()
	case "register":
		runRegister()
	case "install-sunshine":
		runInstallSunshine()
	case "update-sunshine":
		runUpdateSunshine()
	case "sunshine-status":
		runSunshineStatus()
	case "install-tailscale":
		runInstallTailscale()
	case "tailscale-status":
		runTailscaleStatus()
	case "package":
		runPackage()
	case "deploy":
		runDeploy()
	case "docker-compose":
		runDockerCompose()
	case "uninstall":
		runUninstall()
	case "status":
		runStatus()
	case "db-migrate":
		runDBMigrate()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("vaporrmm - Vapor RMM Agent CLI")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  vaporrmm <command> [options]")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  install-agent     Install the Vapor RMM agent on a host")
	fmt.Println("  register          Register a new device with the server")
	fmt.Println("  install-sunshine  Install Sunshine for remote desktop access")
	fmt.Println("  update-sunshine   Update Sunshine configuration for remote access")
	fmt.Println("  sunshine-status   Check Sunshine installation and running status")
	fmt.Println("  install-tailscale Install Tailscale for secure remote access")
	fmt.Println("  tailscale-status  Check Tailscale installation and connection status")
	fmt.Println("  package           Generate installation packages (MSI/DEB/RPM/OpenRC)")
	fmt.Println("  deploy            Deploy vaporRMM to a new host")
  fmt.Println("  docker-compose    Generate Docker Compose configuration")
  fmt.Println("  uninstall         Remove the Vapor RMM agent")
  fmt.Println("  status            Show agent status")
  fmt.Println("  db-migrate        Trigger database migrations on the server")
  fmt.Println("  help              Show this help message")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  sudo vaporrmm install-agent --server https://rmm.example.com")
	fmt.Println("  vaporrmm register --name workstation-1 --tags office,desktop")
	fmt.Println("  vaporrmm update-sunshine --port 47990")
	fmt.Println("  vaporrmm package --type deb --output ./dist")
	fmt.Println("  vaporrmm package --type openrc --output ./dist")
}

// generateAgentID creates a random, URL-safe agent identifier.
func generateAgentID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating agent ID: %v\n", err)
		os.Exit(1)
	}
	return fmt.Sprintf("agent-%s", base64.URLEncoding.EncodeToString(b))
}

// generateSecureKey creates a random base64-encoded 32-byte key.
func generateSecureKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating secure key: %v\n", err)
		os.Exit(1)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	dest, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dest.Close()

	_, err = io.Copy(dest, source)
	return err
}

func canUseSystemd() bool {
	cmd := exec.Command("systemctl", "--version")
	return cmd.Run() == nil
}

func canUseOpenRC() bool {
	cmd := exec.Command("openrc", "--version")
	if cmd.Run() == nil {
		return true
	}
	// Also check for /sbin/openrc-run directly
	_, err := os.Stat("/sbin/openrc-run")
	return err == nil
}

// setupSystemdService installs the agent as a systemd service.
// The agent key is stored in a separate credentials file (not exposed in the
// unit's command line, which would be visible via `ps aux`).
func setupSystemdService(serverURL, agentID, agentKey string) {
	// Write credentials to a root-readable-only environment file.
	envContent := fmt.Sprintf("VAPOR_SERVER_URL=%s\nVAPOR_AGENT_TOKEN=%s\n", serverURL, agentKey)
	envPath := "/etc/vaporrmm/agent.env"
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		fmt.Printf("[!] Warning: Could not create environment file: %v\n", err)
		return
	}
	fmt.Println("[OK] Agent environment file created (0600)")

	serviceContent := fmt.Sprintf(`[Unit]
Description=Vapor RMM Agent
After=network.target

[Service]
Type=simple
EnvironmentFile=/etc/vaporrmm/agent.env
ExecStart=/usr/local/bin/vaporrmm-agent --agent-id %s
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, agentID)

	servicePath := "/etc/systemd/system/vaporrmm-agent.service"
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		fmt.Printf("[!] Warning: Could not create systemd service file: %v\n", err)
		return
	}
	fmt.Println("[OK] Systemd service created")

	if cmd := exec.Command("systemctl", "daemon-reload"); cmd.Run() == nil {
		fmt.Println("[OK] Systemd daemon reloaded")
	}
	if cmd := exec.Command("systemctl", "enable", "vaporrmm-agent.service"); cmd.Run() == nil {
		fmt.Println("[OK] Service enabled")
	}
}

// setupOpenRCService installs the agent as an OpenRC service.
func setupOpenRCService(serverURL, agentID, agentKey string) {
	// Write credentials to a root-readable-only environment file.
	envContent := fmt.Sprintf("VAPOR_SERVER_URL=%s\nVAPOR_AGENT_TOKEN=%s\n", serverURL, agentKey)
	envPath := "/etc/vaporrmm/agent.env"
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		fmt.Printf("[!] Warning: Could not create environment file: %v\n", err)
		return
	}
	fmt.Println("[OK] Agent environment file created (0600)")

	serviceContent := fmt.Sprintf(`#!/sbin/openrc-run
# Vapor RMM Agent OpenRC service

description="Vapor RMM Remote Monitoring Agent"
command="/usr/local/bin/vaporrmm-agent"
command_args="--agent-id %s"
command_background=true
pidfile="/run/${RC_SVCNAME}.pid"
output_log="/var/log/vaporrmm-agent.log"
error_log="/var/log/vaporrmm-agent.log"

depend() {
	need net
	after firewall
}

start_pre() {
	checkpath -f -m 0644 -o root:root "${output_log}"
}
`, agentID)

	servicePath := "/etc/init.d/vaporrmm-agent"
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0755); err != nil {
		fmt.Printf("[!] Warning: Could not create OpenRC service file: %v\n", err)
		return
	}
	fmt.Println("[OK] OpenRC service created")

	// Create conf.d file for environment
	confContent := fmt.Sprintf(`VAPOR_SERVER_URL=%s
VAPOR_AGENT_TOKEN=%s
`, serverURL, agentKey)
	confPath := "/etc/conf.d/vaporrmm-agent"
	if err := os.WriteFile(confPath, []byte(confContent), 0600); err != nil {
		fmt.Printf("[!] Warning: Could not create OpenRC config file: %v\n", err)
		return
	}
	fmt.Println("[OK] OpenRC config created")

	if cmd := exec.Command("rc-update", "add", "vaporrmm-agent", "default"); cmd.Run() == nil {
		fmt.Println("[OK] Service added to default runlevel")
	}
}

func getOSType() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	default:
		return "linux"
	}
}

func getOSName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	}
	// Try /etc/os-release for Linux distributions.
	if content, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
			}
		}
	}
	return "Linux"
}

// getArch returns the CPU architecture using runtime.GOARCH to avoid
// platform-specific shell commands that fail on Windows.
func getArch() string {
	return runtime.GOARCH
}

func getHostname() string {
	hostname, _ := os.Hostname()
	return hostname
}

// ============== Install Agent Command ==============

func runInstallAgent() {
	var serverURL string
	var agentName string
	installAsService := true

	for i := 2; i < len(os.Args); i++ {
		switch {
		case os.Args[i] == "--server" && i+1 < len(os.Args):
			serverURL = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--server="):
			serverURL = strings.TrimPrefix(os.Args[i], "--server=")
		case os.Args[i] == "--name" && i+1 < len(os.Args):
			agentName = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--name="):
			agentName = strings.TrimPrefix(os.Args[i], "--name=")
		case os.Args[i] == "--no-service":
			installAsService = false
		}
	}

	if serverURL == "" {
		fmt.Print("Enter Vapor RMM Server URL: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		serverURL = strings.TrimSpace(input)
	}

	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "Error: Server URL is required")
		os.Exit(1)
	}

	configDir := "/etc/vaporrmm"
	if err := os.MkdirAll(configDir, 0750); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
		os.Exit(1)
	}

	agentID := generateAgentID()
	agentKey := generateSecureKey()

	config := Config{
		ServerURL: strings.TrimSuffix(serverURL, "/"),
		AgentID:   agentID,
		AgentKey:  agentKey,
		Name:      agentName,
		CreatedAt: time.Now().Unix(),
	}

	configPath := filepath.Join(configDir, "config.json")
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling config: %v\n", err)
		os.Exit(1)
	}
	// 0600 — only the owner (root) can read the file that contains the agent key.
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[OK] Configuration saved (permissions: 0600)")

	agentBin := "/usr/local/bin/vaporrmm-agent"
	currentExe, _ := os.Executable()
	if err := copyFile(currentExe, agentBin); err != nil {
		fmt.Printf("[!] Warning: Could not copy agent binary: %v\n", err)
	} else {
		os.Chmod(agentBin, 0755)
		fmt.Println("[OK] Agent binary installed")
	}

	if installAsService {
		if canUseSystemd() {
			setupSystemdService(config.ServerURL, agentID, agentKey)
		} else if canUseOpenRC() {
			setupOpenRCService(config.ServerURL, agentID, agentKey)
		} else if os.Getuid() != 0 {
			fmt.Println("\n[!] Note: Run as root to install as a system service")
		} else {
			fmt.Println("\n[!] Note: No supported init system found (systemd or OpenRC)")
		}
	}

	fmt.Printf("\n[OK] Installation complete!\n")
	fmt.Printf("  Agent ID: %s\n", agentID)
	fmt.Printf("  Server URL: %s\n", config.ServerURL)
	if agentName != "" {
		fmt.Printf("  Name: %s\n", agentName)
	}
}

// ============== Register Command ==============

func runRegister() {
	var name string
	var tags string

	for i := 2; i < len(os.Args); i++ {
		switch {
		case os.Args[i] == "--name" && i+1 < len(os.Args):
			name = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--name="):
			name = strings.TrimPrefix(os.Args[i], "--name=")
		case os.Args[i] == "--tags" && i+1 < len(os.Args):
			tags = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--tags="):
			tags = strings.TrimPrefix(os.Args[i], "--tags=")
		}
	}

	if name == "" {
		fmt.Print("Enter device name: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		name = strings.TrimSpace(input)
	}

	if name == "" {
		fmt.Fprintln(os.Stderr, "Error: Device name is required")
		os.Exit(1)
	}

	var tagsList []string
	if tags != "" {
		for _, t := range strings.Split(tags, ",") {
			if trimmed := strings.TrimSpace(t); trimmed != "" {
				tagsList = append(tagsList, trimmed)
			}
		}
	}

	// Load the installed agent ID if available; fall back to generating one.
	agentID := loadInstalledAgentID()

	deviceInfo := map[string]interface{}{
		"name":     name,
		"tags":     tagsList,
		"type":     getOSType(),
		"os":       getOSName(),
		"arch":     getArch(),
		"host":     getHostname(),
		"agent_id": agentID,
		"time":     time.Now().Unix(),
	}

	fmt.Println("\n[OK] Device registration ready:")
	data, _ := json.MarshalIndent(deviceInfo, "", "  ")
	fmt.Println(string(data))
	fmt.Println("\nRun the following on your Vapor RMM server to complete registration:")
	fmt.Printf("  vaporrmm-cli register-device --agent-id %s\n", agentID)
}

// loadInstalledAgentID reads the agent ID from the installed config file.
// If the config is not present, a new ID is generated.
func loadInstalledAgentID() string {
	configPath := "/etc/vaporrmm/config.json"
	content, err := os.ReadFile(configPath)
	if err != nil {
		return generateAgentID()
	}
	var cfg Config
	if err := json.Unmarshal(content, &cfg); err != nil || cfg.AgentID == "" {
		return generateAgentID()
	}
	return cfg.AgentID
}

// ============== Update Sunshine Command ==============

// SunshineConfig represents the Sunshine JSON configuration structure
type SunshineConfig struct {
	ServerName       string `json:"server_name"`
	HTTPPort         int    `json:"http_port"`
	HTTPSPort        int    `json:"https_port"`
	OriginWebUIAllowed string `json:"origin_web_ui_allowed,omitempty"`
	DesktopWidth     int    `json:"desktop_width"`
	DesktopHeight    int    `json:"desktop_height"`
	MinBitrate       int    `json:"min_bitrate"`
	MaxBitrate       int    `json:"max_bitrate"`
	FPS              []int  `json:"fps"`
	Codec            string `json:"codec"`
	SvtAv1Enabled    bool   `json:"svt_av1_enabled,omitempty"`
	HevcEnabled      bool   `json:"hevc_enabled,omitempty"`
	Av1Enabled       bool   `json:"av1_enabled,omitempty"`
	Encoder          string `json:"encoder,omitempty"`
	LogLevel         string `json:"log_level"`
	// Credentials stored separately for security
	WebUIUsername    string `json:"web_ui_username,omitempty"`
	WebUIPassword    string `json:"web_ui_password,omitempty"`
}

// SunshineCredentials holds the credentials for Sunshine web UI
type SunshineCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func runUpdateSunshine() {
	port := 47990
	var hostname string
	var encoder string
	var outputOnly bool

	for i := 2; i < len(os.Args); i++ {
		switch {
		case os.Args[i] == "--port" && i+1 < len(os.Args):
			if p, err := strconv.Atoi(os.Args[i+1]); err == nil {
				port = p
			}
			i++
		case strings.HasPrefix(os.Args[i], "--port="):
			if p, err := strconv.Atoi(strings.TrimPrefix(os.Args[i], "--port=")); err == nil {
				port = p
			}
		case os.Args[i] == "--hostname" && i+1 < len(os.Args):
			hostname = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--hostname="):
			hostname = strings.TrimPrefix(os.Args[i], "--hostname=")
		case os.Args[i] == "--encoder" && i+1 < len(os.Args):
			encoder = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--encoder="):
			encoder = strings.TrimPrefix(os.Args[i], "--encoder=")
		case os.Args[i] == "--output-only":
			outputOnly = true
		}
	}

	if hostname == "" {
		hostname = getHostname()
	}

	// Auto-detect encoder if not specified
	if encoder == "" {
		encoder = detectBestEncoder()
	}

	username := "admin"
	password := generateSecureKey()[:16]

	// Build proper Sunshine JSON config
	sunshineConfig := SunshineConfig{
		ServerName:       hostname,
		HTTPPort:         port,
		HTTPSPort:        port + 1,
		OriginWebUIAllowed: "lan",
		DesktopWidth:     1920,
		DesktopHeight:    1080,
		MinBitrate:       1000,
		MaxBitrate:       50000,
		FPS:              []int{30, 60},
		Codec:            "auto",
		SvtAv1Enabled:    false,
		HevcEnabled:      true,
		Av1Enabled:       false,
		Encoder:          encoder,
		LogLevel:         "info",
		WebUIUsername:    username,
		WebUIPassword:    password,
	}

	configJSON, err := json.MarshalIndent(sunshineConfig, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating config: %v\n", err)
		return
	}

	// Add header comment
	configContent := "// Sunshine Configuration for vaporRMM\n" + string(configJSON)

	// Determine config paths based on OS
	var configPaths []string
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, _ := os.UserHomeDir()
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		configPaths = []string{
			filepath.Join(appData, "Sunshine", "sunshine.json"),
			filepath.Join(appData, "Sunshine", "config", "sunshine.json"),
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		configPaths = []string{
			filepath.Join(home, ".config", "sunshine", "sunshine.json"),
		}
	default: // Linux
		configPaths = []string{
			"/etc/sunshine/sunshine.json",
			"/etc/xdg/sunshine/sunshine.json",
		}
		home, _ := os.UserHomeDir()
		if home != "" {
			configPaths = append(configPaths,
				filepath.Join(home, ".config", "sunshine", "sunshine.json"),
				filepath.Join(home, ".local", "share", "sunshine", "sunshine.json"),
			)
		}
	}

	if !outputOnly {
		var configPath string
		for _, p := range configPaths {
			dir := filepath.Dir(p)
			if _, err := os.Stat(dir); err == nil {
				configPath = p
				break
			}
		}

		if configPath != "" {
			// 0600 — the config contains plaintext credentials.
			if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
				fmt.Printf("[!] Warning: Could not write Sunshine config: %v\n", err)
				return
			}
			fmt.Printf("[OK] Sunshine configuration updated at %s (permissions: 0600)\n", configPath)
		} else {
			fmt.Println("[OK] Sunshine Configuration (save with permissions 0600):")
			fmt.Println(configContent)
			fmt.Printf("\n[!] Note: Save to one of these paths:\n")
			for _, p := range configPaths {
				fmt.Printf("    - %s\n", p)
			}
		}
	} else {
		fmt.Println(configContent)
	}

	fmt.Printf("\n[!] IMPORTANT: Save these credentials - they cannot be retrieved later:\n")
	fmt.Printf("    Username: %s\n", username)
	fmt.Printf("    Password: %s\n", password)
	fmt.Printf("\n[!] Connect via Moonlight: http://<device-ip>:%d\n", port)
}

// detectBestEncoder returns the best available encoder for the current system
func detectBestEncoder() string {
	// Check for NVIDIA GPU (NVENC)
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return "nvenc"
	}
	// Check for AMD GPU (AMF)
	if _, err := exec.LookPath("vainfo"); err == nil {
		// Check AMF support
		cmd := exec.Command("vainfo", "--display", "drm")
		if output, err := cmd.CombinedOutput(); err == nil {
			if strings.Contains(strings.ToLower(string(output)), "amf") ||
				strings.Contains(strings.ToLower(string(output)), "radeonsi") {
				return "amf"
			}
		}
	}
	// Check for Intel QuickSync (VA-API)
	if _, err := exec.LookPath("vainfo"); err == nil {
		cmd := exec.Command("vainfo")
		if output, err := cmd.CombinedOutput(); err == nil {
			if strings.Contains(strings.ToLower(string(output)), "intel") ||
				strings.Contains(strings.ToLower(string(output)), "i965") ||
				strings.Contains(strings.ToLower(string(output)), "iris") {
				return "vaapi"
			}
		}
	}
	// Fall back to software encoding
	return "software"
}

// updateSunshineWithPort updates the Sunshine configuration with a specific port
// without using os.Args manipulation - passes parameters directly.
func updateSunshineWithPort(port int) {
	hostname := getHostname()
	encoder := detectBestEncoder()
	username := "admin"
	password := generateSecureKey()[:16]

	// Build proper Sunshine JSON config
	sunshineConfig := SunshineConfig{
		ServerName:       hostname,
		HTTPPort:         port,
		HTTPSPort:        port + 1,
		OriginWebUIAllowed: "lan",
		DesktopWidth:     1920,
		DesktopHeight:    1080,
		MinBitrate:       1000,
		MaxBitrate:       50000,
		FPS:              []int{30, 60},
		Codec:            "auto",
		SvtAv1Enabled:    false,
		HevcEnabled:      true,
		Av1Enabled:       false,
		Encoder:          encoder,
		LogLevel:         "info",
		WebUIUsername:    username,
		WebUIPassword:    password,
	}

	configJSON, err := json.MarshalIndent(sunshineConfig, "", "  ")
	if err != nil {
		fmt.Printf("[!] Warning: Could not generate Sunshine config: %v\n", err)
		return
	}

	// Add header comment
	configContent := "// Sunshine Configuration for vaporRMM\n" + string(configJSON)

	// Determine config paths based on OS
	var configPaths []string
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, _ := os.UserHomeDir()
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		configPaths = []string{
			filepath.Join(appData, "Sunshine", "sunshine.json"),
			filepath.Join(appData, "Sunshine", "config", "sunshine.json"),
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		configPaths = []string{
			filepath.Join(home, ".config", "sunshine", "sunshine.json"),
		}
	default: // Linux
		configPaths = []string{
			"/etc/sunshine/sunshine.json",
			"/etc/xdg/sunshine/sunshine.json",
		}
		home, _ := os.UserHomeDir()
		if home != "" {
			configPaths = append(configPaths,
				filepath.Join(home, ".config", "sunshine", "sunshine.json"),
				filepath.Join(home, ".local", "share", "sunshine", "sunshine.json"),
			)
		}
	}

	var configPath string
	for _, p := range configPaths {
		dir := filepath.Dir(p)
		if _, err := os.Stat(dir); err == nil {
			configPath = p
			break
		}
	}

	if configPath != "" {
		// 0600 — the config contains plaintext credentials.
		if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
			fmt.Printf("[!] Warning: Could not write Sunshine config: %v\n", err)
			return
		}
		fmt.Printf("[OK] Sunshine configuration updated at %s (permissions: 0600)\n", configPath)
	} else {
		fmt.Println("[!] Warning: No valid Sunshine config directory found, skipping config generation")
	}

	fmt.Printf("[!] IMPORTANT: Save these credentials - they cannot be retrieved later:\n")
	fmt.Printf("    Username: %s\n", username)
	fmt.Printf("    Password: %s\n", password)
	fmt.Printf("[!] Connect via Moonlight: http://<device-ip>:%d\n", port)
}

// ============== Install Sunshine Command ==============

func runInstallSunshine() {
	var port int
	autoStart := true // Default to true
	var skipConfig bool

	for i := 2; i < len(os.Args); i++ {
		switch {
		case os.Args[i] == "--port" && i+1 < len(os.Args):
			if p, err := strconv.Atoi(os.Args[i+1]); err == nil {
				port = p
			}
			i++
		case strings.HasPrefix(os.Args[i], "--port="):
			if p, err := strconv.Atoi(strings.TrimPrefix(os.Args[i], "--port=")); err == nil {
				port = p
			}
		case os.Args[i] == "--no-auto-start":
			autoStart = false
		case os.Args[i] == "--skip-config":
			skipConfig = true
		}
	}

	if port == 0 {
		port = 47990
	}

	fmt.Println("[OK] Installing Sunshine for remote desktop access...")
	fmt.Println("  Port:", port)

	switch runtime.GOOS {
	case "linux":
		installSunshineLinux(port, autoStart, skipConfig)
	case "windows":
		installSunshineWindows(port, autoStart, skipConfig)
	case "darwin":
		installSunshineMacOS(port, autoStart, skipConfig)
	default:
		fmt.Fprintf(os.Stderr, "[!] Unsupported OS: %s\n", runtime.GOOS)
	}
}

func installSunshineLinux(port int, autoStart bool, skipConfig bool) {
	// Check if running as root
	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, "[!] This command requires root privileges. Run with sudo.")
		return
	}

	// Detect package manager
	pkgManager := detectPackageManager()
	if pkgManager == "" {
		fmt.Fprintln(os.Stderr, "[!] No supported package manager found (apt, dnf, pacman)")
		return
	}

	fmt.Printf("[OK] Detected package manager: %s\n", pkgManager)

	// Install dependencies
	fmt.Println("[OK] Installing dependencies...")
	installDepsLinux(pkgManager)

	// Check if Sunshine is already installed
	if _, err := exec.LookPath("sunshine"); err == nil {
		fmt.Println("[OK] Sunshine is already installed")
	} else {
		fmt.Println("[OK] Installing Sunshine...")
		installSunshineBinary(pkgManager)
	}

	// Create config directory
	configDir := "/etc/sunshine"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		fmt.Printf("[!] Warning: Could not create config directory: %v\n", err)
	}

	// Generate config if not skipped
	if !skipConfig {
		fmt.Println("[OK] Generating Sunshine configuration...")
		// Call update-sunshine directly with port parameter instead of manipulating os.Args
		updateSunshineWithPort(port)
	}

	// Create systemd service for Sunshine
	if autoStart {
		fmt.Println("[OK] Setting up Sunshine service...")
		setupSunshineService()
	}

	fmt.Println("\n[OK] Sunshine installation complete!")
	fmt.Printf("[OK] Sunshine will be available on port %d\n", port)
	fmt.Println("[!] Remember to configure your Moonlight client to connect to this device")
}

func installSunshineWindows(port int, autoStart bool, skipConfig bool) {
	fmt.Println("[OK] Installing Sunshine for Windows...")

	// Check if Sunshine is installed
	sunshinePaths := []string{
		"C:\\Program Files\\Sunshine\\sunshine.exe",
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Sunshine", "sunshine.exe"),
	}

	installed := false
	for _, path := range sunshinePaths {
		if _, err := os.Stat(path); err == nil {
			installed = true
			fmt.Printf("[OK] Sunshine found at: %s\n", path)
			break
		}
	}

	if !installed {
		fmt.Println("[!] Sunshine is not installed.")
		fmt.Println("[OK] Please download Sunshine from: https://github.com/LizardByte/Sunshine/releases")
		fmt.Println("[OK] After installation, run: vaporrmm update-sunshine --port <port>")
		return
	}

	// Create config directory
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	configDir := filepath.Join(appData, "Sunshine")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		fmt.Printf("[!] Warning: Could not create config directory: %v\n", err)
	}

	// Generate config if not skipped
	if !skipConfig {
		fmt.Println("[OK] Generating Sunshine configuration...")
		updateSunshineWithPort(port)
	}

	// Set up auto-start
	if autoStart {
		fmt.Println("[OK] Setting up Sunshine auto-start...")
		// Add to Windows startup
		setupSunshineWindowsStartup()
	}

	fmt.Println("\n[OK] Sunshine setup complete!")
	fmt.Printf("[OK] Sunshine will be available on port %d\n", port)
}

func installSunshineMacOS(port int, autoStart bool, skipConfig bool) {
	fmt.Println("[OK] Installing Sunshine for macOS...")

	// Check if Sunshine is installed
	if _, err := exec.LookPath("sunshine"); err == nil {
		fmt.Println("[OK] Sunshine is already installed")
	} else {
		fmt.Println("[!] Sunshine is not installed.")
		fmt.Println("[OK] Install via Homebrew: brew install --cask sunshine")
		fmt.Println("[OK] Or download from: https://github.com/LizardByte/Sunshine/releases")
		return
	}

	// Create config directory
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".config", "sunshine")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		fmt.Printf("[!] Warning: Could not create config directory: %v\n", err)
	}

	// Generate config if not skipped
	if !skipConfig {
		fmt.Println("[OK] Generating Sunshine configuration...")
		updateSunshineWithPort(port)
	}

	fmt.Println("\n[OK] Sunshine setup complete!")
	fmt.Printf("[OK] Sunshine will be available on port %d\n", port)
}

func detectPackageManager() string {
	// Check for apt
	if _, err := exec.LookPath("apt"); err == nil {
		return "apt"
	}
	// Check for dnf
	if _, err := exec.LookPath("dnf"); err == nil {
		return "dnf"
	}
	// Check for pacman
	if _, err := exec.LookPath("pacman"); err == nil {
		return "pacman"
	}
	// Check for zypper
	if _, err := exec.LookPath("zypper"); err == nil {
		return "zypper"
	}
	return ""
}

func installDepsLinux(pkgManager string) {
	var cmd *exec.Cmd
	switch pkgManager {
	case "apt":
		cmd = exec.Command("apt-get", "update")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
		cmd = exec.Command("apt-get", "install", "-y", "libopus0", "libssl3", "libavcodec60", "libavfilter9", "libavformat60", "libavutil58", "libswscale7", "libx11-6", "libxext6", "libxrandr2", "libxtst6", "libevdev2", "libpulse0")
	case "dnf":
		cmd = exec.Command("dnf", "install", "-y", "opus", "openssl", "ffmpeg-libs", "libX11", "libXext", "libXrandr", "libXtst", "libevdev", "pulseaudio-libs")
	case "pacman":
		cmd = exec.Command("pacman", "-S", "--noconfirm", "opus", "openssl", "ffmpeg", "libx11", "libxext", "libxrandr", "libxtst", "libevdev", "libpulse")
	case "zypper":
		cmd = exec.Command("zypper", "install", "-y", "libopus0", "libopenssl-3", "ffmpeg-6", "libX11-6", "libXext6", "libXrandr2", "libXtst6", "libevdev2", "libpulse0")
	default:
		fmt.Println("[!] Unsupported package manager")
		return
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("[!] Warning: Could not install dependencies: %v\n", err)
	} else {
		fmt.Println("[OK] Dependencies installed")
	}
}

func installSunshineBinary(pkgManager string) {
	// Try to install from package manager first, fallback to AppImage
	var cmd *exec.Cmd
	switch pkgManager {
	case "apt", "dnf", "zypper":
		// Sunshine may not be in default repos, try flatpak
		if _, err := exec.LookPath("flatpak"); err == nil {
			fmt.Println("[OK] Installing Sunshine via Flatpak...")
			cmd = exec.Command("flatpak", "install", "-y", "--noninteractive", "flathub", "dev.lizardbyte.app.Sunshine")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err == nil {
				fmt.Println("[OK] Sunshine installed via Flatpak")
				return
			}
		}
	case "pacman":
		cmd = exec.Command("pacman", "-S", "--noconfirm", "sunshine")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			fmt.Println("[OK] Sunshine installed via pacman")
			return
		}
	}

	// Fallback: download AppImage
	fmt.Println("[OK] Installing Sunshine AppImage...")
	installSunshineAppImage()
}

func installSunshineAppImage() {
	// Create directory
	installDir := "/opt/sunshine"
	if err := os.MkdirAll(installDir, 0755); err != nil {
		fmt.Printf("[!] Warning: Could not create install directory: %v\n", err)
		return
	}

	appImagePath := filepath.Join(installDir, "Sunshine.AppImage")

	// Download latest release (simplified - in production, use GitHub API)
	fmt.Println("[OK] Downloading Sunshine AppImage...")
	cmd := exec.Command("wget", "-q", "-O", appImagePath, "https://github.com/LizardByte/Sunshine/releases/latest/download/sunshine.AppImage")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("[!] Warning: Could not download Sunshine: %v\n", err)
		fmt.Println("[!] Please download manually from: https://github.com/LizardByte/Sunshine/releases")
		return
	}

	// Make executable
	os.Chmod(appImagePath, 0755)
	fmt.Println("[OK] Sunshine AppImage downloaded")
}

func setupSunshineService() {
	serviceContent := `[Unit]
Description=Sunshine Gamestream Host
After=network.target display-manager.service
Wants=display-manager.service

[Service]
Type=simple
ExecStart=/opt/sunshine/Sunshine.AppImage --no-confirm
Restart=on-failure
RestartSec=5
Environment=DISPLAY=:0
Environment=XAUTHORITY=/run/user/1000/gdm/Xauthority

[Install]
WantedBy=graphical.target
`
	servicePath := "/etc/systemd/system/sunshine.service"
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		fmt.Printf("[!] Warning: Could not create sunshine service: %v\n", err)
		return
	}

	fmt.Println("[OK] Sunshine service created")

	// Enable and start
	if cmd := exec.Command("systemctl", "daemon-reload"); cmd.Run() == nil {
		fmt.Println("[OK] Systemd daemon reloaded")
	}
	if cmd := exec.Command("systemctl", "enable", "sunshine.service"); cmd.Run() == nil {
		fmt.Println("[OK] Sunshine service enabled")
	}
	if cmd := exec.Command("systemctl", "start", "sunshine.service"); cmd.Run() == nil {
		fmt.Println("[OK] Sunshine service started")
	}
}

func setupSunshineWindowsStartup() {
	// Use registry to add to startup
	keyPath := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	sunshinePath := filepath.Join(appData, "Sunshine", "sunshine.exe")

	cmd := exec.Command("reg", "add", keyPath, "/v", "Sunshine", "/t", "REG_SZ", "/d", sunshinePath, "/f")
	if err := cmd.Run(); err != nil {
		fmt.Printf("[!] Warning: Could not add to startup: %v\n", err)
	} else {
		fmt.Println("[OK] Sunshine added to Windows startup")
	}
}

// ============== Sunshine Status Command ==============

func runSunshineStatus() {
	fmt.Println("[OK] Checking Sunshine status...")

	// Check if Sunshine is installed
	installed := false
	installPath := ""

	switch runtime.GOOS {
	case "linux":
		if path, err := exec.LookPath("sunshine"); err == nil {
			installed = true
			installPath = path
		}
		// Check AppImage
		if _, err := os.Stat("/opt/sunshine/Sunshine.AppImage"); err == nil {
			installed = true
			installPath = "/opt/sunshine/Sunshine.AppImage"
		}
		// Check flatpak
		cmd := exec.Command("flatpak", "list", "--columns=application")
		if output, err := cmd.Output(); err == nil {
			if strings.Contains(string(output), "dev.lizardbyte.app.Sunshine") {
				installed = true
				installPath = "flatpak://dev.lizardbyte.app.Sunshine"
			}
		}
	case "windows":
		paths := []string{
			"C:\\Program Files\\Sunshine\\sunshine.exe",
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Sunshine", "sunshine.exe"),
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				installed = true
				installPath = p
				break
			}
		}
	case "darwin":
		if path, err := exec.LookPath("sunshine"); err == nil {
			installed = true
			installPath = path
		}
	}

	fmt.Printf("  Installed: %v\n", installed)
	if installed {
		fmt.Printf("  Path: %s\n", installPath)
	}

	// Check if running
	running := isSunshineRunning()
	fmt.Printf("  Running: %v\n", running)

	// Check config files
	configPaths := getSunshineConfigPaths()
	configFound := false
	for _, p := range configPaths {
		if _, err := os.Stat(p); err == nil {
			configFound = true
			fmt.Printf("  Config: %s\n", p)
			break
		}
	}
	if !configFound {
		fmt.Println("  Config: Not found")
	}

	// Check port availability
	port := 47990
	if running {
		fmt.Printf("  Port: %d (in use)\n", port)
	}

	// Summary
	fmt.Println()
	if installed && running && configFound {
		fmt.Println("[OK] Sunshine is fully operational")
	} else if installed && configFound {
		fmt.Println("[!] Sunshine is installed but not running")
		fmt.Println("    Start with: systemctl start sunshine.service (Linux)")
	} else {
		fmt.Println("[!] Sunshine is not properly installed")
		fmt.Println("    Install with: vaporrmm install-sunshine")
	}
}

func isSunshineRunning() bool {
	// Try to connect to Sunshine HTTP port
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://localhost:47990/")
	if err == nil {
		defer resp.Body.Close()
		return true
	}
	// Try HTTPS
	resp, err = client.Get("https://localhost:47991/")
	if err == nil {
		defer resp.Body.Close()
		return true
	}
	return false
}

func getSunshineConfigPaths() []string {
	var paths []string
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, _ := os.UserHomeDir()
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		paths = []string{
			filepath.Join(appData, "Sunshine", "sunshine.json"),
			filepath.Join(appData, "Sunshine", "config", "sunshine.json"),
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		paths = []string{
			filepath.Join(home, ".config", "sunshine", "sunshine.json"),
		}
	default:
		paths = []string{
			"/etc/sunshine/sunshine.json",
			"/etc/xdg/sunshine/sunshine.json",
		}
		home, _ := os.UserHomeDir()
		if home != "" {
			paths = append(paths,
				filepath.Join(home, ".config", "sunshine", "sunshine.json"),
				filepath.Join(home, ".local", "share", "sunshine", "sunshine.json"),
			)
		}
	}
	return paths
}

// ============== Install Tailscale Command ==============

func runInstallTailscale() {
	var authKey string
	var exitNode bool
	var skipUp bool

	for i := 2; i < len(os.Args); i++ {
		switch {
		case os.Args[i] == "--authkey" && i+1 < len(os.Args):
			authKey = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--authkey="):
			authKey = strings.TrimPrefix(os.Args[i], "--authkey=")
		case os.Args[i] == "--exit-node":
			exitNode = true
		case os.Args[i] == "--skip-up":
			skipUp = true
		}
	}

	fmt.Println("[OK] Installing Tailscale for secure remote access...")

	switch runtime.GOOS {
	case "linux":
		installTailscaleLinux(authKey, exitNode, skipUp)
	case "windows":
		installTailscaleWindows(authKey, exitNode, skipUp)
	case "darwin":
		installTailscaleMacOS(authKey, exitNode, skipUp)
	default:
		fmt.Fprintf(os.Stderr, "[!] Unsupported OS: %s\n", runtime.GOOS)
	}
}

func installTailscaleLinux(authKey string, exitNode bool, skipUp bool) {
	// Check if running as root
	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, "[!] This command requires root privileges. Run with sudo.")
		return
	}

	// Detect package manager
	pkgManager := detectPackageManager()
	if pkgManager == "" {
		fmt.Fprintln(os.Stderr, "[!] No supported package manager found (apt, dnf, pacman)")
		return
	}

	fmt.Printf("[OK] Detected package manager: %s\n", pkgManager)

	// Check if Tailscale is already installed
	if _, err := exec.LookPath("tailscale"); err == nil {
		fmt.Println("[OK] Tailscale is already installed")
	} else {
		fmt.Println("[OK] Installing Tailscale...")
		installTailscaleBinary(pkgManager)
	}

	// Start Tailscale if not skipped
	if !skipUp {
		fmt.Println("[OK] Starting Tailscale...")
		startTailscale(authKey, exitNode)
	}

	// Get Tailscale IP
	tailscaleIP := getTailscaleIP()
	if tailscaleIP != "" {
		fmt.Printf("[OK] Tailscale IP: %s\n", tailscaleIP)
	}

	fmt.Println("\n[OK] Tailscale installation complete!")
	fmt.Println("[OK] Your device is now connected to your Tailscale network")
}

func installTailscaleWindows(authKey string, exitNode bool, skipUp bool) {
	fmt.Println("[OK] Installing Tailscale for Windows...")

	// Check if Tailscale is installed
	if _, err := exec.LookPath("tailscale"); err == nil {
		fmt.Println("[OK] Tailscale is already installed")
	} else {
		fmt.Println("[!] Tailscale is not installed.")
		fmt.Println("[OK] Please download Tailscale from: https://tailscale.com/download")
		fmt.Println("[OK] After installation, run: vaporrmm install-tailscale --authkey <key>")
		return
	}

	// Start Tailscale if not skipped
	if !skipUp {
		fmt.Println("[OK] Starting Tailscale...")
		startTailscale(authKey, exitNode)
	}

	tailscaleIP := getTailscaleIP()
	if tailscaleIP != "" {
		fmt.Printf("[OK] Tailscale IP: %s\n", tailscaleIP)
	}

	fmt.Println("\n[OK] Tailscale setup complete!")
}

func installTailscaleMacOS(authKey string, exitNode bool, skipUp bool) {
	fmt.Println("[OK] Installing Tailscale for macOS...")

	// Check if Tailscale is installed
	if _, err := exec.LookPath("tailscale"); err == nil {
		fmt.Println("[OK] Tailscale is already installed")
	} else {
		fmt.Println("[!] Tailscale is not installed.")
		fmt.Println("[OK] Install via Homebrew: brew install --cask tailscale")
		fmt.Println("[OK] Or download from: https://tailscale.com/download")
		return
	}

	// Start Tailscale if not skipped
	if !skipUp {
		fmt.Println("[OK] Starting Tailscale...")
		startTailscale(authKey, exitNode)
	}

	tailscaleIP := getTailscaleIP()
	if tailscaleIP != "" {
		fmt.Printf("[OK] Tailscale IP: %s\n", tailscaleIP)
	}

	fmt.Println("\n[OK] Tailscale setup complete!")
}

func installTailscaleBinary(pkgManager string) {
	var cmd *exec.Cmd
	switch pkgManager {
	case "apt":
		// Use official Tailscale apt repository
		cmd = exec.Command("sh", "-c", `
			curl -fsSL https://tailscale.com/install.sh | sh
		`)
	case "dnf":
		cmd = exec.Command("sh", "-c", `
			curl -fsSL https://tailscale.com/install.sh | sh
		`)
	case "pacman":
		cmd = exec.Command("pacman", "-S", "--noconfirm", "tailscale")
	case "zypper":
		cmd = exec.Command("sh", "-c", `
			curl -fsSL https://tailscale.com/install.sh | sh
		`)
	default:
		fmt.Println("[!] Unsupported package manager")
		return
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("[!] Warning: Could not install Tailscale: %v\n", err)
	} else {
		fmt.Println("[OK] Tailscale installed successfully")
	}
}

func startTailscale(authKey string, exitNode bool) {
	args := []string{"up"}
	if authKey != "" {
		args = append(args, "--authkey", authKey)
	}
	if exitNode {
		args = append(args, "--advertise-exit-node")
	}

	cmd := exec.Command("tailscale", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("[!] Warning: Could not start Tailscale: %v\n", err)
		fmt.Println("[!] You may need to authenticate manually: tailscale up")
	} else {
		fmt.Println("[OK] Tailscale started successfully")
	}
}

func getTailscaleIP() string {
	cmd := exec.Command("tailscale", "ip", "-4")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// ============== Tailscale Status Command ==============

func runTailscaleStatus() {
	fmt.Println("[OK] Checking Tailscale status...")

	// Check if Tailscale is installed
	installed := false
	switch runtime.GOOS {
	case "linux", "darwin":
		if _, err := exec.LookPath("tailscale"); err == nil {
			installed = true
		}
	case "windows":
		if _, err := exec.LookPath("tailscale"); err == nil {
			installed = true
		}
	}

	fmt.Printf("  Installed: %v\n", installed)

	if !installed {
		fmt.Println("[!] Tailscale is not installed")
		fmt.Println("    Install with: vaporrmm install-tailscale")
		return
	}

	// Check if Tailscale is connected
	cmd := exec.Command("tailscale", "status", "--json")
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("  Connected: false\n")
		fmt.Println("[!] Tailscale is not connected")
		fmt.Println("    Connect with: tailscale up")
		return
	}

	// Parse status JSON
	var status map[string]interface{}
	if err := json.Unmarshal(output, &status); err != nil {
		fmt.Printf("  Connected: unknown (parse error)\n")
		return
	}

	// Get self IP
	if selfIP, ok := status["Self"]; ok {
		if selfMap, ok := selfIP.(map[string]interface{}); ok {
			if ip, ok := selfMap["TailscaleIPs"]; ok {
				if ips, ok := ip.([]interface{}); ok && len(ips) > 0 {
					fmt.Printf("  Tailscale IP: %s\n", ips[0])
				}
			}
		}
	}

	// Get backend status
	if backend, ok := status["BackendState"]; ok {
		if state, ok := backend.(string); ok {
			connected := state == "Running"
			fmt.Printf("  Connected: %v\n", connected)
			if connected {
				fmt.Println("[OK] Tailscale is connected and operational")
			} else {
				fmt.Println("[!] Tailscale is not connected")
				fmt.Println("    Connect with: tailscale up")
			}
		}
	}

	// List peers
	if peers, ok := status["Peer"]; ok {
		if peerMap, ok := peers.(map[string]interface{}); ok {
			fmt.Printf("  Connected Peers: %d\n", len(peerMap))
		}
	}
}

// ============== Package Command ==============

func runPackage() {
	var pkgType string
	outputDir := "./dist"
	version := "1.0.0"

	for i := 2; i < len(os.Args); i++ {
		switch {
		case os.Args[i] == "--type" && i+1 < len(os.Args):
			pkgType = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--type="):
			pkgType = strings.TrimPrefix(os.Args[i], "--type=")
		case os.Args[i] == "--output" && i+1 < len(os.Args):
			outputDir = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--output="):
			outputDir = strings.TrimPrefix(os.Args[i], "--output=")
		case os.Args[i] == "--version" && i+1 < len(os.Args):
			version = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--version="):
			version = strings.TrimPrefix(os.Args[i], "--version=")
		}
	}

	if pkgType == "" {
		fmt.Print("Enter package type (deb, rpm, msi): ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		pkgType = strings.TrimSpace(strings.ToLower(input))
	}

	pkgType = strings.ToLower(pkgType)

	fmt.Printf("[OK] Generating %s package...\n", pkgType)
	fmt.Println("  Package type:", pkgType)
	fmt.Println("  Output directory:", outputDir)
	fmt.Println("  Version:", version)

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Could not create output directory: %v\n", err)
		os.Exit(1)
	}

	switch pkgType {
		case "deb":
		if err := generateDebPackage(outputDir, version); err != nil {
			fmt.Fprintf(os.Stderr, "[!] Failed to generate .deb package: %v\n", err)
			os.Exit(1)
		}
	case "openrc":
		if err := generateOpenRCPackage(outputDir, version); err != nil {
			fmt.Fprintf(os.Stderr, "[!] Failed to generate OpenRC package: %v\n", err)
			os.Exit(1)
		}
	case "rpm":
		if err := generateRPMPackage(outputDir, version); err != nil {
			fmt.Fprintf(os.Stderr, "[!] Failed to generate .rpm package: %v\n", err)
			os.Exit(1)
		}
	case "msi":
		if err := generateMSIPackage(outputDir, version); err != nil {
			fmt.Fprintf(os.Stderr, "[!] Failed to generate .msi package: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "[!] Unknown package type: %s\n", pkgType)
		os.Exit(1)
	}
}

func generateDebPackage(outputDir, version string) error {
	baseDir := filepath.Join(outputDir, "vaporrmm-agent_"+version+"_amd64")
	dirs := []string{
		filepath.Join(baseDir, "DEBIAN"),
		filepath.Join(baseDir, "usr", "local", "bin"),
		filepath.Join(baseDir, "etc", "vaporrmm"),
		filepath.Join(baseDir, "lib", "systemd", "system"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}

	control := fmt.Sprintf(`Package: vaporrmm-agent
Version: %s
Section: admin
Priority: optional
Architecture: amd64
Maintainer: VaporRMM Team <team@vaporrmm.io>
Description: VaporRMM Agent - Remote Monitoring and Management
 VaporRMM agent for remote monitoring, management,
 and secure remote desktop access.
`, version)
	if err := os.WriteFile(filepath.Join(baseDir, "DEBIAN", "control"), []byte(control), 0644); err != nil {
		return err
	}

	service := `[Unit]
Description=VaporRMM Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/vaporrmm-agent
Restart=always
RestartSec=10
User=root

[Install]
WantedBy=multi-user.target
`
	if err := os.WriteFile(filepath.Join(baseDir, "lib", "systemd", "system", "vaporrmm-agent.service"), []byte(service), 0644); err != nil {
		return err
	}

	config := `{
  "server_url": "https://rmm.yourdomain.com",
  "agent_token": "",
  "log_level": "info"
}
`
	if err := os.WriteFile(filepath.Join(baseDir, "etc", "vaporrmm", "config.json"), []byte(config), 0600); err != nil {
		return err
	}

	postinst := `#!/bin/bash
set -e
systemctl daemon-reload
systemctl enable vaporrmm-agent.service
if [ -f /etc/vaporrmm/agent-state.json ]; then
    echo "[OK] Existing agent state found. Agent will reconnect automatically."
fi
`
	if err := os.WriteFile(filepath.Join(baseDir, "DEBIAN", "postinst"), []byte(postinst), 0755); err != nil {
		return err
	}

		fmt.Printf("[OK] Generated .deb package structure at %s\n", baseDir)
		fmt.Println("  Build with: dpkg-deb --build " + baseDir)
		return nil
}

func generateOpenRCPackage(outputDir, version string) error {
	baseDir := filepath.Join(outputDir, "vaporrmm-agent-openrc-"+version)
	dirs := []string{
		filepath.Join(baseDir, "usr", "local", "bin"),
		filepath.Join(baseDir, "etc", "init.d"),
		filepath.Join(baseDir, "etc", "vaporrmm"),
		filepath.Join(baseDir, "etc", "conf.d"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}

	initScript := `#!/sbin/openrc-run
# VaporRMM Agent OpenRC service

description="VaporRMM Remote Monitoring Agent"
command="/usr/local/bin/vaporrmm-agent"
command_background=true
pidfile="/run/${RC_SVCNAME}.pid"
output_log="/var/log/vaporrmm-agent.log"
error_log="/var/log/vaporrmm-agent.log"

depend() {
	need net
	after firewall
}

start_pre() {
	checkpath -f -m 0644 -o root:root "${output_log}"
}
`
	if err := os.WriteFile(filepath.Join(baseDir, "etc", "init.d", "vaporrmm-agent"), []byte(initScript), 0755); err != nil {
		return err
	}

	conf := `rc_verbose=yes
`
	if err := os.WriteFile(filepath.Join(baseDir, "etc", "conf.d", "vaporrmm-agent"), []byte(conf), 0644); err != nil {
		return err
	}

	config := `{
  "server_url": "https://rmm.yourdomain.com",
  "agent_token": "",
  "log_level": "info"
}
`
	if err := os.WriteFile(filepath.Join(baseDir, "etc", "vaporrmm", "config.json"), []byte(config), 0600); err != nil {
		return err
	}

	fmt.Printf("[OK] Generated OpenRC package structure at %s\n", baseDir)
	fmt.Println("  Install with: cp -r " + baseDir + "/* / && rc-update add vaporrmm-agent default")
	return nil
}

func generateRPMPackage(outputDir, version string) error {
	baseDir := filepath.Join(outputDir, "vaporrmm-agent-"+version)
	dirs := []string{
		filepath.Join(baseDir, "BUILD"),
		filepath.Join(baseDir, "BUILDROOT"),
		filepath.Join(baseDir, "RPMS"),
		filepath.Join(baseDir, "SOURCES"),
		filepath.Join(baseDir, "SPECS"),
		filepath.Join(baseDir, "SRPMS"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}

	spec := fmt.Sprintf(`Name:           vaporrmm-agent
Version:        %s
Release:        1%%%%{?dist}
Summary:        VaporRMM Agent - Remote Monitoring and Management
License:        AGPL-3.0
URL:            https://vaporrmm.io
Source0:        %%%%{name}-%%%%{version}.tar.gz

%%%%description
VaporRMM agent for remote monitoring, management,
and secure remote desktop access.

%%%%install
mkdir -p %%%%{buildroot}/usr/local/bin
mkdir -p %%%%{buildroot}/etc/vaporrmm
mkdir -p %%%%{buildroot}/lib/systemd/system

%%%%files
/usr/local/bin/vaporrmm-agent
/etc/vaporrmm/config.json
/lib/systemd/system/vaporrmm-agent.service

%%%%post
systemctl daemon-reload
systemctl enable vaporrmm-agent.service

%%%%changelog
* %s VaporRMM Team <team@vaporrmm.io> - %s-1
- Initial package release
`, version, time.Now().Format("Mon Jan 02 2006"), version)
	if err := os.WriteFile(filepath.Join(baseDir, "SPECS", "vaporrmm-agent.spec"), []byte(spec), 0644); err != nil {
		return err
	}

	service := `[Unit]
Description=VaporRMM Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/vaporrmm-agent
Restart=always
RestartSec=10
User=root

[Install]
WantedBy=multi-user.target
`
	if err := os.WriteFile(filepath.Join(baseDir, "SOURCES", "vaporrmm-agent.service"), []byte(service), 0644); err != nil {
		return err
	}

	config := `{
  "server_url": "https://rmm.yourdomain.com",
  "agent_token": "",
  "log_level": "info"
}
`
	if err := os.WriteFile(filepath.Join(baseDir, "SOURCES", "config.json"), []byte(config), 0600); err != nil {
		return err
	}

	fmt.Printf("[OK] Generated .rpm package structure at %s\n", baseDir)
	fmt.Println("  Build with: rpmbuild --define '_topdir " + baseDir + "' -ba " + filepath.Join(baseDir, "SPECS", "vaporrmm-agent.spec"))
	return nil
}

func generateMSIPackage(outputDir, version string) error {
	baseDir := filepath.Join(outputDir, "vaporrmm-agent-"+version+"-msi")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return err
	}

	wix := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <Product Id="*" Name="VaporRMM Agent" Language="1033" Version="%s"
           Manufacturer="VaporRMM" UpgradeCode="12345678-1234-1234-1234-123456789012">
    <Package InstallerVersion="200" Compressed="yes" InstallScope="perMachine" />
    <MediaTemplate EmbedCab="yes" />
    <Feature Id="ProductFeature" Title="VaporRMM Agent" Level="1">
      <ComponentGroupRef Id="ProductComponents" />
    </Feature>
    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="ProgramFilesFolder">
        <Directory Id="INSTALLFOLDER" Name="VaporRMM">
          <Component Id="AgentBinary" Guid="*">
            <File Id="AgentExe" Source="vaporrmm-agent.exe" KeyPath="yes" />
          </Component>
          <Component Id="ConfigFile" Guid="*">
            <File Id="ConfigJson" Source="config.json" KeyPath="yes" />
          </Component>
        </Directory>
      </Directory>
    </Directory>
    <ComponentGroup Id="ProductComponents" Directory="INSTALLFOLDER">
      <ComponentRef Id="AgentBinary" />
      <ComponentRef Id="ConfigFile" />
    </ComponentGroup>
  </Product>
</Wix>
`, version)
	if err := os.WriteFile(filepath.Join(baseDir, "vaporrmm-agent.wxs"), []byte(wix), 0644); err != nil {
		return err
	}

	config := `{
  "server_url": "https://rmm.yourdomain.com",
  "agent_token": "",
  "log_level": "info"
}
`
	if err := os.WriteFile(filepath.Join(baseDir, "config.json"), []byte(config), 0600); err != nil {
		return err
	}

	fmt.Printf("[OK] Generated .msi package structure at %s\n", baseDir)
	fmt.Println("  Build with: candle vaporrmm-agent.wxs && light vaporrmm-agent.wixobj -o vaporrmm-agent.msi")
	return nil
}

// ============== Deploy Command ==============

func runDeploy() {
	var serverURL string
	var targetHost string
	username := "root"

	for i := 2; i < len(os.Args); i++ {
		switch {
		case os.Args[i] == "--server" && i+1 < len(os.Args):
			serverURL = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--server="):
			serverURL = strings.TrimPrefix(os.Args[i], "--server=")
		case os.Args[i] == "--host" && i+1 < len(os.Args):
			targetHost = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--host="):
			targetHost = strings.TrimPrefix(os.Args[i], "--host=")
		case os.Args[i] == "--user" && i+1 < len(os.Args):
			username = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--user="):
			username = strings.TrimPrefix(os.Args[i], "--user=")
		}
	}

	if targetHost == "" {
		fmt.Fprintln(os.Stderr, "Error: Target host is required")
		os.Exit(1)
	}

	fmt.Println("[OK] Deploying vaporRMM to", targetHost)
	fmt.Println("  Server URL:", serverURL)

	script := fmt.Sprintf(`#!/bin/bash
# Install vaporRMM agent on %s
set -euo pipefail

curl -fsSL https://vaporrmm.com/install.sh | bash -s -- --server %s
`, targetHost, serverURL)

	scriptPath := "install-script.sh"
	if err := os.WriteFile(scriptPath, []byte(script), 0750); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Failed to write deployment script: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n[OK] Deployment script written to %s\n", scriptPath)
	fmt.Println("[OK] To deploy, run:")
	fmt.Printf("  ssh %s@%s 'bash -s' < %s\n", username, targetHost, scriptPath)
}

// ============== Docker Compose Command ==============

func runDockerCompose() {
	var serverURL string

	for i := 2; i < len(os.Args); i++ {
		switch {
		case os.Args[i] == "--server-url" && i+1 < len(os.Args):
			serverURL = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--server-url="):
			serverURL = strings.TrimPrefix(os.Args[i], "--server-url=")
		}
	}

	if serverURL == "" {
		fmt.Print("Enter server URL: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		serverURL = strings.TrimSpace(input)
	}

	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "Error: Server URL is required")
		os.Exit(1)
	}

	postgresPassword := generateSecureKey()[:16]

	dockerCompose := fmt.Sprintf(`version: '3.8'

services:
  vaporrmm-server:
    image: ghcr.io/vaporrmm/server:latest
    container_name: vaporrmm-server
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=sqlite:///data/vaporrmm.db
      - SERVER_URL=%s
    volumes:
      - ./data:/app/data
      - ./logs:/app/logs
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3

  vaporrmm-dashboard:
    image: ghcr.io/vaporrmm/dashboard:latest
    container_name: vaporrmm-dashboard
    ports:
      - "3000:3000"
    environment:
      - NEXT_PUBLIC_API_URL=http://localhost:8080/api
    depends_on:
      - vaporrmm-server
    restart: unless-stopped

  # Optional: PostgreSQL for production
  # postgres:
  #   image: postgres:15
  #   container_name: vaporrmm-postgres
  #   environment:
  #     - POSTGRES_PASSWORD=%s
  #   volumes:
  #     - ./postgres-data:/var/lib/postgresql/data
  #   restart: unless-stopped`, serverURL, postgresPassword)

	composePath := "docker-compose.generated.yml"
	if err := os.WriteFile(composePath, []byte(dockerCompose), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Failed to write compose file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[OK] Docker Compose config written to %s\n", composePath)
	fmt.Println("[OK] To use this configuration:")
	fmt.Printf("  docker compose -f %s up -d\n", composePath)
}

// ============== Uninstall Command ==============

func runUninstall() {
	var removeData bool
	var force bool

	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--remove-data":
			removeData = true
		case "--force":
			force = true
		}
	}

	if !force {
		fmt.Print("Are you sure you want to uninstall the vaporRMM agent? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			fmt.Println("Uninstall cancelled.")
			return
		}
	}

	fmt.Println("[OK] Preparing to uninstall vaporRMM agent...")

	// Try systemd first, then OpenRC
	if canUseSystemd() {
		if cmd := exec.Command("systemctl", "stop", "vaporrmm-agent.service"); cmd.Run() == nil {
			fmt.Println("[OK] Agent service stopped")
		}
		if cmd := exec.Command("systemctl", "disable", "vaporrmm-agent.service"); cmd.Run() == nil {
			fmt.Println("[OK] Agent service disabled")
		}
		servicePath := "/etc/systemd/system/vaporrmm-agent.service"
		if _, err := os.Stat(servicePath); err == nil {
			if err := os.Remove(servicePath); err != nil {
				fmt.Printf("[!] Warning: Could not remove service file: %v\n", err)
			} else {
				fmt.Println("[OK] Systemd service file removed")
			}
		}
	} else if canUseOpenRC() {
		if cmd := exec.Command("rc-service", "vaporrmm-agent", "stop"); cmd.Run() == nil {
			fmt.Println("[OK] Agent service stopped")
		}
		if cmd := exec.Command("rc-update", "del", "vaporrmm-agent", "default"); cmd.Run() == nil {
			fmt.Println("[OK] Agent service removed from default runlevel")
		}
		servicePath := "/etc/init.d/vaporrmm-agent"
		if _, err := os.Stat(servicePath); err == nil {
			if err := os.Remove(servicePath); err != nil {
				fmt.Printf("[!] Warning: Could not remove service file: %v\n", err)
			} else {
				fmt.Println("[OK] OpenRC service file removed")
			}
		}
		confPath := "/etc/conf.d/vaporrmm-agent"
		if _, err := os.Stat(confPath); err == nil {
			if err := os.Remove(confPath); err != nil {
				fmt.Printf("[!] Warning: Could not remove config file: %v\n", err)
			} else {
				fmt.Println("[OK] OpenRC config removed")
			}
		}
	} else if os.Getuid() != 0 {
		fmt.Println("[!] Note: Run as root to stop system service")
	}

	binPath := "/usr/local/bin/vaporrmm-agent"
	if _, err := os.Stat(binPath); err == nil {
		if err := os.Remove(binPath); err != nil {
			fmt.Printf("[!] Warning: Could not remove agent binary: %v\n", err)
		} else {
			fmt.Println("[OK] Agent binary removed")
		}
	}

	if removeData {
		configDir := "/etc/vaporrmm"
		if _, err := os.Stat(configDir); err == nil {
			if err := os.RemoveAll(configDir); err != nil {
				fmt.Printf("[!] Warning: Could not remove config directory: %v\n", err)
			} else {
				fmt.Printf("[OK] Configuration data removed from %s\n", configDir)
			}
		}
	}

	fmt.Println("\n[OK] Uninstallation complete!")
}

// ============== Status Command ==============

func runStatus() {
	configPath := "/etc/vaporrmm/config.json"

	info, err := os.Stat(configPath)
	if os.IsNotExist(err) {
		fmt.Println("vaporrmm-agent: Not installed")
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config: %v\n", err)
		return
	}
	_ = info

	content, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
		return
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config file: %v\n", err)
		return
	}

	fmt.Println("[OK] vaporRMM Agent Status:")
	fmt.Printf("  Server URL: %s\n", config.ServerURL)
	fmt.Printf("  Agent ID:   %s\n", config.AgentID)
	if config.Name != "" {
		fmt.Printf("  Name:       %s\n", config.Name)
	}
	fmt.Printf("  Installed:  %s\n", time.Unix(config.CreatedAt, 0).Format(time.RFC3339))

	cmd := exec.Command("systemctl", "is-active", "vaporrmm-agent.service")
	output, _ := cmd.Output()
	switch strings.TrimSpace(string(output)) {
	case "active":
		fmt.Println("  Status:     Running")
	case "":
		if os.Getuid() != 0 {
			fmt.Println("  Status:     Service check requires root privileges")
		} else {
			fmt.Println("  Status:     Not running")
		}
	default:
		fmt.Printf("  Status:     %s\n", strings.TrimSpace(string(output)))
	}
}

func runDBMigrate() {
	serverURL := os.Getenv("SERVER_URL")
	if serverURL == "" {
		fmt.Println("Error: SERVER_URL environment variable is required")
		fmt.Println("Example: SERVER_URL=http://localhost:8080 vaporrmm db-migrate")
		os.Exit(1)
	}

	token := os.Getenv("ADMIN_TOKEN")
	if token == "" {
		fmt.Println("Error: ADMIN_TOKEN environment variable is required")
		fmt.Println("Example: ADMIN_TOKEN=your-jwt-token SERVER_URL=http://localhost:8080 vaporrmm db-migrate")
		os.Exit(1)
	}

	fmt.Println("Triggering database migrations...")
	req, err := http.NewRequest("POST", serverURL+"/api/admin/db-migrate", nil)
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Migration failed: %s\n", string(body))
		os.Exit(1)
	}

	fmt.Println("Migrations completed successfully")
	fmt.Println(string(body))
}
