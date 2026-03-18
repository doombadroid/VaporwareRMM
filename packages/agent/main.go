package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

const (
	DefaultServerURL = "http://localhost:8080"
	DefaultAgentPort = 47991
)

type Agent struct {
	serverURL    string
	port         int
	hostname     string
	deviceID     string
	lastCommands []CommandResult
}

type CommandRequest struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"` // shell, script, ping, reboot
	Payload   map[string]interface{} `json:"payload"`
	CreatedAt time.Time              `json:"created_at"`
}

type CommandResult struct {
	CommandID string                 `json:"command_id"`
	Success   bool                   `json:"success"`
	Output    string                 `json:"output,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

func NewAgent(serverURL string, port int) *Agent {
	hostname, _ := os.Hostname()
	return &Agent{
		serverURL: serverURL,
		port:      port,
		hostname:  hostname,
	}
}

func (a *Agent) Start() error {
	log.Printf("Starting Vapor RMM Agent on port %d", a.port)
	log.Printf("Server URL: %s", a.serverURL)

	http.HandleFunc("/agent/register", a.handleRegister)
	http.HandleFunc("/agent/heartbeat", a.handleHeartbeat)
	http.HandleFunc("/agent/commands", a.handleCommands)
	http.HandleFunc("/agent/results", a.handleResults)
	http.HandleFunc("/agent/run", a.handleRunCommand)
	http.HandleFunc("/metrics", a.handleMetrics)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", a.port), nil); err != nil {
		return fmt.Errorf("failed to start agent server: %w", err)
	}
	return nil
}

func (a *Agent) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	registration := a.getRegistrationInfo()
	data, _ := json.Marshal(registration)

	req, err := http.NewRequest("POST", a.serverURL+"/agent/register", bytes.NewBuffer(data))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to register: %v", err), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Server not reachable: %v", err), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (a *Agent) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := a.getStatus()
	data, _ := json.Marshal(status)

	req, err := http.NewRequest("POST", a.serverURL+"/agent/heartbeat", bytes.NewBuffer(data))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed heartbeat: %v", err), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Server not reachable: %v", err), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (a *Agent) handleCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	req, err := http.Get(a.serverURL + fmt.Sprintf("/agent/%s/commands", a.hostname))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch commands: %v", err), http.StatusInternalServerError)
		return
	}
	defer req.Body.Close()

	var commands []CommandRequest
	json.NewDecoder(req.Body).Decode(&commands)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(commands)
}

func (a *Agent) handleResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	results := a.lastCommands
	a.lastCommands = nil // Clear after sending

	data, _ := json.Marshal(map[string]interface{}{
		"results": results,
	})

	req, err := http.Post(a.serverURL+fmt.Sprintf("/agent/%s/results", a.hostname), "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Failed to send results: %v", err)
		http.Error(w, fmt.Sprintf("Failed to send results: %v", err), http.StatusInternalServerError)
		return
	}
	defer req.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (a *Agent) handleRunCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var cmdReq struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	json.NewDecoder(r.Body).Decode(&cmdReq)

	output := ""
	success := true

	switch strings.ToLower(cmdReq.Type) {
	case "shell":
		if runtime.GOOS == "windows" {
			cmd := exec.Command("cmd.exe", "/C", cmdReq.Command)
			outputBytes, err := cmd.CombinedOutput()
			output = string(outputBytes)
			if err != nil {
				success = false
			}
		} else {
			cmd := exec.Command("/bin/sh", "-c", cmdReq.Command)
			outputBytes, err := cmd.CombinedOutput()
			output = string(outputBytes)
			if err != nil {
				success = false
			}
		}
	case "script":
		if runtime.GOOS == "windows" {
			cmd := exec.Command("cmd.exe", "/C", cmdReq.Command)
			outputBytes, err := cmd.CombinedOutput()
			output = string(outputBytes)
			if err != nil {
				success = false
			}
		} else {
			cmd := exec.Command("/bin/sh", "-c", cmdReq.Command)
			outputBytes, err := cmd.CombinedOutput()
			output = string(outputBytes)
			if err != nil {
				success = false
			}
		}
	default:
		success = false
		output = "Unknown command type"
	}

	result := CommandResult{
		CommandID: fmt.Sprintf("cmd_%d", time.Now().Unix()),
		Success:   success,
		Output:    output,
		Timestamp: time.Now(),
	}

	a.lastCommands = append(a.lastCommands, result)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (a *Agent) getRegistrationInfo() map[string]interface{} {
	cpuInfo, _ := cpu.Info()
	memInfo, _ := mem.VirtualMemory()
	diskInfo, _ := disk.Usage("/")
	hostInfo, _ := host.Info()

	var localIPs []string

	ips, err := net.Interfaces()
	if err != nil {
		log.Printf("Warning: Could not get network interfaces: %v", err)
	}

	for _, iface := range ips {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				localIPs = append(localIPs, ipNet.IP.String())
			}
		}
	}

	var publicIP string
	if len(localIPs) > 0 {
		publicIP = localIPs[0]
	}

	var mac string
	for _, iface := range ips {
		if len(iface.HardwareAddr) > 0 {
			mac = iface.HardwareAddr.String()
			break
		}
	}

	return map[string]interface{}{
		"hostname":      a.hostname,
		"os":            hostInfo.OS,
		"os_version":    hostInfo.PlatformVersion,
		"public_ip":     publicIP,
		"local_ips":     localIPs,
		"mac_address":   mac,
		"cpu":           getCPUName(cpuInfo),
		"ram":           memInfo.Total,
		"storage":       diskInfo.Total,
		"uptime":        hostInfo.Uptime,
		"agent_version": "1.0.0",
	}
}

func (a *Agent) getStatus() map[string]interface{} {
	cpuPercent, _ := cpu.Percent(0, false)
	memInfo, _ := mem.VirtualMemory()
	diskInfo, _ := disk.Usage("/")
	hostInfo, _ := host.Info()

	deviceID := a.deviceID
	if deviceID == "" {
		deviceID = a.hostname
	}

	return map[string]interface{}{
		"id":        deviceID,
		"hostname":  a.hostname,
		"status":    "online",
		"cpu":       cpuPercent[0],
		"ram":       memInfo.Used,
		"storage":   diskInfo.Used,
		"uptime":    hostInfo.Uptime,
		"timestamp": time.Now().Unix(),
	}
}

func getCPUName(info []cpu.InfoStat) string {
	if len(info) == 0 {
		return "Unknown"
	}
	return info[0].ModelName
}

func (a *Agent) SetDeviceID(id string) {
	a.deviceID = id
	log.Printf("Agent registered with device ID: %s", id)
}

func (a *Agent) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get CPU metrics
	cpuPercent, _ := cpu.Percent(0, false)
	cpuInfo, _ := cpu.Info()

	// Get memory metrics
	memInfo, _ := mem.VirtualMemory()

	// Get disk metrics
	diskInfo, _ := disk.Usage("/")

	// Get system load (1m, 5m, 15m)
	loadAvg, _ := host.LoadAverage()

	metrics := map[string]interface{}{
		"timestamp": time.Now().Unix(),
		"cpu": map[string]interface{}{
			"percent":     cpuPercent[0],
			"cores":       runtime.NumCPU(),
			"model":       getCPUName(cpuInfo),
		},
		"memory": map[string]interface{}{
			"total":       memInfo.Total,
			"used":        memInfo.Used,
			"free":        memInfo.Free,
			"percent":     memInfo.Percent,
		},
		"disk": map[string]interface{}{
			"total":       diskInfo.Total,
			"used":        diskInfo.Used,
			"free":        diskInfo.Free,
			"percent":     diskInfo.Percent,
		},
		"load": map[string]interface{}{
			"1m":  loadAvg[0],
			"5m":  loadAvg[1],
			"15m": loadAvg[2],
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

func main() {
	serverURL := os.Getenv("VAPOR_SERVER_URL")
	if serverURL == "" {
		serverURL = DefaultServerURL
	}

	port := 47991 // Default agent port
	if p, ok := os.LookupEnv("VAPOR_AGENT_PORT"); ok {
		if parsedPort, err := strconv.Atoi(p); err == nil {
			port = parsedPort
		}
	}

	agent := NewAgent(serverURL, port)

	// Register with server first
	resp, err := http.Get(fmt.Sprintf("%s/health", serverURL))
	if err == nil {
		defer resp.Body.Close()
		log.Println("Server is reachable")
	} else {
		log.Printf("Warning: Cannot connect to server at %s: %v", serverURL, err)
	}

	// Start agent server
	if err := agent.Start(); err != nil {
		log.Fatalf("Agent failed: %v", err)
	}
}