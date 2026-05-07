# vaporRMM Docker Quick Reference

## One-Liners

```bash
# Start everything (full stack with test agents)
cd docker && sudo ./setup-docker.sh

# Start minimal (server + dashboard only)
cd docker && docker compose up -d

# Start full stack
cd docker && docker compose -f docker-compose.full.yml up -d

# Run tests
cd docker && ./test.sh

# View everything
docker compose -f docker-compose.full.yml ps
```

## Common Tasks

### Check Status
```bash
docker compose -f docker-compose.full.yml ps
docker compose -f docker-compose.full.yml top
```

### Logs
```bash
# All
docker compose -f docker-compose.full.yml logs -f

# Server only
docker compose -f docker-compose.full.yml logs -f server

# Agents only
docker compose -f docker-compose.full.yml logs -f agent-ubuntu agent-alpine

# Last 50 lines
docker compose -f docker-compose.full.yml logs --tail=50 server
```

### Restart
```bash
# One service
docker compose -f docker-compose.full.yml restart server

# All
docker compose -f docker-compose.full.yml restart
```

### Shell Access
```bash
# Server
docker compose -f docker-compose.full.yml exec server bash

# Check DB
docker compose -f docker-compose.full.yml exec server sqlite3 /app/data/vapor_rmm.db ".tables"

# Check Tailscale
docker compose -f docker-compose.full.yml exec server tailscale status

# Agent
docker compose -f docker-compose.full.yml exec agent-ubuntu bash
```

### Data Management
```bash
# Backup SQLite DB
cp docker/data/vapor_rmm.db docker/backups/vapor_rmm-$(date +%Y%m%d).db

# Reset everything (data + containers)
docker compose -f docker-compose.full.yml down -v
rm -rf docker/data/*

# Rebuild from scratch
docker compose -f docker-compose.full.yml down
docker compose -f docker-compose.full.yml up -d --build
```

### Network
```bash
# Inspect network
docker network inspect docker_vaporrmm

# Test connectivity between containers
docker compose -f docker-compose.full.yml exec agent-ubuntu wget -qO- http://server:8080/health
```

## URLs

| Service | Local | From Another Machine |
|---------|-------|---------------------|
| Dashboard | http://localhost:3000 | http://YOUR_IP:3000 |
| API | http://localhost:8080 | http://YOUR_IP:8080 |
| Health | http://localhost:8080/health | http://YOUR_IP:8080/health |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_SECRET` | (required) | Random string for JWT signing |
| `ADMIN_PASSWORD` | ChangeMe123! | Initial admin password |
| `TAILSCALE_AUTH_KEY` | (optional) | Auth key for Tailscale integration |
| `CORS_ORIGINS` | localhost:3000 | Allowed dashboard origins |
| `SERVER_HOST` | 0.0.0.0 | Bind address |
| `SERVER_PORT` | 8080 | API port |

## File Locations

| Path | Content |
|------|---------|
| `docker/data/` | SQLite database |
| `docker/backups/` | DB backups |
| `docker/.env` | Configuration |
| `docker/vaporrmm-server` | Server binary |
| `docker/vaporrmm-agent` | Agent binary |

## Troubleshooting

**Port already in use**
```bash
sudo lsof -i :8080
# Edit docker-compose.full.yml to change ports
```

**Permission denied**
```bash
sudo chown -R $USER:$USER docker/data docker/backups
```

**Build fails**
```bash
# Clear Docker cache
docker compose -f docker-compose.full.yml build --no-cache
```
