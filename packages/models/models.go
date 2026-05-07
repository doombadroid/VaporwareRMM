package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Status constants — use these instead of bare strings.
// ---------------------------------------------------------------------------

// DeviceStatus represents the operational state of a managed device.
type DeviceStatus string

const (
	DeviceStatusOnline      DeviceStatus = "online"
	DeviceStatusOffline     DeviceStatus = "offline"
	DeviceStatusMaintenance DeviceStatus = "maintenance"
)

// CommandStatus represents the lifecycle state of a remote command.
type CommandStatus string

const (
	CommandStatusPending   CommandStatus = "pending"
	CommandStatusRunning   CommandStatus = "running"
	CommandStatusCompleted CommandStatus = "completed"
	CommandStatusFailed    CommandStatus = "failed"
)

// PowerActionStatus represents the lifecycle state of a power action.
type PowerActionStatus string

const (
	PowerActionStatusPending  PowerActionStatus = "pending"
	PowerActionStatusExecuted PowerActionStatus = "executed"
	PowerActionStatusFailed   PowerActionStatus = "failed"
)

// FileTransferStatus represents the lifecycle state of a file transfer.
type FileTransferStatus string

const (
	FileTransferStatusPending    FileTransferStatus = "pending"
	FileTransferStatusInProgress FileTransferStatus = "in_progress"
	FileTransferStatusCompleted  FileTransferStatus = "completed"
	FileTransferStatusFailed     FileTransferStatus = "failed"
)

// ---------------------------------------------------------------------------
// Metadata — stored as JSON in the database.
// ---------------------------------------------------------------------------

// Metadata holds optional, device-specific metadata.
type Metadata struct {
	Location   string   `json:"location,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Notes      string   `json:"notes,omitempty"`
	OwnerEmail string   `json:"owner_email,omitempty"`
}

// Value implements driver.Valuer so Metadata can be stored as a JSON column.
func (m Metadata) Value() (driver.Value, error) {
	b, err := json.Marshal(m)
	return string(b), err
}

// Scan implements sql.Scanner so Metadata can be read back from a JSON column.
func (m *Metadata) Scan(src interface{}) error {
	var source string
	switch v := src.(type) {
	case string:
		source = v
	case []byte:
		source = string(v)
	case nil:
		return nil
	default:
		return fmt.Errorf("unsupported Metadata scan type: %T", src)
	}
	return json.Unmarshal([]byte(source), m)
}

// ---------------------------------------------------------------------------
// Core models
// ---------------------------------------------------------------------------

// Device represents a remote device managed by VaporRMM.
type Device struct {
	ID           string       `json:"id" db:"id"`
	Name         string       `json:"name" db:"name"`
	IPAddress    string       `json:"ip_address" db:"ip_address"`
	MacAddress   string       `json:"mac_address" db:"mac_address"`
	Status       DeviceStatus `json:"status" db:"status"`
	LastSeen     *time.Time   `json:"last_seen" db:"last_seen"`
	CreatedAt    time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at" db:"updated_at"`
	AgentVersion string       `json:"agent_version" db:"agent_version"`
	OSName       string       `json:"os_name" db:"os_name"`
	OSVersion    string       `json:"os_version" db:"os_version"`
	Hostname     string       `json:"hostname" db:"hostname"`
	UserID       *string      `json:"user_id,omitempty" db:"user_id"` // Optional, for multi-tenant setups
	Metadata     Metadata     `json:"metadata" db:"metadata"`
}

// AgentStatus represents the status reported by an agent during a heartbeat.
type AgentStatus struct {
	DeviceID    string    `json:"device_id"`
	Status      string    `json:"status"`
	LastSeen    time.Time `json:"last_seen"`
	Uptime      int64     `json:"uptime"` // seconds
	CPUUsage    float64   `json:"cpu_usage"`
	MemoryUsage float64   `json:"memory_usage"`
	DiskUsage   float64   `json:"disk_usage"`
}

// Command represents a remote command to be executed on an agent.
type Command struct {
	ID          string        `json:"id" db:"id"`
	DeviceID    string        `json:"device_id" db:"device_id"`
	Type        string        `json:"type"`                          // shell, script, etc.
	Status      CommandStatus `json:"status" db:"status"`
	Command     string        `json:"command"`
	Result      *string       `json:"result,omitempty"`
	Error       *string       `json:"error,omitempty"`
	CreatedAt   time.Time     `json:"created_at" db:"created_at"`
	CompletedAt *time.Time    `json:"completed_at,omitempty" db:"completed_at"`
}

// PowerAction represents a power management command.
type PowerAction struct {
	ID          string            `json:"id" db:"id"`
	DeviceID    string            `json:"device_id" db:"device_id"`
	Action      string            `json:"action"` // shutdown, reboot, sleep, wake
	Status      PowerActionStatus `json:"status" db:"status"`
	CreatedAt   time.Time         `json:"created_at" db:"created_at"`
	CompletedAt *time.Time        `json:"completed_at,omitempty" db:"completed_at"`
}

// FileTransfer represents a file transfer operation.
type FileTransfer struct {
	ID          string             `json:"id" db:"id"`
	DeviceID    string             `json:"device_id" db:"device_id"`
	Type        string             `json:"type"` // upload, download
	FileName    string             `json:"file_name"`
	FilePath    string             `json:"file_path"`
	Status      FileTransferStatus `json:"status" db:"status"`
	Progress    int                `json:"progress"` // 0–100
	CreatedAt   time.Time          `json:"created_at" db:"created_at"`
	CompletedAt *time.Time         `json:"completed_at,omitempty" db:"completed_at"`
}

// ---------------------------------------------------------------------------
// Input / request models
// ---------------------------------------------------------------------------

// NewDeviceInput represents data for creating a new device.
type NewDeviceInput struct {
	Name       string   `json:"name"`
	IPAddress  string   `json:"ip_address"`
	MacAddress string   `json:"mac_address"`
	Hostname   string   `json:"hostname"`
	OSName     string   `json:"os_name"`
	OSVersion  string   `json:"os_version"`
	UserID     *string  `json:"user_id,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

// UpdateDeviceInput represents data for updating a device.
type UpdateDeviceInput struct {
	Name     *string   `json:"name,omitempty"`
	Hostname *string   `json:"hostname,omitempty"`
	Status   *string   `json:"status,omitempty"`
	Metadata *Metadata `json:"metadata,omitempty"`
	Tags     *[]string `json:"tags,omitempty"`
}

// LoginRequest represents a login request.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse represents a login response with a JWT token.
type LoginResponse struct {
	Token  string `json:"token"`
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name,omitempty"`
}

// ErrorResponse represents an API error response.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// ServerDevice represents a device record for JSON serialization.
type ServerDevice struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Hostname      string           `json:"hostname"`
	IPAddress     string           `json:"ip_address"`
	MacAddress    string           `json:"mac_address"`
	OSName        string           `json:"os_name"`
	OSVersion     string           `json:"os_version"`
	KernelVersion string           `json:"kernel_version"`
	AgentVersion  string           `json:"agent_version"`
	Status        string           `json:"status"`
	LastSeen      int64            `json:"last_seen"`
	CreatedAt     int64            `json:"created_at"`
	PublicKey     *string          `json:"public_key,omitempty"`
	UserData      *string          `json:"user_data,omitempty"`
	SystemUUID    *string          `json:"system_uuid,omitempty"`
	SerialNumber  *string          `json:"serial_number,omitempty"`
	Manufacturer  *string          `json:"manufacturer,omitempty"`
	Model         *string          `json:"model,omitempty"`
	CPU           *string          `json:"cpu,omitempty"`
	Memory        *int64           `json:"memory,omitempty"`
	DiskSize      *int64           `json:"disk_size,omitempty"`
	Timezone      *string          `json:"timezone,omitempty"`
	AgentPort     *int             `json:"agent_port,omitempty"`
	AgentIP       *string          `json:"agent_ip,omitempty"`
	Tags          []string         `json:"tags,omitempty"`
	Sunshine      *SunshineStatus  `json:"sunshine,omitempty"`
	Tailscale     *TailscaleStatus `json:"tailscale,omitempty"`
}

// SunshineStatus represents the status of Sunshine on a device.
type SunshineStatus struct {
	Installed bool `json:"installed"`
	Running   bool `json:"running"`
	Port      int  `json:"port"`
}

// TailscaleStatus represents the status of Tailscale on a device.
type TailscaleStatus struct {
	Installed    bool   `json:"installed"`
	Connected    bool   `json:"connected"`
	IP           string `json:"ip,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	Peers        int    `json:"peers,omitempty"`
	BackendState string `json:"backend_state,omitempty"`
}

// BrandingConfig holds the white-label branding configuration.
type BrandingConfig struct {
	AppName      string `json:"app_name"`
	IconURL      string `json:"icon_url"`
	CompanyName  string `json:"company_name"`
	PrimaryColor string `json:"primary_color"`
}

// StatusResponse for health checks.
type StatusResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// CommandRequest represents a command to send to an agent.
type CommandRequest struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Payload   map[string]interface{} `json:"payload"`
	CreatedAt interface{}            `json:"created_at"`
}

// CommandResult represents the result of a command execution.
type CommandResult struct {
	CommandID string      `json:"command_id"`
	Success   bool        `json:"success"`
	Output    string      `json:"output,omitempty"`
	Error     string      `json:"error,omitempty"`
	Timestamp interface{} `json:"timestamp"`
}

// AgentToken stores registered agent tokens with their device IDs.
type AgentToken struct {
	TokenHash string
	DeviceID  string
	Hostname  string
	ExpiresAt int64 // Unix timestamp; 0 means no expiration
}

// Migration represents a single database migration.
type Migration struct {
	Version string
	Name    string
	SQL     string
}
