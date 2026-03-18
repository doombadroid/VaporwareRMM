package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	serverURL  = "http://localhost:3001"
	sunshineURL = "http://localhost:3001/api/status"
)

// DeviceInfo holds system information gathered from Sunshine API
type DeviceInfo struct {
	Name      string `json:"name"`
	Hostname  string `json:"hostname"`
	IPAddress string `json:"ip_address"`
	Status    string `json:"status"`
	LastSeen  string `json:"last_seen"`
	Uptime    int64  `json:"uptime"`
	CPU       string `json:"cpu"`
	Memory    uint64 `json:"memory"`
	Disk      uint64 `json:"disk"`
	GPUs      string `json:"gpus"`
	Network   string `json:"network"`
	Drives    string `json:"drives"`
}

// Agent manages the connection to the Vapor RMM server
type Agent struct {
	serverURL string
	hostName  string
	client    *http.Client
}

func NewAgent(serverURL string) *Agent {
	return &Agent{
		serverURL: serverURL,
		hostName:  getHostname(),
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *Agent) Start() error {
	log.Printf("Vapor RMM Agent starting...")
	log.Printf("Server URL: %s", a.serverURL)
	log.Printf("Host Name: %s", a.hostName)

	// Get system info
	deviceInfo, err := a.GetDeviceInfo()
	if err != nil {
		log.Printf("Warning: Could not get device info: %v", err)
	}

	// Register with server
	err = a.RegisterDevice(deviceInfo)
	if err != nil {
		log.Printf("Warning: Could not register device: %v", err)
	}

	// Start heartbeat loop
	go a.heartbeatLoop()

	return nil
}

func (a *Agent) GetDeviceInfo() (*DeviceInfo, error) {
	info := &DeviceInfo{
		Name:      a.hostName,
		Hostname:  a.hostName,
		Status:    "online",
		LastSeen:  time.Now().Format(time.RFC3339),
		Uptime:    getUptime(),
		CPU:       getCPUInfo(),
		Memory:    getMemoryInfo(),
		Disk:      getDiskInfo(),
		GPUs:      getGPUInfo(),
		Network:   getNetworkInfo(),
		Drives:    getDriveInfo(),
	}

	// Get IP address
	ip, err := getPublicIP()
	if err != nil {
		log.Printf("Warning: Could not get public IP: %v", err)
		ip = "unknown"
	}
	info.IPAddress = ip

	return info, nil
}

func (a *Agent) RegisterDevice(info *DeviceInfo) error {
	payload, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal device info: %w", err)
	}

	resp, err := a.client.Post(
		fmt.Sprintf("%s/api/devices/register", a.serverURL),
		"application/json",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return fmt.Errorf("failed to register device: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	log.Printf("Device registered successfully")
	return nil
}

func (a *Agent) UpdateStatus(action string, data interface{}) error {
	status := map[string]interface{}{
		"action":    action,
		"data":      data,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	payload, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal status: %w", err)
	}

	resp, err := a.client.Post(
		fmt.Sprintf("%s/api/status", a.serverURL),
		"application/json",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

func (a *Agent) heartbeatLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		err := a.UpdateStatus("heartbeat", map[string]string{
			"hostname": a.hostName,
		})
		if err != nil {
			log.Printf("Heartbeat failed: %v", err)
		}
	}
}

// System Info Functions

func getHostname() string {
	hostname, _ := os.Hostname()
	return hostname
}

func getUptime() int64 {
	// Try to read from /proc/uptime (Linux)
	if runtime.GOOS == "linux" {
		content, err := os.ReadFile("/proc/uptime")
		if err == nil {
			var uptime float64
			fmt.Sscanf(string(content), "%f", &uptime)
			return int64(uptime)
		}
	}

	// Fallback: use system command
	cmd := exec.Command("uptime", "-p")
	output, _ := cmd.Output()
	// Parse uptime string (e.g., "up 2 hours, 30 minutes")
	return time.Now().Unix() - getBootTime()
}

func getBootTime() int64 {
	if runtime.GOOS == "linux" {
		content, err := os.ReadFile("/proc/stat")
		if err == nil {
			for _, line := range strings.Split(string(content), "\n") {
				if strings.HasPrefix(line, "btime ") {
					var btime int64
					fmt.Sscanf(line, "btime %d", &btime)
					return btime
				}
			}
		}
	}

	cmd := exec.Command("who", "-b")
	output, _ := cmd.Output()
	// Parse last boot time from output
	return time.Now().Unix() - 3600 // Fallback to 1 hour ago
}

func getCPUInfo() string {
	if runtime.GOOS == "linux" {
		content, err := os.ReadFile("/proc/cpuinfo")
		if err == nil {
			for _, line := range strings.Split(string(content), "\n") {
				if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "processor") {
					return strings.TrimSpace(strings.TrimPrefix(line, "model name:"))
				}
			}
		}
	}

	cmd := exec.Command("uname", "-m")
	output, _ := cmd.Output()
	return string(output)
}

func getMemoryInfo() uint64 {
	if runtime.GOOS == "linux" {
		content, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			for _, line := range strings.Split(string(content), "\n") {
				if strings.HasPrefix(line, "MemTotal:") {
					var memKB uint64
					fmt.Sscanf(line, "MemTotal: %d kB", &memKB)
					return memKB * 1024
				}
			}
		}
	}

	cmd := exec.Command("free", "-b")
	output, _ := cmd.Output()
	// Parse free command output
	return 8 * 1024 * 1024 * 1024 // Fallback to 8GB
}

func getDiskInfo() uint64 {
	if runtime.GOOS == "linux" {
		cmd := exec.Command("df", "--output=size", "/")
		output, _ := cmd.Output()
		var size int64
		fmt.Sscanf(string(output), "%d", &size)
		return uint64(size * 1024) // Convert KB to bytes
	}

	return 500 * 1024 * 1024 * 1024 // Fallback to 500GB
}

func getGPUInfo() string {
	if runtime.GOOS == "linux" {
		cmd := exec.Command("lspci")
		output, _ := cmd.Output()
		gpus := []string{}
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, "VGA") || strings.Contains(line, "3D") || strings.Contains(line, "Display") {
				gpus = append(gpus, strings.TrimSpace(strings.TrimPrefix(line, "00:")))
			}
		}
		return strings.Join(gpus, "; ")
	}

	return "Unknown GPU"
}

func getNetworkInfo() string {
	cmd := exec.Command("hostname", "-I")
	output, _ := cmd.Output()
	ipStr := strings.TrimSpace(string(output))

	if ipStr == "" {
		ipStr = "127.0.0.1"
	}
	return ipStr
}

func getDriveInfo() string {
	if runtime.GOOS == "linux" {
		cmd := exec.Command("lsblk", "-o", "NAME,SIZE,MOUNTPOINT,FSTYPE")
		output, _ := cmd.Output()
		return strings.TrimSpace(string(output))
	}
	return "Unknown drives"
}

func getPublicIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org?format=text")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, _ := os.ReadFile("/proc/sys/net/ipv4/conf/all/shared_media")
	content, _ := os.ReadFile("/sys/class/net/eth0/address")
	
	resp2, err2 := http.Get("https://ifconfig.me")
	if err2 != nil {
		return "127.0.0.1", nil
	}
	defer resp2.Body.Close()

	body, _ := os.ReadAll(resp2.Body)
	return strings.TrimSpace(string(body)), nil
}

func main() {
	log.Println("Starting Vapor RMM Agent...")
	
	agent := NewAgent(serverURL)
	if err := agent.Start(); err != nil {
		log.Fatalf("Agent failed to start: %v", err)
	}

	log.Println("Agent running. Press Ctrl+C to stop.")
	select {}
}