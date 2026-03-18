package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	
	switch command {
	case "install":
		handleInstall()
	case "uninstall":
		handleUninstall()
	case "start":
		handleStart()
	case "status":
		handleStatus()
	case "stop":
		handleStop()
	case "version":
		fmt.Printf("vaporrmm-cli version %s\n", version)
	case "--help", "-h":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Vapor RMM CLI - Remote Machine Management
==========================================

Usage: vaporrmm <command>

Commands:
  install     Install Vapor RMM agent service
  uninstall   Remove Vapor RMM agent service
  start       Start the agent service
  stop        Stop the agent service
  status      Show agent status
  version     Show version information
  --help, -h  Show this help message

Examples:
  vaporrmm install    Install as a system service
  vaporrmm start      Start the agent
  vaporrmm status     Check if agent is running
`)
}

func handleInstall() {
	fmt.Println("Installing Vapor RMM Agent...")
	
	// Determine the installation path
	installPath := "/opt/vaporrmm"
	if runtimeOS() == "windows" {
		installPath = filepath.Join(os.Getenv("ProgramFiles"), "VaporRMM")
	}
	
	// Create directory
	err := os.MkdirAll(installPath, 0755)
	if err != nil {
		fmt.Printf("Error creating directory: %v\n", err)
		os.Exit(1)
	}

	// Copy agent binary
	agentBin, err := getAgentBinary()
	if err != nil {
		fmt.Printf("Warning: Could not find agent binary: %v\n", err)
	} else {
		target := filepath.Join(installPath, "agent")
		err = copyFile(agentBin, target)
		if err != nil {
			fmt.Printf("Warning: Could not copy agent binary: %v\n", err)
		} else {
			fmt.Println("Agent binary installed")
		}
	}

	// Create config file
	config := map[string]string{
		"server_url": "http://localhost:3001",
	}
	configPath := filepath.Join(installPath, "config.json")
	configJSON, _ := json.MarshalIndent(config, "", "  ")
	err = os.WriteFile(configPath, configJSON, 0644)
	if err != nil {
		fmt.Printf("Warning: Could not write config: %v\n", err)
	} else {
		fmt.Println("Config file created")
	}

	// Create service file (Linux systemd)
	if runtimeOS() == "linux" {
		createServiceFile(installPath)
	}

	fmt.Println("\nInstallation complete!")
	fmt.Println("\nTo start the agent, run:")
	fmt.Printf("  vaporrmm start\n")
}

func handleUninstall() {
	fmt.Println("Uninstalling Vapor RMM Agent...")
	
	// Stop service if running
	stopAgent()

	installPath := "/opt/vaporrmm"
	if runtimeOS() == "windows" {
		installPath = filepath.Join(os.Getenv("ProgramFiles"), "VaporRMM")
	}

	// Remove service file (Linux)
	if runtimeOS() == "linux" {
		serviceFile := "/etc/systemd/system/vaporrmm-agent.service"
		os.Remove(serviceFile)
		fmt.Println("Service file removed")
	}

	// Remove directory
	err := os.RemoveAll(installPath)
	if err != nil {
		fmt.Printf("Error removing installation: %v\n", err)
	} else {
		fmt.Println("Installation removed successfully")
	}
}

func handleStart() {
	if runtimeOS() == "linux" {
		cmd := exec.Command("systemctl", "start", "vaporrmm-agent")
		err := cmd.Run()
		if err != nil {
			fmt.Println("Starting agent directly...")
			startAgentDirect()
		} else {
			fmt.Println("Agent service started via systemctl")
		}
	} else {
		startAgentDirect()
	}
}

func handleStop() {
	stopAgent()
	if runtimeOS() == "linux" {
		cmd := exec.Command("systemctl", "stop", "vaporrmm-agent")
		cmd.Run()
		fmt.Println("Agent service stopped via systemctl")
	}
}

func handleStatus() {
	fmt.Println("Vapor RMM Agent Status:")
	
	// Check if process is running
	if isProcessRunning("agent") || isProcessRunning("vaporrmm") {
		fmt.Println("  Status: Running")
	} else {
		fmt.Println("  Status: Not running")
	}

	// Check config
	configPath := "/opt/vaporrmm/config.json"
	if runtimeOS() == "windows" {
		configPath = filepath.Join(os.Getenv("ProgramFiles"), "VaporRMM", "config.json")
	}
	
	if _, err := os.Stat(configPath); err == nil {
		fmt.Println("  Config: Found")
	} else {
		fmt.Println("  Config: Not found")
	}

	// Server status
	fmt.Println("\nServer Status:")
	cmd := exec.Command("curl", "-s", "--max-time", "2", "http://localhost:3001/api/health")
	output, err := cmd.Output()
	if err == nil {
		fmt.Println("  Server: Online")
	} else {
		fmt.Println("  Server: Not reachable (is it running?)")
	}
}

func startAgentDirect() {
	cmd := exec.Command("pkill", "-f", "vaporrmm.*agent")
	cmd.Run()

	args := []string{"-d"}
	if runtimeOS() == "linux" {
		args = append(args, "/opt/vaporrmm/agent")
	} else {
		// Try to find agent in PATH
		args = append(args, "agent")
	}

	cmd = exec.Command("nohup", args[0], args[1:]...)
	cmd.Start()

	fmt.Println("Agent started")
}

func stopAgent() {
	if runtimeOS() == "windows" {
		exec.Command("taskkill", "/F", "/IM", "agent.exe").Run()
	} else {
		exec.Command("pkill", "-f", "vaporrmm.*agent").Run()
	}
	fmt.Println("Agent stopped")
}

func isProcessRunning(name string) bool {
	cmd := exec.Command("pgrep", "-f", name)
	err := cmd.Run()
	return err == nil
}

func getAgentBinary() (string, error) {
	// Look for agent binary in common locations
	locations := []string{
		"../../agent/agent",
		"/opt/vaporrmm/agent",
		"./agent",
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc, nil
		}
	}

	return "", fmt.Errorf("agent binary not found")
}

func copyFile(src, dst string) error {
	content, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, content, 0755)
}

func runtimeOS() string {
	if strings.HasPrefix(os.Getenv("GOOS"), "windows") || 
	   (len(os.Args) > 0 && os.Args[0] == ".exe") {
		return "windows"
	}
	
	switch os := runtime.GOOS; os {
	case "linux":
		return "linux"
	case "darwin":
		return "darwin"
	default:
		return os
	}
}

func createServiceFile(installPath string) {
	serviceContent := fmt.Sprintf(`[Unit]
Description=Vapor RMM Agent
After=network.target

[Service]
Type=simple
ExecStart=%s/agent
Restart=always
RestartSec=5
WorkingDirectory=%s
StandardOutput=syslog
StandardError=syslog
SyslogIdentifier=vaporrmm-agent

[Install]
WantedBy=multi-user.target
`, installPath, installPath)

	serviceFile := "/etc/systemd/system/vaporrmm-agent.service"
	err := os.WriteFile(serviceFile, []byte(serviceContent), 0644)
	if err != nil {
		fmt.Printf("Warning: Could not create service file: %v\n", err)
	} else {
		fmt.Println("Systemd service file created")
		fmt.Println("\nTo enable and start the service:")
		fmt.Println("  sudo systemctl daemon-reload")
		fmt.Println("  sudo systemctl enable vaporrmm-agent")
		fmt.Println("  sudo systemctl start vaporrmm-agent")
	}
}

// Helper function to detect runtime GOOS
func runtimeGOOS() string {
	return runtime.GOOS
}