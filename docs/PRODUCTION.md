# Production Deployment Runbook

Tested target: 10-tenant MSP, ~1000 managed devices, single-region single-host (cheap) or HA pair (durable).

## 0. Prerequisites checklist

- [ ] Real domain with DNS control. You'll need:
      - `rmm.yourdomain.com` (apex)
      - `*.rmm.yourdomain.com` (wildcard A/AAAA → server IP) for tenant subdomains
- [ ] Server: 4 vCPU / 8GB RAM / 100GB SSD, Ubuntu 22.04+ or RHEL/Alma 9+ or Gentoo
- [ ] Ports 80, 443, 443/udp open inbound (Caddy + HTTP/3)
- [ ] Outbound to GitHub (Sunshine releases) and Tailscale control plane
- [ ] SMTP relay: Postmark, SES, Sendgrid, or your own (for invites + password reset)
- [ ] Tailscale account + a node tagged `tag:rmm-server` with auth-key issue permission
- [ ] PostgreSQL 16+ available (managed RDS/CloudSQL OR a separate host OR co-located in compose)

## 1. First boot

### 1a. Clone + secrets

```bash
git clone https://github.com/<you>/vaporRMM /srv/vaporrmm
cd /srv/vaporrmm

# Generate the 4 production secrets. Save these to your password manager.
openssl rand -hex 32 > secrets/jwt_secret.txt           # 64-char hex
openssl rand -base64 32                                  # SECRETS_ENCRYPTION_KEY
openssl rand -base64 24                                  # POSTGRES_PASSWORD
openssl rand -base64 32                                  # ADMIN_PASSWORD (printable)
```

### 1b. `.env` file

Create `.env` at repo root:

```bash
# Public URL — drives password-reset links, agent install commands, OAuth, etc.
DOMAIN=rmm.yourdomain.com
PUBLIC_URL=https://rmm.yourdomain.com
ACME_EMAIL=ops@yourdomain.com

# Database
POSTGRES_PASSWORD=<paste>

# Server secrets
JWT_SECRET=<paste hex>
SECRETS_ENCRYPTION_KEY=<paste base64>
ADMIN_PASSWORD=<paste — first-run admin login>

# Optional but recommended
REGISTRATION_SECRET=<random hex — global fallback for agents>
SUSPENSION_GRACE_HOURS=72
SIGNUP_INVITE_CODE=<random hex IF you want self-serve signup gated by a code; omit for invite-only>

# Allowed CORS origins. For subdomain tenants, list the apex + wildcard pattern.
CORS_ORIGINS=https://rmm.yourdomain.com
```

### 1c. Bring up the stack

```bash
docker compose up -d
docker compose logs -f server
# Look for: "ADMIN CREDENTIALS (first run only — save these now!)"
# Capture the random password if you didn't set ADMIN_PASSWORD.
```

### 1d. Verify

```bash
curl https://rmm.yourdomain.com/health        # → 200 {"status":"ok"}
curl https://rmm.yourdomain.com/api/branding/ # → 200 default branding JSON
```

Open `https://rmm.yourdomain.com` in a browser. Log in as `admin@vaporrmm.local`.

### 1e. Hardening (do these on day one, not week three)

1. Change the default admin email + password (Settings → Users → ⋯)
2. Enable TOTP on the admin account (Settings → Security)
3. Print the backup codes onto paper, lock in a safe
4. Run `make test-postgres` against the live DB once to confirm migrations applied cleanly
5. Run a backup + restore drill (see `docs/BACKUP_RESTORE.md`)

## 2. Onboarding the first tenant

### 2a. Create

- Tenants → New tenant → name, slug = `acme`
- The registration secret panel appears once. Save the install command.

### 2b. Register agents

Run the install command on each managed machine:

```bash
# Linux/macOS
curl -fsSL https://rmm.yourdomain.com/api/branding/agent-install?format=script \
  | sudo REGISTRATION_SECRET='vrt_xxxxx' bash -s -- --server https://rmm.yourdomain.com

# Windows (PowerShell as Admin)
$env:REGISTRATION_SECRET='vrt_xxxxx'
iwr -UseBasicParsing https://rmm.yourdomain.com/api/branding/agent-install?format=script | iex
```

Each agent registers exactly once with the registration secret, then uses its persistent bearer token forever after.

### 2c. Sunshine + Tailscale (optional)

Only install on machines that need remote desktop:

```bash
sudo bash install.sh --install-sunshine --install-tailscale --tailscale-auth-key tskey-xxx
```

Verify integrations work:

```bash
curl https://rmm.yourdomain.com/api/v1/admin/probes/tailscale -b cookies.txt -H "X-CSRF-Token: ..."
# {"ok":true, "detail":"Tailscale CLI present, authenticated, can issue auth keys."}
```

## 3. Operational tasks

### Monitoring

Prometheus scrapes `/metrics`. Required env: `METRICS_API_KEY` (set, then add `Authorization: Bearer ...` to your scrape config). Per-tenant gauges:

```promql
vaporrmm_tenant_devices{tenant_id="acme"}
vaporrmm_tenant_devices_online{tenant_id="acme"}
vaporrmm_tenant_users{tenant_id="acme"}
vaporrmm_tenants_active
vaporrmm_tenants_suspended
```

Suggested alerts:
- `vaporrmm_tenant_devices_online{tenant_id="*"} / vaporrmm_tenant_devices < 0.5` for > 10 min → fleet outage
- `http_request_duration_seconds{path="/agent/heartbeat"} > 0.5` p95 for > 5 min → DB pressure
- `db_in_use_connections / db_open_connections > 0.9` for > 5 min → bump pool
- `vaporrmm_tenants_suspended > 0` → flag for ops review

### Backups

Automate `scripts/backup.sh postgres` hourly via systemd timer or OpenRC cron. Ship to off-host storage daily. See `docs/BACKUP_RESTORE.md` for the drill procedure.

### Updating the server

1. `git fetch && git checkout v<new-tag>`
2. `docker compose build --pull server dashboard caddy`
3. `docker compose up -d server`     # Caddy + dashboard hot-restart on next change
4. Migrations run automatically on startup. Watch `docker compose logs server` for `migration applied`.

If a migration fails: stop, restore from the last backup, file an issue. Don't try to hand-fix prod.

### Tenant suspension

Default grace period: 72 hours (`SUSPENSION_GRACE_HOURS=72`). During grace, users see a red banner with the deadline. After grace, both dashboard + agents return 403. Reactivating clears `suspended_at` and re-allows access immediately.

To suspend a tenant: Tenants → ⋯ → Suspend.

### Tenant offboarding (right-to-erasure)

```bash
# Give them their data first
curl -X GET https://rmm.yourdomain.com/api/v1/admin/tenants/<id>/export \
  -b cookies.txt > tenant-export.json

# Then purge. Audit logs preserved by default. Use ?include_audit=1 for full erasure.
curl -X DELETE 'https://rmm.yourdomain.com/api/v1/admin/tenants/<id>/purge'
```

## 4. Scaling beyond one box

When the single-host setup hits its limits:

| Symptom | Move |
|---|---|
| DB pool exhausted, p99 latency climbing | Move PostgreSQL off the server host. Connect via `DATABASE_URL`. |
| WebSocket clients dropping during deploys | Run 2+ server replicas behind Caddy. Confirm `REDIS_URL` is set so filtered events fan out across replicas. |
| Tailscale auth-key generation slow | Use OAuth-issued keys instead of CLI. |
| Sunshine downloads timing out | Mirror the .deb/.exe to your own object storage; bump `SUNSHINE_VERSION` to point at the mirror. |
| Agent registration spikes | Move rate-limit state to Redis (already automatic when `REDIS_URL` is set). |

## 5. Disaster recovery

| Scenario | Recovery |
|---|---|
| DB host gone | Restore latest pg_dump per `docs/BACKUP_RESTORE.md`. RTO 30 min if backups land on cold storage. |
| Server host gone | New host, clone repo, restore secrets (env + jwt_secret.txt + encryption key), restore DB. RTO ~1 hour. |
| Lost SECRETS_ENCRYPTION_KEY | SMTP passwords + TOTP secrets unrecoverable. Operators must re-enter SMTP creds + re-enrol TOTP. Existing agents continue working (their bearer tokens are not encrypted with this key). |
| Lost JWT_SECRET | All sessions + invites + TOTP challenges invalidated. Users must log in again. Agents unaffected. |
| Lost ADMIN_PASSWORD | If TOTP on, admin uses backup code. If TOTP off and locked out, restore from a backup taken before the lockout. Last resort: shell into DB, `UPDATE users SET password_hash = '<bcrypt of new>' WHERE email = 'admin@vaporrmm.local'`. |
| Tenant accidentally purged | Restore from previous backup; this is the WHOLE database, so you'll lose other tenants' recent activity. Better: tenant export → keep monthly per-tenant snapshots. |

## 6. Things this runbook deliberately doesn't cover

- Multi-region deployment
- Read replicas
- SSO / SAML
- Stripe billing
- Pen test prep
- SOC2 audit prep

Those are Tier 5+ work. Add them when the customer asks.
