package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// Agent configuration
type Config struct {
	ServerURL      string `json:"server_url"`
	ClientID       string `json:"client_id"`
	ClientSecret   string `json:"client_secret"`
	HeartbeatInterval int    `json:"heartbeat_interval"`
}

// Device info from Sunshine API
type SunshineStatus struct {
	Version        string `json:"version"`
	PublicKey      string `json:"public_key"`
	Name           string `json:"name"`
	LocalIPs       []string `json:"local_ips"`
	ConnectionUUID string   `json:"connection_uuid"`
}

// Agent status to send to server
type AgentStatus struct {
	ClientID     string    `json:"client_id"`
	Connected    bool      `json:"connected"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Device       DeviceInfo `json:"device"`
}

type DeviceInfo struct {
	ID        uint   `json:"id"`
	MacAddress string  `json:"mac_address"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	IPAddress string  `json:"ip_address"`
}

func loadConfig() Config {
	config := Config{
		ServerURL:      os.Getenv("VAPOR_SERVER_URL"),
		ClientID:       os.Getenv("VAPOR_CLIENT_ID"),
		ClientSecret:   os.Getenv("VAPOR_CLIENT_SECRET"),
		HeartbeatInterval: 30,
	}
	
	if config.ServerURL == "" {
		config.ServerURL = "http://localhost:3001"
	}
	
	return config
}

func getSunshineStatus() (*SunshineStatus, error) {
	resp, err := http.Get("http://localhost:47990/status")
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
		return nil, err
	}

	return &status, nil
}

func getMacAddress() (string, error) {
	resp, err := http.Get("http://localhost:47990/mac")
	if err != nil {
		// Fallback to generating a dummy MAC for testing
		return "00:11:22:33:44:55", nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		MAC string `json:"mac"`
	}
	json.Unmarshal(body, &result)
	return result.MAC, nil
}

func getLocalIP() (string, error) {
	resp, err := http.Get("http://localhost:47990/ip")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		IP string `json:"ip"`
	}
	json.Unmarshal(body, &result)
	return result.IP, nil
}

func sendHeartbeat(config Config) error {
	status, err := getSunshineStatus()
	if err != nil {
		log.Printf("Failed to get Sunshine status: %v", err)
		return err
	}

	macAddr, _ := getMacAddress()
	localIP, _ := getLocalIP()

	agentStatus := AgentStatus{
		ClientID: config.ClientID,
		Connected: true,
		LastHeartbeat: time.Now(),
		Device: DeviceInfo{
			Name:      status.Name,
			MacAddress: macAddr,
			Status:    "online",
			IPAddress: localIP,
		},
	}

	jsonData, _ := json.Marshal(agentStatus)
	
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("POST", config.ServerURL+"/api/heartbeat", io.NopCloser(&jsonData))
	req.Header.Set("Content-Type", "application/json")
	if config.ClientSecret != "" {
		req.Header.Set("Authorization", "Bearer "+config.ClientSecret)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to send heartbeat: %v", err)
		return err
	}
	defer resp.Body.Close()

	log.Println("Heartbeat sent successfully")
	return nil
}

func runForever(config Config) {
	for {
		sendHeartbeat(config)
		time.Sleep(time.Duration(config.HeartbeatInterval) * time.Second)
	}
}

func main() {
	config := loadConfig()
	
	fmt.Printf(" vaporRMM Agent starting...\n")
	fmt.Printf("Server URL: %s\n", config.ServerURL)
	fmt.Printf("Client ID: %s\n", config.ClientID)
	
	// Verify Sunshine is available
	status, err := getSunshineStatus()
	if err != nil {
		log.Printf("Warning: Could not connect to Sunshine API (localhost:47990): %v", err)
	} else {
		fmt.Printf("Connected to Sunshine v%s on %s\n", status.Version, status.Name)
	}
	
	runForever(config)
}