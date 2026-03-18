# vaporRMM - Remote Device Management System

vaporRMM is a modern, distributed Remote Monitoring and Management system built with Go, Next.js, and SQLite.

## Architecture

```
┌─────────────────────┐
│   Dashboard (NextJS)│
│  /dashboard         │
└──────────┬──────────┘
           │ REST/WebSocket API
           │
┌──────────▼──────────┐
│    Server (Go/Fiber)│
│  /packages/server   │
│  - SQLite DB        │
└──────────┬──────────┘
           │
     ┌─────┴─────┐
     ▼           ▼
┌────────┐  ┌──────────┐
│ Agent  │  │   CLI    │
│ (Go)   │  │  (Go)    │
└────────┘  └──────────┘
```

## Project Structure

```
vaporrmm/
├── apps/
│   └── dashboard/         # Next.js 15 Dashboard Application
│       ├── src/
│       │   ├── app/       # App Router pages
│       │   ├── components/# React components
│       │   └── lib/       # Utilities
│       └── package.json
├── packages/
│   ├── server/            # Go/Fiber API Server
│   │   ├── main.go
│   │   ├── models/        # Database models
│   │   ├── handlers/      # HTTP handlers
│   │   ├── middleware/    # Auth, logging
│   │   └── go.mod
│   ├── agent/             # Go Agent for device monitoring
│   │   ├── main.go
│   │   ├── sunshine/      # Sunshine API wrapper
│   │   └── go.mod
│   └── cli/               # CLI tool for agent management
│       ├── main.go
│       └── Dockerfile
└── package.json           # Turborepo root config
```

## Quick Start

### Prerequisites

- Node.js 18+ and npm/yarn/pnpm
- Go 1.21+
- SQLite3

### Installation

```bash
# Install dependencies
pnpm install

# Build all packages
pnpm build

# Run development servers
pnpm dev
```

## Dashboard (apps/dashboard)

A modern Next.js 15 dashboard with:

- Real-time device monitoring via WebSockets
- Device management interface
- Charts and analytics using recharts
- Responsive design with Tailwind CSS
- shadcn/ui components

### Features

- Device list and status monitoring
- Real-time telemetry data
- Command execution on agents
- System health visualization

## Server (packages/server)

Go/Fiber REST API server with:

- SQLite database for lightweight storage
- JWT authentication
- WebSocket support for real-time communication
- RESTful endpoints for all operations

### API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET    | /api/health | Health check |
| POST   | /api/login | User login |
| GET    | /api/devices | List devices |
| POST   | /api/devices | Register device |
| GET    | /api/devices/:id | Device details |
| POST   | /api/devices/:id/command | Execute command |

## Agent (packages/agent)

Go agent that runs on managed devices:

- Connects to the server
- Reports system information
- Executes remote commands
- Monitors device health

### Features

- Automatic reconnection to server
- Cross-platform support (Windows, Linux, macOS)
- Low resource usage

## CLI (packages/cli)

Command-line tool for agent management:

```bash
# Initialize agent configuration
vaporrmm init

# Generate Dockerfile
vaporrmm docker > Dockerfile

# Run the agent
vaporrmm run
```

## Development

### Running Server

```bash
cd packages/server
go run main.go
```

### Running Agent

```bash
cd packages/agent
go run main.go
```

### Running Dashboard

```bash
cd apps/dashboard
npm run dev
```

## Building Docker Images

```bash
# Build agent image
docker build -t vaporrmm-agent packages/cli/

# Run container
docker run -d --name vaporrmm-agent vaporrmm-agent
```

## License

MIT License - See LICENSE file for details.