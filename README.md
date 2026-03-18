# VapoRMM - Remote Monitoring and Management Platform

A comprehensive RMM platform built with a monorepo architecture featuring:

- **Dashboard**: Next.js 15 web interface for device management
- **Server**: Go/Fiber REST API server
- **Agent**: Go agent that wraps Sunshine API
- **CLI**: Installation and management CLI tool

## Project Structure

```
vaporrmm/
├── apps/
│   └── dashboard/          # Next.js 15 Application Router app
├── packages/
│   ├── server/            # Go/Fiber REST API server
│   ├── agent/             # Go Sunshine API wrapper agent
│   └── cli/               # Go CLI for installation
├── package.json           # Root pnpm workspace config
├── turbo.json             # Turborepo configuration
└── pnpm-workspace.yaml    # Workspace setup
```

## Getting Started

### Prerequisites

- [Go 1.21+](https://golang.org/doc/install)
- [Node.js 18+](https://nodejs.org/)
- [pnpm](https://pnpm.io/)
- [Turborepo](https://turbo.build/repo)

### Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/vaporrmm.git
cd vaporrmm

# Install dependencies
pnpm install

# Build all packages
pnpm build

# Start development servers
pnpm dev:dashboard  # Next.js dashboard on :3000
pnpm dev:server     # Go API server on :8080
```

### Building Go Packages

```bash
# Build the server
cd packages/server && go build -o vaporrmm-server main.go

# Build the agent
cd packages/agent && go build -o vaporrmm-agent main.go

# Build the CLI
cd packages/cli && go build -o vaporrmm-cli main.go
```

## Components

### Dashboard (`apps/dashboard`)

Next.js 15 application with:
- App Router architecture
- TypeScript for type safety
- Tailwind CSS for styling
- TanStack Query for data fetching
- WebSocket support for real-time updates
- shadcn/ui components

### Server (`packages/server`)

Go/Fiber REST API server with:
- SQLite database for persistence
- RESTful endpoints for device management
- Agent registration and heartbeat handling
- Device configuration management

### Agent (`packages/agent`)

Go agent that wraps Sunshine API (localhost:47990):
- System information collection
- Remote command execution
- File operations
- Process management
- Network status monitoring

### CLI (`packages/cli`)

Go CLI tool for:
- Installation and uninstallation
- Service management
- Configuration updates
- Health checks

## Running Tests

```bash
# Run Go tests
go test ./...

# Run dashboard tests
pnpm test
```

## Docker

```bash
# Build the application
docker build -t vaporrmm .

# Run with Docker Compose (when available)
docker-compose up -d
```

## License

MIT License - see LICENSE file for details.