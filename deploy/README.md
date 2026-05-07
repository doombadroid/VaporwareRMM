# vaporRMM Production Deployment Guide

This directory contains everything you need to deploy vaporRMM on a remote VM.

## Architecture

```
[ Your Machine / VM ]          [ Remote Agents ]
       |                               |
       |  http://vm-ip:8080            |  Register + Heartbeat
       |  http://vm-ip:3000            |
   [ vaporRMM Server ]           [ vaporRMM Agent ]
   [ vaporRMM Dashboard ]
```

## Quick Start (VM Setup)

### 1. Prepare the deployment package

On your **build machine** (where you develop vaporRMM):

```bash
cd /path/to/vaporRMM

# Build the server binary
cd packages/server
go build -o ../../deploy/vaporrmm-server ./main.go
cd ../..

# Build the agent binary (needed for the download endpoint)
cd packages/agent
go build -o ../../deploy/vaporrmm-agent ./main.go
cd ../..

# The deploy/ directory now contains everything
ls deploy/
# setup-server.sh    vaporrmm-server    vaporrmm-agent
# systemd/           server.env.example  dashboard.env.example
```

### 2. Copy to your VM

```bash
# From your build machine
scp -r deploy/ user@your-vm-ip:~/
ssh user@your-vm-ip
```

### 3. Run the setup script

On the **VM**:

```bash
cd ~/deploy
sudo ./setup-server.sh
```

This will:
- Install Go, Node.js, SQLite (if missing)
- Create `vaporrmm` user
- Install server binary + dashboard
- Create config files with a random JWT secret
- Start systemd/OpenRC services

### 4. Configure

Edit `/etc/vaporrmm/server.env` on the VM:

```bash
sudo nano /etc/vaporrmm/server.env
```

Key settings:
```env
# Change this!
ADMIN_PASSWORD=YourStrongPassword123!

# Bind to all interfaces so remote agents can reach you
SERVER_HOST=0.0.0.0

# Add your VM's public domain/IP to CORS
CORS_ORIGINS=http://your-vm-ip:3000,https://your-domain.com

# Optional: PostgreSQL instead of SQLite
# DATABASE_URL=postgres://user:pass@localhost/vaporrmm
```

Restart after changes:
```bash
sudo systemctl restart vaporrmm-server vaporrmm-dashboard
```

### 5. Access the dashboard

Open your browser:
```
http://your-vm-ip:3000
```

Login:
- Email: `admin@vaporrmm.local`
- Password: (whatever you set in `ADMIN_PASSWORD`)

### 6. Install agent on a remote machine

From **any machine** that can reach your VM:

```bash
curl -fsSL http://your-vm-ip:8080/api/branding/agent-install?format=script | sudo bash -s -- --server http://your-vm-ip:8080
```

The agent will:
1. Download the binary from your server
2. Install an init service (systemd or OpenRC)
3. Start sending heartbeats

Check the dashboard — the new device should appear within 30 seconds.

---

## Tailscale Integration

To test Tailscale:

1. **On the VM** (server), install Tailscale:
   ```bash
   curl -fsSL https://tailscale.com/install.sh | sh
   sudo tailscale up
   ```

2. **On the agent machine**, install Tailscale:
   ```bash
   curl -fsSL https://tailscale.com/install.sh | sh
   sudo tailscale up
   ```

3. In the dashboard, click a device → **Tailscale** button → generate auth key → install

---

## Troubleshooting

### Agent can't connect to server

```bash
# From the agent machine, test connectivity
curl http://your-vm-ip:8080/health

# Check server logs on the VM
sudo journalctl -u vaporrmm-server -f
```

### Dashboard shows "Something went wrong"

```bash
# Check if API is reachable from dashboard
curl http://localhost:8080/health

# Check dashboard logs
sudo journalctl -u vaporrmm-dashboard -f
```

### Rate limiting blocks agent

The agent heartbeat is limited to 5/min. If you see 429 errors, the agent will auto-backoff and retry.

### Binary download fails (404)

The server serves the agent binary from `/tmp/vaporrmm-agent`. Make sure you built it:
```bash
cd packages/agent && go build -o /tmp/vaporrmm-agent ./main.go
```

---

## Updating

To update the server:

```bash
# On build machine
cd packages/server && go build -o ../../deploy/vaporrmm-server ./main.go

# Copy to VM and replace
scp deploy/vaporrmm-server user@vm-ip:/tmp/
ssh user@vm-ip "sudo mv /tmp/vaporrmm-server /opt/vaporrmm/server/ && sudo systemctl restart vaporrmm-server"
```

To update the dashboard:

```bash
# On build machine
cd apps/dashboard && npm run build

# Copy to VM
rsync -av --delete apps/dashboard/ user@vm-ip:/opt/vaporrmm/dashboard/
ssh user@vm-ip "sudo systemctl restart vaporrmm-dashboard"
```

---

## Security Checklist

- [ ] Change `ADMIN_PASSWORD` from default
- [ ] Set a strong `JWT_SECRET`
- [ ] Enable TLS (`TLS_CERT` + `TLS_KEY`)
- [ ] Restrict CORS to your actual domain
- [ ] Use PostgreSQL instead of SQLite for production
- [ ] Set up a reverse proxy (Caddy/nginx) with HTTPS
- [ ] Firewall: only expose 3000 (dashboard) and 8080 (API) if needed
