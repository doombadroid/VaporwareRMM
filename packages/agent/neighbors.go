package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// AgentNeighborEntry is one observed L2 neighbor (ARP / IPv6 ND).
type AgentNeighborEntry struct {
	IP       string `json:"ip"`
	MAC      string `json:"mac,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Iface    string `json:"iface,omitempty"`
}

// NeighborInterval is the cadence for ARP-table sweeps. Slower than
// heartbeat; ARP entries don't change minute-to-minute on most LANs.
const NeighborInterval = 1 * time.Hour

func (a *Agent) neighborLoop() {
	time.Sleep(120 * time.Second)
	a.collectAndPostNeighbors()

	ticker := time.NewTicker(NeighborInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !a.registered || a.deviceID == "" {
			continue
		}
		a.collectAndPostNeighbors()
	}
}

func (a *Agent) collectAndPostNeighbors() {
	if a.deviceID == "" {
		return
	}
	neighbors := collectNeighbors()
	body := map[string]interface{}{"neighbors": neighbors}
	data, err := json.Marshal(body)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, a.serverURL+"/agent/neighbors/"+a.deviceID, bytes.NewBuffer(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiToken)
	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	slog.Info("neighbors posted", "count", len(neighbors), "status", resp.StatusCode)
}
