# vaporRMM Docker Deployment

Complete Docker-based deployment for vaporRMM with **automated testing agents** and **Tailscale support**.

## Quick Start

```bash
cd /path/to/vaporRMM/docker

# 1. Create .env (interactive)
cp .env.example .env
nano .env  # Edit JWT_SECRET and ADMIN_PASSWORD

# 2. Start everything (server + dashboard + 2 test agents)
sudo ./setup-docker.sh

# 3. Run tests
./test.sh
```

That's it. You now have:
- Server on http://localhost:8080
- Dashboard on http://localhost:3000
- 2 test agents auto-registering (Ubuntu + Alpine)
- Optional Tailscale integration ready

## Architecture

```
┌─────────────────────────────────────────────┐
│           Docker Host                        │
│                                              │
│  ┌──────────────┐  ┌──────────────────┐    │
│  │  Dashboard   │  │  Server          │    │
│  │  :3000       │  │  :8080           │    │
│  │  (Next.js)   │  │  (Go + SQLite)   │    │
│  └──────────────┘  └────────┬─────────┘    │
│                             │              │
│         ┌───────────────────┼───────────┐  │
│         │                   │           │  │
│  ┌──────┴──────┐  ┌────────┴────────┐  │  │
│  │ Agent-Ubuntu│  │ Agent-Alpine    │  │  │
│  │ (debian)    │  │ (alpine)        │  │  │
│  └─────────────┘  └─────────────────┘  │  │
│                                        │  │
│  ┌──────────────────────────────────┐  │  │
│  │ Tailscale Node (optional)        │  │  │
│  │ Separate tailnet member          │  │  │
│  └──────────────────────────────────┘  │  │
│                                        │  │
│     [ Remote machines via Tailscale ]  │  │
└────────────────────────────────────────┘  │
```

## Services

| Service | Container | Purpose |
|---------|-----------|---------|
| **server** | `vaporrmm-server` | Go API server + SQLite + Tailscale CLI |
| **dashboard** | `vaporrmm-dashboard` | Next.js frontend |
| **agent-ubuntu** | `vaporrmm-agent-ubuntu` | Test agent on Debian-like system |
| **agent-alpine** | `vaporrmm-agent-alpine` | Test agent on Alpine Linux |
| **tailscale-node** | `vaporrmm-tailscale-node` | Separate Tailscale node for testing |

## Files

| File | Purpose |
|------|---------|
| `docker-compose.full.yml` | Full stack: server + dashboard + test agents + tailscale |
| `docker-compose.yml` | Minimal: server + dashboard only |
| `Dockerfile.server` | Server image with Tailscale CLI installed |
| `dashboard-files/Dockerfile` | Dashboard image |
| `entrypoint.sh` | Starts tailscaled (if auth key provided) then server |
| `.env.example` | Configuration template |
| `setup-docker.sh` | One-command setup + test runner |
| `test.sh` | Automated test suite (12 tests) |

## Configuration

Edit `.env`:

```bash
# Required
JWT_SECRET=your-random-secret-here
ADMIN_PASSWORD=YourStrongPassword123!

# Optional: Tailscale
TAILSCALE_AUTH_KEY=tskey-auth-k1234567cnTRL-...

# Optional: CORS (add your domains)
CORS_ORIGINS=http://localhost:3000,http://127.0.0.1:3000
```

### Getting a Tailscale Auth Key

1. Go to https://login.tailscale.com/admin/settings/keys
2. Click **Generate auth key...**
3. Set: Reusable = true, Ephemeral = false
4. Copy the key starting with `tskey-auth-`
5. Add to `.env`: `TAILSCALE_AUTH_KEY=tskey-auth-...`
6. Restart: `docker compose -f docker-compose.full.yml up -d`

## Commands

### Start
```bash
docker compose -f docker-compose.full.yml up -d
```

### View Logs
```bash
# All services
docker compose -f docker-compose.full.yml logs -f

# Just server
docker compose -f docker-compose.full.yml logs -f server

# Just agents
docker compose -f docker-compose.full.yml logs -f agent-ubuntu agent-alpine
```

### Stop
```bash
docker compose -f docker-compose.full.yml down
```

### Restart
```bash
docker compose -f docker-compose.full.yml restart
```

### Shell into containers
```bash
# Server
docker compose -f docker-compose.full.yml exec server bash

# Check Tailscale status from server
docker compose -f docker-compose.full.yml exec server tailscale status

# Agent
docker compose -f docker-compose.full.yml exec agent-ubuntu bash
```

## Testing

### Automated Tests
```bash
./test.sh
```

Tests 12 things:
1. Server health endpoint
2. Version endpoint
3. Public branding (no auth)
4. Install links (no auth)
5. Agent install script download
6. Agent binary download
7. Dashboard loads
8. Login API works
9. Authenticated device list
10. Dashboard overview API
11. Test agents registered
12. Tailscale connected (if configured)

### Manual Testing Checklist

**Dashboard**
- [ ] Open http://localhost:3000
- [ ] Login with `admin@vaporrmm.local`
- [ ] See 2 test agents in device fleet
- [ ] Click agent → view details
- [ ] Click agent → Remote Control (Sunshine)
- [ ] Click agent → Tailscale status

**Agent Install**
- [ ] From host machine: `curl -fsSL http://localhost:8080/api/branding/agent-install?format=script | bash -n`
- [ ] Verify script has correct server URL

**Tailscale (if configured)**
- [ ] Dashboard → Settings → Tailscale tab
- [ ] Generate auth key
- [ ] Install Tailscale on a test agent
- [ ] Verify agent shows Tailscale IP in dashboard

**API**
- [ ] `curl http://localhost:8080/health`
- [ ] `curl http://localhost:8080/api/version`
- [ ] `curl http://localhost:8080/api/branding/install-links`

## Installing Agent on Remote Machines

```bash
curl -fsSL http://YOUR_SERVER_IP:8080/api/branding/agent-install?format=script | sudo bash -s -- --server http://YOUR_SERVER_IP:8080
```

## Data Persistence

| Data | Location |
|------|----------|
| SQLite DB | `./data/vapor_rmm.db` |
| Tailscale state | Docker volume `tailscale-state` |
| Server backups | `./backups/` |

## Updating

```bash
cd /path/to/vaporRMM

# Rebuild binaries
cd packages/server && go build -o ../../docker/vaporrmm-server ./main.go
cd ../agent && go build -o ../../docker/vaporrmm-agent ./main.go

# Rebuild dashboard
cd ../../apps/dashboard && npm run build
cd ../../docker
cp -r ../apps/dashboard/.next ../apps/dashboard/package.json dashboard-files/
cp -r ../apps/dashboard/node_modules dashboard-files/

# Restart
docker compose -f docker-compose.full.yml down
docker compose -f docker-compose.full.yml up -d --build
```

## Troubleshooting

### Port already in use
```bash
sudo lsof -i :8080
sudo lsof -i :3000
# Or change ports in docker-compose.full.yml
```

### Agents not registering
```bash
# Check agent logs
docker compose -f docker-compose.full.yml logs -f agent-ubuntu

# Check server rate limits
docker compose -f docker-compose.full.yml logs -f server | grep "429\|agent"
```

### Tailscale not connecting
```bash
# Check tailscale status
docker compose -f docker-compose.full.yml exec server tailscale status

# Re-authenticate
docker compose -f docker-compose.full.yml exec server tailscale up --force-reauth
```

### Dashboard can't reach API
```bash
# Check CORS settings in .env
docker compose -f docker-compose.full.yml exec dashboard wget -qO- http://server:8080/health
```

## PostgreSQL (Optional)

Uncomment the `postgres` service in `docker-compose.full.yml` and update `.env`:

```env
DATABASE_URL=postgres://vaporrmm:vaporrmm@postgres:5432/vaporrmm?sslmode=disable
```

Then restart:
```bash
docker compose -f docker-compose.full.yml up -d
```
