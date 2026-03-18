package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

// SystemInfo holds system metadata
type SystemInfo struct {
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	Platform    string `json:"platform"`
	Kernel      string `json:"kernel"`
	Uptime      uint64 `json:"uptime"`
	MachineArch string `json:"machineArch"`
}

// CPUStats holds CPU usage statistics
type CPUStats struct {
	CPUTime     map[string]float64 `json:"cpuTime"`
	Percent     float64            `json:"percent"`
	Temperature float64            `json:"temperature,omitempty"`
}

// MemoryStats holds memory usage statistics
type MemoryStats struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"usedPercent"`
	SwapTotal   uint64  `json:"swapTotal"`
	SwapUsed    uint64  `json:"swapUsed"`
	SwapFree    uint64  `json:"swapFree"`
}

// DiskStats holds disk usage statistics
type DiskStats struct {
	Path        string  `json:"path"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"usedPercent"`
}

// SunshineStatus represents the current state of Sunshine
type SunshineStatus struct {
	Running     bool   `json:"running"`
	Version     string `json:"version,omitempty"`
	Ports       []int  `json:"ports,omitempty"`
	Error       string `json:"error,omitempty"`
}

// AgentConfig holds agent configuration
type AgentConfig struct {
	ServerURL    string        `mapstructure:"server_url" env:"SERVER_URL"`
	AgentID      string        `mapstructure:"agent_id" env:"AGENT_ID"`
	UpdatePeriod time.Duration `mapstructure:"update_period" env:"UPDATE_PERIOD"`
	Retries      int           `mapstructure:"retries" env:"RETRIES"`
}

// Agent represents the main agent struct
type Agent struct {
	app          *fiber.App
	config       AgentConfig
	serverStatus *SunshineStatus
}

func main() {
	// Load configuration from environment
	config := AgentConfig{
		ServerURL:    getEnv("SERVER_URL", "http://localhost:8000"),
		AgentID:      getEnv("AGENT_ID", generateAgentID()),
		UpdatePeriod: 15 * time.Second,
		Retries:      3,
	}

	if val, ok := os.LookupEnv("UPDATE_PERIOD"); ok {
		if duration, err := time.ParseDuration(val); err == nil {
			config.UpdatePeriod = duration
		}
	}

	agent := NewAgent(config)
	
	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down agent...")
		if err := agent.Shutdown(); err != nil {
			log.Printf("Error during shutdown: %v", err)
		}
		os.Exit(0)
	}()

	if err := agent.Start(); err != nil {
		log.Fatalf("Failed to start agent: %v", err)
	}
}

// NewAgent creates a new Agent instance
func NewAgent(config AgentConfig) *Agent {
	app := fiber.New(fiber.Config{
		EnablePPROF:          false,
		StreamRequestBody:    true,
		MaxRequestBodySize:   1024 * 1024, // 1MB
		ReadBufferSize:       4096,
		Concurrency:          256 * 1024,
	})

	agent := &Agent{
		app:          app,
		config:       config,
		serverStatus: &SunshineStatus{},
	}

	// Setup routes
	agent.setupRoutes()

	return agent
}

func (a *Agent) setupRoutes() {
	// Health check endpoint
	a.app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "healthy",
			"agent":  a.config.AgentID,
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	// System info endpoint
	a.app.Get("/system/info", a.handleSystemInfo)

	// CPU stats endpoint
	a.app.Get("/system/cpu", a.handleCPUStats)

	// Memory stats endpoint
	a.app.Get("/system/memory", a.handleMemoryStats)

	// Disk stats endpoint
	a.app.Get("/system/disk", a.handleDiskStats)

	// Sunshine status endpoint
	a.app.Get("/sunshine/status", a.handleSunshineStatus)

	// Full system report endpoint
	a.app.Get("/report/system", a.handleFullReport)

	// Register agent with server
	a.app.Post("/register", a.handleRegister)

	// Heartbeat endpoint
	a.app.Post("/heartbeat", a.handleHeartbeat)
}

func (a *Agent) handleSystemInfo(c *fiber.Ctx) error {
	info, err := host.Info()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to get system info: %v", err),
		})
	}

	return c.JSON(SystemInfo{
		Hostname:    info.Hostname,
		OS:          info.OS,
		Platform:    info.Platform,
		Kernel:      info.Kernel,
		Uptime:      info.Uptime,
		MachineArch: info.PlatformFamily + "/" + info.Platform,
	})
}

func (a *Agent) handleCPUStats(c *fiber.Ctx) error {
	// Get CPU times
	times, err := cpu.Times(true)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to get CPU times: %v", err),
		})
	}

	// Calculate overall percentage
	percent, err := cpu.Percent(0, false)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to get CPU percent: %v", err),
		})
	}

	cpuTime := make(map[string]float64)
	for _, t := range times {
		total := t.User + t.System + t.Idle + t.Nice + t.Iowait
		if total > 0 {
			cpuTime[t.CPU] = (t.User + t.System) / total * 100
		}
	}

	return c.JSON(CPUStats{
		CPUTime: cpuTime,
		Percent: percent[0],
	})
}

func (a *Agent) handleMemoryStats(c *fiber.Ctx) error {
	vmem, err := mem.VirtualMemory()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to get memory stats: %v", err),
		})
	}

	swap, err := mem.SwapMemory()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to get swap stats: %v", err),
		})
	}

	return c.JSON(MemoryStats{
		Total:       vmem.Total,
		Used:        vmem.Used,
		Free:        vmem.Free,
		UsedPercent: vmem.UsedPercent,
		SwapTotal:   swap.Total,
		SwapUsed:    swap.Used,
		SwapFree:    swap.Free,
	})
}

func (a *Agent) handleDiskStats(c *fiber.Ctx) error {
	partitions, err := disk.Partitions(false)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("failed to get partitions: %v", err),
		})
	}

	var disks []DiskStats
	for _, p := range partitions {
		if strings.HasPrefix(p.Mountpoint, "/sys") || 
		   strings.HasPrefix(p.Mountpoint, "/proc") ||
		   strings.HasPrefix(p.Mountpoint, "/dev") {
			continue
		}

		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue
		}

		disks = append(disks, DiskStats{
			Path:        p.Mountpoint,
			Total:       usage.Total,
			Used:        usage.Used,
			Free:        usage.Free,
			UsedPercent: usage.UsedPercent,
		})
	}

	return c.JSON(disks)
}

func (a *Agent) handleSunshineStatus(c *fiber.Ctx) error {
	status := *a.serverStatus
	
	if !status.Running {
		// Try to detect Sunshine
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://127.0.0.1:47990")
		if err == nil && resp.StatusCode == 200 {
			status.Running = true
			status.Ports = []int{47990}
			resp.Body.Close()
		} else {
			status.Error = "Sunshine not detected or not running"
		}
	}

	return c.JSON(status)
}

func (a *Agent) handleFullReport(c *fiber.Ctx) error {
	report := make(fiber.Map)

	// System info
	info, err := host.Info()
	if err == nil {
		report["system"] = SystemInfo{
			Hostname:    info.Hostname,
			OS:          info.OS,
			Platform:    info.Platform,
			Kernel:      info.Kernel,
			Uptime:      info.Uptime,
			MachineArch: info.PlatformFamily + "/" + info.Platform,
		}
	}

	// CPU stats
	percent, _ := cpu.Percent(0, false)
	report["cpu"] = fiber.Map{
		"percent": percent[0],
	}

	// Memory stats
	vmem, _ := mem.VirtualMemory()
	report["memory"] = fiber.Map{
		"usedPercent": vmem.UsedPercent,
		"totalGB":     float64(vmem.Total) / (1024 * 1024 * 1024),
	}

	// Disk stats
	partitions, _ := disk.Partitions(false)
	var totalDisk uint64
	var usedDisk uint64
	for _, p := range partitions {
		if strings.HasPrefix(p.Mountpoint, "/sys") || 
		   strings.HasPrefix(p.Mountpoint, "/proc") ||
		   strings.HasPrefix(p.Mountpoint, "/dev") {
			continue
		}
		usage, _ := disk.Usage(p.Mountpoint)
		totalDisk += usage.Total
		usedDisk += usage.Used
	}
	report["disk"] = fiber.Map{
		"totalGB":  float64(totalDisk) / (1024 * 1024 * 1024),
		"usedGB":   float64(usedDisk) / (1024 * 1024 * 1024),
		"usedPct":  usedDisk / float64(totalDisk) * 100,
	}

	// Sunshine status
	report["sunshine"] = a.serverStatus

	return c.JSON(report)
}

func (a *Agent) handleRegister(c *fiber.Ctx) error {
	var req struct {
		ServerURL string `json:"server_url"`
	}
	
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	// Store the server URL
	a.config.ServerURL = req.ServerURL

	return c.JSON(fiber.Map{
		"status":  "registered",
		"agentID": a.config.AgentID,
		"url":     req.ServerURL,
	})
}

func (a *Agent) handleHeartbeat(c *fiber.Ctx) error {
	var req struct {
		Status string `json:"status"`
	}
	
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	go a.updateSunshineStatus()

	return c.JSON(fiber.Map{
		"status":     "heartbeat received",
		"agentID":    a.config.AgentID,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"sunshine":   a.serverStatus.Running,
	})
}

func (a *Agent) updateSunshineStatus() {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:47990")
	if err == nil && resp.StatusCode == http.StatusOK {
		a.serverStatus.Running = true
		a.serverStatus.Error = ""
		
		var data struct {
			Version string `json:"version"`
		}
		json.NewDecoder(resp.Body).Decode(&data)
		a.serverStatus.Version = data.Version
		resp.Body.Close()
	} else {
		a.serverStatus.Running = false
		a.serverStatus.Error = "Sunshine not running or unreachable"
	}
}

func (a *Agent) Start() error {
	port := getEnv("PORT", "3001")
	
	log.Printf("Starting vapor-rmm agent on port %s", port)
	log.Printf("Agent ID: %s", a.config.AgentID)
	log.Printf("Server URL: %s", a.config.ServerURL)

	// Update Sunshine status
	go a.updateSunshineStatus()

	return a.app.Listen(":" + port)
}

func (a *Agent) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := a.app.ShutdownWithContext(ctx); err != nil {
		return err
	}
	
	log.Println("Agent shutdown complete")
	return nil
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func generateAgentID() string {
	id := make([]byte, 8)
	for i := range id {
		id[i] = byte('a' + (i % 26))
	}
	return "agent-" + string(id) + "-" + time.Now().Format("20060102150405")
}