package handlers

type Config struct {
	BuildVersion            string
	DefaultOfflineThreshold int
	DefaultAgentWSPort      int
	DefaultSunshinePort     int
	DefaultCookieMaxAge     int
	MaxDevicesLimit         int
	MaxCommandLimit         int
	MaxAuditLimit           int
	MoonlightWebURL         string
	SunshineVersion         string
}

const agentHeartbeatRateLimit = 5 // per minute
