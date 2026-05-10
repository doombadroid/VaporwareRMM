package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

// InventorySoftware mirrors the server-side struct. Keep field names in
// sync with handlers/inventory.go.
type InventorySoftware struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Vendor      string `json:"vendor,omitempty"`
	InstallDate int64  `json:"install_date,omitempty"`
}

// InventoryHardware likewise mirrors the server struct.
type InventoryHardware struct {
	CPUModel        string `json:"cpu_model,omitempty"`
	CPUCores        int    `json:"cpu_cores,omitempty"`
	RAMBytes        int64  `json:"ram_bytes,omitempty"`
	DiskTotalBytes  int64  `json:"disk_total_bytes,omitempty"`
	Platform        string `json:"platform,omitempty"`
	PlatformVersion string `json:"platform_version,omitempty"`
	KernelVersion   string `json:"kernel_version,omitempty"`
}

// inventoryLoop runs collectInventory once at startup (after a brief
// settling delay) and then on a ticker.
func (a *Agent) inventoryLoop() {
	// Wait for registration to complete; without device_id the post 404s.
	time.Sleep(60 * time.Second)
	a.collectAndPostInventory()

	ticker := time.NewTicker(InventoryInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !a.registered || a.deviceID == "" {
			continue
		}
		a.collectAndPostInventory()
	}
}

func (a *Agent) collectAndPostInventory() {
	if a.deviceID == "" {
		return
	}

	software := collectSoftware()
	if len(software) > MaxSoftwareEntries {
		software = software[:MaxSoftwareEntries]
	}
	hardware := collectHardware()

	body := map[string]interface{}{
		"software": software,
		"hardware": hardware,
	}
	data, err := json.Marshal(body)
	if err != nil {
		slog.Warn("inventory marshal failed", "error", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, a.serverURL+"/agent/inventory/"+a.deviceID, bytes.NewBuffer(data))
	if err != nil {
		slog.Warn("inventory request build failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiToken)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		slog.Warn("inventory post failed", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("inventory rejected", "status", resp.StatusCode)
		return
	}
	slog.Info("inventory posted", "software_count", len(software))
}

// collectHardware reads platform-independent hardware info via gopsutil.
// Kernel/platform fields differ by OS but gopsutil normalizes them.
func collectHardware() *InventoryHardware {
	h := &InventoryHardware{}
	if cpus, err := cpu.Info(); err == nil && len(cpus) > 0 {
		h.CPUModel = cpus[0].ModelName
	}
	if c, err := cpu.Counts(true); err == nil {
		h.CPUCores = c
	}
	if v, err := mem.VirtualMemory(); err == nil {
		h.RAMBytes = int64(v.Total)
	}
	if parts, err := disk.Partitions(false); err == nil {
		var total uint64
		for _, p := range parts {
			if u, err := disk.Usage(p.Mountpoint); err == nil {
				total += u.Total
			}
		}
		h.DiskTotalBytes = int64(total)
	}
	if info, err := host.Info(); err == nil {
		h.Platform = info.Platform
		h.PlatformVersion = info.PlatformVersion
		h.KernelVersion = info.KernelVersion
	}
	if h.Platform == "" {
		h.Platform = runtime.GOOS
	}
	return h
}

// reportSoftwareError logs and continues; partial inventory is better
// than nothing on a host that has e.g. dpkg but not rpm.
func reportSoftwareError(source string, err error) {
	slog.Debug("software collector skipped", "source", source, "error", fmt.Sprint(err))
}
