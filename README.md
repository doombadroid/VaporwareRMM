# VaporRMM - Remote Monitoring and Management Platform

A comprehensive, open-source RMM platform built with Go and Next.js, designed for managing remote systems with real-time monitoring, automated agent deployment, and powerful CLI tools.

## Architecture Overview

```
┌─────────────────┐     ┌──────────────────┐     ┌──────────────────────┐
│  Dashboard UI   │────▶│   Vapor Server   │◀────┤    Agent (Go)        │
│  (Next.js 15)   │     │  (Fiber/Golang)  │     │   - Sunshine API     │
└─────────────────┘     └──────────────────┘     │   - System Metrics   │
                                                  │   - Task Execution   │
                                                  └──────────────────────┘
                                                           ▲
                                                           │
                                                    ┌────────────┐
                                                    │  Vapor CLI │
                                                    │ (Go Tool)  │
                                                    └────────────┘
```

## Features

### Dashboard (`apps/dashboard`)
- Real-time system monitoring with WebSocket connections
- System health dashboards with customizable widgets
- Remote terminal access via SSH/WebSockets
- Automated task scheduling and execution
- Alert management and notification center
- Multi-system support with group organization
- Dark mode support

### Server (`packages/server`)
- RESTful API server using Fiber framework
- SQLite database for lightweight deployment
- WebSocket support for real-time notifications
- Agent management and communication
- Task scheduling engine
- Prometheus-compatible metrics endpoint

### Agent (`packages/agent`)
- Lightweight Go-based system agent
- Wraps Sunshine API (localhost:47990) for system control
- Real-time metrics collection:
  - CPU, memory, disk usage
  - Network statistics
  - Process monitoring
  - Service status tracking
- Secure communication with server
- Automated task execution

### CLI (`packages/cli`)
- Docker container management
- System diagnostics and health checks
- Agent installation and configuration
- Bulk operations support
- Scriptable automation tool

## Prerequisites

- Go 1.23+ (for server, agent, CLI)
- Node.js 20+ (for dashboard)
- npm or yarn
- Docker & Docker Compose (optional)

## Quick Start

### 1. Clone the Repository

```bash
git clone https://github.com/your-org/vaporrmm.git
cd vaporrmm
```

### 2. Build and Run with Docker

```bash
# Build all services
docker-compose build

# Start all services
docker-compose up -d
```

### 3. Local Development

#### Server
```bash
cd packages/server
go mod download
go run main.go
```
Server runs on `http://localhost:8080`

#### Agent
```bash
cd packages/agent
go mod download
go run main.go
```
Agent connects to server at `ws://localhost:8080/ws`

#### Dashboard
```bash
cd apps/dashboard
npm install
npm run dev
```
Dashboard runs on `http://localhost:3000`

## Project Structure

```
vaporrmm/
├── apps/
│   └── dashboard/              # Next.js 15 Dashboard
│       ├── src/
│       │   ├── app/           # App Router pages
│       │   ├── components/    # Reusable components
│       │   ├── lib/          # Utility functions
│       │   └── types.ts      # TypeScript definitions
│       ├── public/           # Static assets
│       └── package.json
├── packages/
│   ├── server/               # Go Fiber API Server
│   │   ├── internal/
│   │   │   ├── api/         # API handlers
│   │   │   ├── db/          # Database models
│   │   │   └── ws/          # WebSocket handlers
│   │   ├── main.go
│   │   └── go.mod
│   │
│   ├── agent/                # Go System Agent
│   │   ├── internal/
│   │   │   ├── metrics/     # Metrics collection
│   │   │   ├── sunshine/    # Sunshine API wrapper
│   │   │   └── tasks/       # Task execution
│   │   ├── main.go
│   │   └── go.mod
│   │
│   └── cli/                  # Go CLI Tool
│       ├── cmd/             # CLI commands
│       │   ├── install.go
│       │   ├── run.go
│       │   └── status.go
│       ├── main.go
│       └── go.mod
├── docker-compose.yml
├── Dockerfile               # Dashboard container
└── README.md
```

## API Documentation

The server exposes a RESTful API with the following endpoints:

### Agents
- `GET /api/agents` - List all registered agents
- `POST /api/agents/register` - Register new agent
- `GET /api/agents/:id` - Get agent details
- `DELETE /api/agents/:id` - Remove agent

### Metrics
- `GET /api/metrics/agent/:id` - Get agent metrics history
- `GET /api/metrics/latest/:id` - Get latest agent metrics

### Tasks
- `GET /api/tasks` - List all tasks
- `POST /api/tasks` - Create new task
- `POST /api/tasks/run/:id` - Run task immediately
- `DELETE /api/tasks/:id` - Remove task

### WebSocket
- `ws://localhost:8080/ws` - Real-time updates

## Configuration

### Server Environment Variables
```bash
VAPOR_PORT=8080          # API server port
VAPOR_WS_PORT=8081       # WebSocket port
VAPOR_DB_PATH=/data/db   # SQLite database path
VAPOR_SECRET_KEY=your-secret-key
```

### Agent Configuration
```bash
VAPOR_SERVER_URL=ws://localhost:8080/ws
VAPOR_AGENT_ID=auto-generated
VAPOR_METRICS_INTERVAL=30s
```

## Building from Source

### Server Binary
```bash
cd packages/server
go build -o vapor-server main.go
./vapor-server
```

### Agent Binary
```bash
cd packages/agent
go build -o vapor-agent main.go
./vapor-agent
```

### CLI Binary
```bash
cd packages/cli
go build -o vaporcli main.go
./vaporcli --help
```

## Docker Images

### Build Dashboard
```bash
docker build -t vaporrmm/dashboard -f apps/dashboard/Dockerfile .
```

### Build Server
```bash
docker build -t vaporrmm/server -f packages/server/Dockerfile .
```

### Build Agent
```bash
docker build -t vaporrmm/agent -f packages/agent/Dockerfile .
```

### Build CLI
```bash
docker build -t vaporrmm/cli -f packages/cli/Dockerfile .
```

## Development

### Running Tests
```bash
# Server tests
cd packages/server
go test ./...

# Agent tests  
cd packages/agent
go test ./...
```

### Code Quality
```bash
# Linting
golangci-lint run ./packages/server/...
golangci-lint run ./packages/agent/...
golangci-lint run ./packages/cli/...

# Formatting
go fmt ./packages/server/...
go fmt ./packages/agent/...
go fmt ./packages/cli/...
```

## Deployment

### Production Docker Setup
```bash
docker-compose -f docker-compose.prod.yml up -d
```

### Kubernetes
See `kubernetes/` directory for K8s manifests (coming soon).

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the MIT License.

## Acknowledgments

- Built with [Fiber](https://gofiber.io/) for Go web server
- Dashboard uses [Next.js 15](https://nextjs.org/)
- Agent architecture inspired by popular RMM solutions

---

**Note:** This is a work-in-progress project. Some features may be incomplete or subject to change.