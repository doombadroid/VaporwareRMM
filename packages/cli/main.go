package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "status":
		runStatus()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(" vaporrmm - Vapor RMM Agent CLI")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  vaporrmm <command> [options]")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  install    Install the Vapor RMM agent")
	fmt.Println("  uninstall  Uninstall the Vapor RMM agent")
	fmt.Println("  status     Show agent status")
	fmt.Println("  help       Show this help message")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  sudo vaporrmm install --server https://rmm.example.com")
}

type Config struct {
	ServerURL string `json:"server_url"`
	AgentID   string `json:"agent_id"`
	CreatedAt int64  `json:"created_at"`
}

func runInstall() {
	var serverURL string
	
	if len(os.Args) > 2 {
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "--server" && i+1 < len(os.Args) {
				serverURL = os.Args[i+1]
				i++
			} else if strings.HasPrefix(os.Args[i], "--server=") {
				serverURL = strings.TrimPrefix(os.Args[i], "--server=")
			}
		}
	}
	
	if serverURL == "" {
		fmt.Print("Enter Vapor RMM Server URL: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		serverURL = strings.TrimSpace(input)
	}
	
	if serverURL == "" {
		fmt.Println("Error: Server URL is required")
		os.Exit(1)
	}
	
	// Create config directory
	configDir := "/etc/vaporrmm"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		fmt.Printf("Error creating config directory: %v\n", err)
		os.Exit(1)
	}
	
	// Generate agent ID if not exists
	agentID := generateAgentID()
	
	config := Config{
		ServerURL: strings.TrimSuffix(serverURL, "/"),
		AgentID:   agentID,
		CreatedAt: time.Now().Unix(),
	}
	
	// Save config
	configPath := filepath.Join(configDir, "config.json")
	configData, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Println("✓ Configuration saved")
	
	// Copy agent binary
	agentBin := "/usr/local/bin/vaporrmm-agent"
	currentExe, _ := os.Executable()
	if err := copyFile(currentExe, agentBin); err != nil {
		fmt.Printf("Warning: Could not copy agent binary: %v\n", err)
	} else {
		os.Chmod(agentBin, 0755)
		fmt.Println("✓ Agent binary installed")
	}
	
	// Setup systemd service if on Linux with systemctl
	if _, err := exec.LookPath("systemctl"); err == nil && os.Getuid() == 0 {
		setupSystemdService(config.ServerURL, agentID)
	} else if os.Getuid() != 0 {
		fmt.Println("\nNote: Run as root to install as a system service")
	}
	
	fmt.Printf("\n✓ Installation complete! Agent ID: %s\n", agentID)
	fmt.Printf("Server URL: %s\n", config.ServerURL)
}

func runUninstall() {
	// Stop and disable systemd service
	if _, err := exec.LookPath("systemctl"); err == nil {
		exec.Command("systemctl", "stop", "vaporrmm-agent").Run()
		exec.Command("systemctl", "disable", "vaporrmm-agent").Run()
		os.Remove("/etc/systemd/system/vaporrmm-agent.service")
	}
	
	// Remove config
	os.RemoveAll("/etc/vaporrmm")
	
	// Remove binary
	os.Remove("/usr/local/bin/vaporrmm-agent")
	
	fmt.Println("✓ Uninstallation complete!")
}

func runStatus() {
	configPath := "/etc/vaporrmm/config.json"
	
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Println("Agent is not installed")
		return
	}
	
	data, _ := os.ReadFile(configPath)
	var config Config
	json.Unmarshal(data, &config)
	
	fmt.Printf("Agent ID: %s\n", config.AgentID)
	fmt.Printf("Server URL: %s\n", config.ServerURL)
	fmt.Println("Status: Installed")
}

func generateAgentID() string {
	id, err := exec.Command("uuidgen").Output()
	if err != nil {
		return fmt.Sprintf("agent-%d", time.Now().UnixNano())
	}
	return strings.TrimSpace(string(id))
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

func setupSystemdService(serverURL, agentID string) {
	serviceContent := fmt.Sprintf(`[Unit]
Description=Vapor RMM Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/vaporrmm-agent --server %s --agent-id %s
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
`, serverURL, agentID)
	
	servicePath := "/etc/systemd/system/vaporrmm-agent.service"
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		fmt.Printf("Warning: Could not create systemd service: %v\n", err)
		return
	}
	
	exec.Command("systemctl", "daemon-reload").Run()
	exec.Command("systemctl", "enable", "vaporrmm-agent").Run()
	exec.Command("systemctl", "start", "vaporrmm-agent").Run()
	
	fmt.Println("✓ Systemd service created and started")
}
