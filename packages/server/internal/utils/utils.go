package utils

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"vaporrmm/models"
)

var (
	ServerPort  int
	AgentWSPort int
)

// ResponseWriter adapts bytes.Buffer to http.ResponseWriter for promhttp.
type ResponseWriter struct {
	Body       *bytes.Buffer
	HTTPHeader http.Header
	Code       int
}

func (w *ResponseWriter) Header() http.Header         { return w.HTTPHeader }
func (w *ResponseWriter) Write(b []byte) (int, error) { return w.Body.Write(b) }
func (w *ResponseWriter) WriteHeader(code int)        { w.Code = code }

// agentHTTPClient is a shared client with optional TLS config for agent communication.
var agentHTTPClient = &http.Client{Timeout: 30 * time.Second}

func init() {
	// Optional mTLS for agent command delivery
	agentTLSConfig := buildAgentTLSConfig()
	if agentTLSConfig != nil {
		agentHTTPClient.Transport = &http.Transport{
			TLSClientConfig: agentTLSConfig,
		}
	}
}

// buildAgentTLSConfig returns a tls.Config if AGENT_CA_CERT is set.
func buildAgentTLSConfig() *tls.Config {
	caCertPath := os.Getenv("AGENT_CA_CERT")
	if caCertPath == "" {
		return nil
	}

	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		slog.Error("failed to read AGENT_CA_CERT", "path", caCertPath, "error", err)
		return nil
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		slog.Error("failed to parse AGENT_CA_CERT")
		return nil
	}

	config := &tls.Config{
		RootCAs:    caCertPool,
		MinVersion: tls.VersionTLS12,
	}

	// Optional client certificate for mTLS
	clientCertPath := os.Getenv("AGENT_CLIENT_CERT")
	clientKeyPath := os.Getenv("AGENT_CLIENT_KEY")
	if clientCertPath != "" && clientKeyPath != "" {
		cert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
		if err != nil {
			slog.Error("failed to load agent client cert/key", "error", err)
		} else {
			config.Certificates = []tls.Certificate{cert}
		}
	}

	return config
}

// validateAgentIP enforces the same SSRF guard for every server→agent
// outbound call. Loopback / link-local / metadata addresses are refused;
// private addresses are NOT refused because legitimate agents sit on
// 10.x / 192.168.x / 100.64.x (Tailscale CGNAT) — the trust root for
// agent calls is the bearer token, not the address class.
func validateAgentIP(agentIP string) error {
	check := func(ip net.IP) error {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("agent IP %s is not allowed", ip.String())
		}
		// Cloud-metadata: AWS/GCP/Azure 169.254.169.254 is link-local
		// (caught above), but Alibaba's 100.100.100.200 sits in CGNAT
		// alongside legitimate Tailscale addresses, so check that one
		// specifically instead of refusing all of 100.64.0.0/10.
		if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] == 100 && ip4[2] == 100 && ip4[3] == 200 {
			return fmt.Errorf("agent IP %s is a cloud-metadata address", ip.String())
		}
		return nil
	}
	if ip := net.ParseIP(agentIP); ip != nil {
		return check(ip)
	}
	ips, err := net.LookupIP(agentIP)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("failed to resolve agent IP %s: %w", agentIP, err)
	}
	for _, resolved := range ips {
		if err := check(resolved); err != nil {
			return fmt.Errorf("agent IP %s resolves to disallowed address: %w", agentIP, err)
		}
	}
	return nil
}

// SendCommandToDevice sends a command to an agent at its tracked address with authorization.
func SendCommandToDevice(agentIP string, agentPort int, apiToken string, cmdData []byte) error {
	if agentIP == "" {
		agentIP = "localhost"
	}
	if agentPort == 0 {
		agentPort = 47991
	}

	if err := validateAgentIP(agentIP); err != nil {
		return err
	}

	scheme := "http"
	if os.Getenv("AGENT_CA_CERT") != "" {
		scheme = "https"
	}

	url := fmt.Sprintf("%s://%s:%d/agent/run", scheme, agentIP, agentPort)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(cmdData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := agentHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send command to agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent returned non-OK status: %d", resp.StatusCode)
	}

	return nil
}

// scanner is an interface that covers both *sql.Row and *sql.Rows
type scanner interface {
	Scan(dest ...interface{}) error
}

// ScanDevice scans a device from either a *sql.Row or *sql.Rows.
// Handles NULL columns gracefully by using sql.NullString intermediates.
func ScanDevice(s scanner) (*models.ServerDevice, error) {
	d := &models.ServerDevice{}
	var tagsStr string

	// Use NullString for all text columns that may be NULL
	var name, hostname, ipAddr, macAddr, osName, osVer, kernelVer, agentVer, status sql.NullString
	var publicKey, userData, sysUUID, serialNum, manufacturer, model, cpu, timezone, agentIP sql.NullString
	var agentPort sql.NullInt64
	var memory, diskSize sql.NullInt64

	err := s.Scan(
		&d.ID,
		&name, &hostname, &ipAddr, &macAddr,
		&osName, &osVer, &kernelVer, &agentVer,
		&status, &d.LastSeen, &d.CreatedAt,
		&publicKey, &userData, &sysUUID, &serialNum,
		&manufacturer, &model, &cpu, &memory, &diskSize,
		&timezone, &agentPort, &agentIP, &tagsStr,
	)
	if err != nil {
		return nil, err
	}

	// Helper to convert NullString -> string
	sv := func(ns sql.NullString) string {
		if ns.Valid {
			return ns.String
		}
		return ""
	}
	// Helper to convert NullString -> *string
	sp := func(ns sql.NullString) *string {
		if ns.Valid {
			return &ns.String
		}
		return nil
	}
	// Helper to convert NullInt64 -> *int64
	ip64 := func(ni sql.NullInt64) *int64 {
		if ni.Valid {
			return &ni.Int64
		}
		return nil
	}
	// Helper to convert NullInt64 -> *int
	ipi := func(ni sql.NullInt64) *int {
		if ni.Valid {
			v := int(ni.Int64)
			return &v
		}
		return nil
	}

	d.Name = sv(name)
	d.Hostname = sv(hostname)
	d.IPAddress = sv(ipAddr)
	d.MacAddress = sv(macAddr)
	d.OSName = sv(osName)
	d.OSVersion = sv(osVer)
	d.KernelVersion = sv(kernelVer)
	d.AgentVersion = sv(agentVer)
	d.Status = sv(status)
	d.PublicKey = sp(publicKey)
	d.UserData = sp(userData)
	d.SystemUUID = sp(sysUUID)
	d.SerialNumber = sp(serialNum)
	d.Manufacturer = sp(manufacturer)
	d.Model = sp(model)
	d.CPU = sp(cpu)
	d.Memory = ip64(memory)
	d.DiskSize = ip64(diskSize)
	d.Timezone = sp(timezone)
	d.AgentPort = ipi(agentPort)
	d.AgentIP = sp(agentIP)

	if tagsStr != "" {
		d.Tags = strings.Split(tagsStr, ",")
	}
	return d, nil
}

func BoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ReadSecret reads a secret from env var, falling back to Docker secret file.
func ReadSecret(envVar, fileEnvVar string) string {
	// 1. Direct env var
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	// 2. Docker secret file (e.g. JWT_SECRET_FILE=/run/secrets/jwt_secret)
	if filePath := os.Getenv(fileEnvVar); filePath != "" {
		data, err := os.ReadFile(filePath)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
		slog.Warn("could not read secret file", "path", filePath, "error", err)
	}
	// 3. Direct file path convention (e.g. /run/secrets/jwt_secret)
	defaultPath := "/run/secrets/" + strings.ToLower(envVar)
	data, err := os.ReadFile(defaultPath)
	if err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

// FetchSunshinePIN requests the current pairing PIN from the agent.
func FetchSunshinePIN(agentIP string, agentPort int, apiToken string) (string, error) {
	if agentIP == "" {
		agentIP = "localhost"
	}
	if agentPort == 0 {
		agentPort = 47991
	}

	// SSRF guard. agent_ip is supplied by the agent during heartbeat and
	// stored on the device row; without this check a compromised agent
	// could redirect the server's PIN fetch to internal services
	// (Redis on loopback, AWS metadata, etc.) and exfiltrate the
	// response body via the dashboard.
	if err := validateAgentIP(agentIP); err != nil {
		return "", err
	}

	scheme := "http"
	if os.Getenv("AGENT_CA_CERT") != "" {
		scheme = "https"
	}

	url := fmt.Sprintf("%s://%s:%d/agent/sunshine/pin", scheme, agentIP, agentPort)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := agentHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch PIN from agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("agent returned non-OK status: %d", resp.StatusCode)
	}

	var result struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode PIN response: %w", err)
	}

	return result.PIN, nil
}

// SubmitSunshinePIN forwards a Moonlight pairing PIN to the agent's
// /agent/sunshine/pair endpoint. The agent posts to local Sunshine at
// 127.0.0.1:47990 (see packages/agent/sunshine_pair.go).
func SubmitSunshinePIN(agentIP string, agentPort int, apiToken, pin, name string) error {
	if agentIP == "" {
		agentIP = "localhost"
	}
	if agentPort == 0 {
		agentPort = 47991
	}
	if err := validateAgentIP(agentIP); err != nil {
		return err
	}

	scheme := "http"
	if os.Getenv("AGENT_CA_CERT") != "" {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s:%d/agent/sunshine/pair", scheme, agentIP, agentPort)
	bodyJSON, _ := json.Marshal(map[string]string{"pin": pin, "name": name})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := agentHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to forward PIN: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agent returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// GenerateSecureKey creates a random base64-encoded 32-byte key
func GenerateSecureKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		slog.Error("failed to generate secure key", "error", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
