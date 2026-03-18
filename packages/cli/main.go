package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// Config holds the CLI configuration
type Config struct {
	ServerURL string `json:"server_url"`
	APIKey    string `json:"api_key,omitempty"`
	Timeout   int    `json:"timeout"`
}

var (
	verbose     bool
	configPath  = filepath.Join(homeDir(), ".vaporrmm", "config.json")
	defaultConf = Config{
		ServerURL: "http://localhost:8080",
		Timeout:   30,
	}
)

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // Windows
}

var rootCmd = &cobra.Command{
	Use:   "vaporrmm",
	Short: "Vapor RMM - Remote Monitoring and Management CLI",
	Long: `Vapor RMM CLI is a command-line tool for managing remote machines.

Usage:
  vaporrmm [command]

Available Commands:
  host      Manage monitored hosts
  config    Configuration management
  server    Server connection management
  help      Help about any command

Flags:
  -h, --help     Help for vaporrmm
  -v, --verbose  Enable verbose output

Use "vaporrmm [command] --help" for more information about a command.`,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.AddCommand(hostCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(versionCmd)

	hostCmd.AddCommand(hostListCmd)
	hostCmd.AddCommand(hostAddCmd)
	hostCmd.AddCommand(hostRemoveCmd)
	hostCmd.AddCommand(hostShowCmd)

	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configInitCmd)

	// Initialize config file if it doesn't exist
	initConfigFile()
}

func verboseLog(format string, args ...interface{}) {
	if verbose {
		fmt.Printf("[INFO] "+format+"\n", args...)
	}
}

func initConfigFile() error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		data, _ := json.MarshalIndent(defaultConf, "", "  ")
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return fmt.Errorf("failed to create config file: %w", err)
		}
		verboseLog("Created default config at %s", configPath)
	}
	return nil
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var conf Config
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return &conf, nil
}

func saveConfig(conf *Config) error {
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	verboseLog("Config saved to %s", configPath)
	return nil
}

// ========== Version Command ==========
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Vapor RMM CLI")
		fmt.Println("Version: 0.1.0")
		fmt.Println("Build: development")
	},
}

// ========== Host Commands ==========
var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Manage monitored hosts",
	Long: `Manage and interact with monitored remote hosts.

Commands:
  list    List all registered hosts
  add     Add a new host to monitor
  remove  Remove a host from monitoring
  show    Show detailed information about a host`,
}

var hostListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered hosts",
	Long:  `Retrieve and display a list of all monitored hosts.`,
	RunE: func(cmd *cobra.Command, args []string) {
		conf, err := loadConfig()
		if err != nil {
			return err
		}

		fmt.Printf("Fetching hosts from %s...\n", conf.ServerURL)
		verboseLog("Server URL: %s", conf.ServerURL)

		// TODO: Make API call to fetch hosts
		fmt.Println("Host List (API integration pending):")
		fmt.Println("  ID       | Name              | Status    | Last Seen")
		fmt.Println("  ---------|-------------------|-----------|------------------")

		return nil
	},
}

var hostAddCmd = &cobra.Command{
	Use:   "add [hostname] [ip_address]",
	Short: "Add a new host to monitor",
	Long:  `Register a new remote machine for monitoring.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) {
		hostname := args[0]
		ipAddress := args[1]

		conf, err := loadConfig()
		if err != nil {
			return err
		}

		fmt.Printf("Adding host '%s' (%s) to monitoring...\n", hostname, ipAddress)
		verboseLog("Server URL: %s", conf.ServerURL)

		// TODO: Make API call to add host
		fmt.Println("Host registration pending (API integration required)")

		return nil
	},
}

var hostRemoveCmd = &cobra.Command{
	Use:   "remove [hostname]",
	Short: "Remove a host from monitoring",
	Long:  `Stop monitoring and remove a host from the system.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) {
		hostname := args[0]

		conf, err := loadConfig()
		if err != nil {
			return err
		}

		fmt.Printf("Removing host '%s' from monitoring...\n", hostname)
		verboseLog("Server URL: %s", conf.ServerURL)

		// TODO: Make API call to remove host
		fmt.Println("Host removal pending (API integration required)")

		return nil
	},
}

var hostShowCmd = &cobra.Command{
	Use:   "show [hostname]",
	Short: "Show detailed information about a host",
	Long:  `Retrieve comprehensive details about a specific monitored host.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) {
		hostname := args[0]

		conf, err := loadConfig()
		if err != nil {
			return err
		}

		fmt.Printf("Fetching details for host '%s'...\n", hostname)
		verboseLog("Server URL: %s", conf.ServerURL)

		// TODO: Make API call to fetch host details
		fmt.Println("Host Details (API integration pending):")
		fmt.Printf("  Name:      %s\n", hostname)
		fmt.Printf("  Status:    offline\n")
		fmt.Printf("  Uptime:    -\n")
		fmt.Printf("  Load:      -\n")

		return nil
	},
}

// ========== Config Commands ==========
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
	Long: `Manage Vapor RMM configuration.

Commands:
  show    Show current configuration
  set     Set a configuration value
  init    Initialize new configuration`,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) {
		conf, err := loadConfig()
		if err != nil {
			return err
		}

		fmt.Println("Current Configuration:")
		data, _ := json.MarshalIndent(conf, "", "  ")
		fmt.Println(string(data))
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set [key] [value]",
	Short: "Set a configuration value",
	Long:  `Update a specific configuration parameter.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) {
		key := args[0]
		value := args[1]

		conf, err := loadConfig()
		if err != nil {
			return err
		}

		switch key {
		case "server_url":
			conf.ServerURL = value
		case "timeout":
			var timeout int
			fmt.Sscanf(value, "%d", &timeout)
			conf.Timeout = timeout
		default:
			return fmt.Errorf("unknown configuration key: %s", key)
		}

		if err := saveConfig(conf); err != nil {
			return err
		}
		fmt.Printf("Configuration updated: %s=%s\n", key, value)
		return nil
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize new configuration",
	Long:  `Create a new configuration file with default values.`,
	RunE: func(cmd *cobra.Command, args []string) {
		if err := initConfigFile(); err != nil {
			return err
		}
		fmt.Println("Configuration initialized successfully")
		fmt.Printf("Config location: %s\n", configPath)
		return nil
	},
}

// ========== Server Commands ==========
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Server connection management",
	Long:  `Test and manage server connectivity.`,
}

var serverPingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Test server connectivity",
	RunE: func(cmd *cobra.Command, args []string) {
		conf, err := loadConfig()
		if err != nil {
			return err
		}

		fmt.Printf("Pinging %s...\n", conf.ServerURL)
		// TODO: Implement actual ping/test connection

		fmt.Println("Connection test pending (API integration required)")
		return nil
	},
}
