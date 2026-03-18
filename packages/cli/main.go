package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Config struct {
	ServerURL string
	AgentID   string
	Name      string
}

func printUsage() {
	fmt.Println(" vaporRMM CLI - Remote Device Management System")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  vaporrmm [command] [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  init       Initialize a new vaporRMM agent configuration")
	fmt.Println("  docker     Generate Dockerfile for agent deployment")
	fmt.Println("  run        Start the vaporRMM agent")
	fmt.Println("  help       Show this help message")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  vaporrmm init --server http://localhost:3001")
	fmt.Println("  vaporrmm docker > Dockerfile")
	fmt.Println("  vaporrmm run")
}

func initConfig() error {
	reader := bufio.NewReader(os.Stdin)
	
	config := Config{}
	
	fmt.Print("Server URL (default: http://localhost:3001): ")
	serverURL, _ := reader.ReadString('\n')
	serverURL = strings.TrimSpace(serverURL)
	if serverURL == "" {
		serverURL = "http://localhost:3001"
	}
	config.ServerURL = serverURL
	
	fmt.Print("Agent ID (optional): ")
	agentID, _ := reader.ReadString('\n')
	config.AgentID = strings.TrimSpace(agentID)
	
	fmt.Print("Device name (optional): ")
	name, _ := reader.ReadString('\n')
	config.Name = strings.TrimSpace(name)
	
	// Create vaporrmm directory
	vaporDir := filepath.Join(os.Getenv("HOME"), ".vaporrmm")
	if err := os.MkdirAll(vaporDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	
	// Write config file
	configPath := filepath.Join(vaporDir, "config.json")
	configData := fmt.Sprintf(`{
  "server_url": "%s",
  "client_id": "%s",
  "name": "%s"
}`, config.ServerURL, config.AgentID, config.Name)
	
	if err := os.WriteFile(configPath, []byte(configData), 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	
	fmt.Printf("✓ Configuration saved to %s\n", configPath)
	return nil
}

func generateDockerfile() error {
	dockerfile := `# vaporRMM Agent Dockerfile
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Install dependencies
RUN apk add --no-cache git

# Copy go.mod and go.sum
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the agent
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o vaporrmm-agent .

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binary
COPY --from=builder /build/vaporrmm-agent .

# Create non-root user
RUN adduser -D -g '' appuser && \
    chown -R appuser:appuser /root
USER appuser

EXPOSE 47990

CMD ["./vaporrmm-agent"]
`
	fmt.Print(dockerfile)
	return nil
}

func runAgent() error {
	// Check if agent binary exists
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	
	cmd := exec.Command(executable)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	return cmd.Run()
}

func main() {
	args := os.Args[1:]
	
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage()
		return
	}
	
	switch args[0] {
	case "init":
		if err := initConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		
	case "docker":
		if err := generateDockerfile(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		
	case "run":
		if err := runAgent(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0])
		printUsage()
		os.Exit(1)
	}
}