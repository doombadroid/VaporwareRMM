# vaporRMM Runbook

Quick reference for common operational tasks and troubleshooting.

## First Time Setup

```bash
cp .env.example .env
# Edit .env: set JWT_SECRET, POSTGRES_PASSWORD, DOMAIN
docker compose up -d
docker compose logs server | grep "ADMIN CREDENTIALS"
```

Save the printed admin password. It is only shown once.

## Health Checks

| Endpoint | Expected | Check |
|----------|----------|-------|
| `GET /health` | `{"status":"ok"}` | `docker compose exec server wget -qO- localhost:8080/health` |
| `GET /metrics` | Prometheus text | Available without auth |
| Postgres | `pg_isready` | Built into docker-compose healthcheck |

## Common Issues

### Server won't start

```bash
docker compose logs server
# Check: JWT_SECRET set? DATABASE_URL reachable? Port 8080 free?
```

### Database connection failed

```bash
# Test from server container:
docker compose exec server wget -qO- http://postgres:5432
# Should get empty response (not connection refused)
```

### Agent not registering

1. Check agent can reach server: `docker compose exec agent-example wget -qO- http://server:8080/health`
2. Verify `VAPOR_AGENT_TOKEN` matches a registered token
3. Check server logs for `registration failed`

### Forgot admin password

Use the forgot-password flow, or reset via API:
```bash
curl -X POST http://localhost:8080/api/auth/forgot-password \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@vaporrmm.local"}'
```

### Offline devices not detected

Check `OFFLINE_THRESHOLD_SECONDS` env var (default 120s).
The background goroutine runs every 60s.

## Backups

```bash
# SQLite
./scripts/backup.sh /path/to/vapor_rmm.db

# PostgreSQL
pg_dump -U vaporrmm vaporrmm > backup.sql
```

## Metrics & Alerting

- Prometheus: `http://localhost:9090` (if running compose with prometheus profile)
- Grafana: `http://localhost:3001` (staging compose)
- Alert emails require SMTP config via `GET/PUT /api/v1/alert-settings`

## Secrets Management

### Docker Swarm secrets
```bash
echo "my-secret" | docker secret create jwt_secret -
# Update docker-compose.yml to use `external: true` for the secret
```

### Kubernetes secrets
```bash
kubectl create secret generic vaporrmm-jwt --from-literal=JWT_SECRET=...
```

## Graceful Shutdown

Server handles SIGINT/SIGTERM:
- Closes offline detection goroutine
- Shuts down Fiber app
- Closes database connection

Send `docker compose stop server` for graceful shutdown.

## Log Levels

All packages use `log/slog`. Set level via environment:
```bash
LOG_LEVEL=debug go run ./packages/server
```

Default is INFO. Use `slog.Debug` for verbose tracing, `slog.Warn` for recoverable issues, `slog.Error` for failures requiring attention.
