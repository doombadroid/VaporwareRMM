# Vapor RMM - Remote Monitoring & Management System

A full-stack monorepo for a remote monitoring and management system designed to work with Sunshine (VNC/remote desktop server). Built with Next.js 15, Go, and SQLite.

## Features

- **Dashboard**: Modern web interface built with Next.js 15 App Router
- **Server**: FastAPI-compatible REST API with Go Fiber
- **Agent**: Lightweight Go agent that wraps Sunshine API (localhost:47990)
- **CLI**: Command-line tool for installing and managing the agent

## Architecture

```
vaporrmm/
├── apps/
│   └── dashboard/          # Next.js 15 application
├── packages/
│   ├── server/            # Go Fiber REST API server
│   ├── agent/             # Go Sunshine wrapper agent
│   └── cli/               # Go CLI installation tool
├── packages.json          # Monorepo root
└── turbo.json             # Turborepo configuration
```

## Prerequisites

- Node.js 18+ and npm/pnpm
- Go 1.21+
- Docker (optional, for containerized deployment)
- Sunshine server running on localhost:47990

## Quick Start

### 1. Install Dependencies

```bash
pnpm install
```

### 2. Start the Dashboard (Development)

```bash
cd apps/dashboard
pnpm dev
```

The dashboard will be available at `http://localhost:3000`.

### 3. Build and Run the Server

```bash
cd packages/server
go build -o vaporrmm-server main.go
./vaporrmm-server
```

Server runs on port `8080` by default.

### 4. Install and Run the Agent

```bash
# Build the agent
cd packages/agent
go build -o vaporrmm-agent main.go

# Run the agent
./vaporrmm-agent
```

The agent connects to Sunshine at `http://localhost:47990`.

### 5. Use the CLI

```bash
# Install the agent as a service
cd packages/cli
go build -o vaporrmm-cli main.go
sudo ./vaporrmm-cli install

# Check status
./vaporrmm-cli status

# Stop the agent
./vaporrmm-cli stop
```

## API Documentation

### Server Endpoints

- `GET /api/health` - Health check endpoint
- `GET /api/sessions` - List active Sunshine sessions
- `POST /api/sessions/{id}/start` - Start a session
- `POST /api/sessions/{id}/stop` - Stop a session
- `GET /api/devices` - List registered devices

### Agent Endpoints

The agent exposes the same Sunshine API at:
- `http://localhost:47991/api/sessions`
- `http://localhost:47991/app`

## Configuration

### Server Config (`packages/server/config.json`)

```json
{
  "port": 8080,
  "database": {
    "path": "/var/lib/vaporrmm/db.sqlite"
  },
  "agent": {
    "endpoint": "http://localhost:47990",
    "interval": 30
  }
}
```

### Agent Config (`packages/agent/config.json`)

```json
{
  "serverUrl": "http://localhost:8080",
  "sunshineUrl": "http://localhost:47990",
  "refreshInterval": 30,
  "logLevel": "info"
}
```

## Docker Deployment

### Build Images

```bash
# Build all images
docker-compose build

# Or build individually
cd packages/server && docker build -t vaporrmm-server .
cd packages/agent && docker build -t vaporrmm-agent .
cd packages/cli && docker build -t vaporrmm-cli .
```

### Run with Docker Compose

```bash
docker-compose up -d
```

## Development

### Running in Monorepo Mode

```bash
# Install turbo globally
npm install -g turbo

# Run dashboard and server together
turbo run dev
```

### Code Structure

#### apps/dashboard
- `app/` - Next.js App Router pages
- `components/` - React components
- `lib/api.ts` - API client utilities
- `styles/globals.css` - Global styles with Tailwind

#### packages/server
- `models/` - Database models
- `handlers/` - HTTP handlers
- `db/` - SQLite database setup

#### packages/agent
- `sunshine/` - Sunshine API wrappers
- `client/` - Server communication client
- `monitor/` - Session monitoring logic

## License

MIT License

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'feat: add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Support

For support, please join our Discord server or open an issue on GitHub.