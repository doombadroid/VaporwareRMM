package models

// AgentToken stores registered agent tokens with their device IDs.
type AgentToken struct {
	TokenHash string // SHA-256 hash of the bearer token
	DeviceID  string
	Hostname  string
	TenantID  string
	ExpiresAt int64 // Unix timestamp; 0 means no expiration
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

// StatusResponse for health checks.
type StatusResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// LoginRequest represents a login request.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse represents a login response.
type LoginResponse struct {
	Token  string `json:"token"`
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name,omitempty"`
}

// CommandRequest represents a command to send to an agent.
type CommandRequest struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Payload   map[string]interface{} `json:"payload"`
	CreatedAt interface{}            `json:"created_at"` // time.Time or int64
}

// CommandResult represents the result of a command execution.
type CommandResult struct {
	CommandID string      `json:"command_id"`
	Success   bool        `json:"success"`
	Output    string      `json:"output,omitempty"`
	Error     string      `json:"error,omitempty"`
	Timestamp interface{} `json:"timestamp"` // time.Time or int64
}

// Migration represents a single database migration.
type Migration struct {
	Version string
	Name    string
	SQL     string
}

// ResponseWriter adapts bytes.Buffer to http.ResponseWriter for promhttp.
type ResponseWriter struct {
	Body   interface{}
	Header interface{}
	Code   int
}
