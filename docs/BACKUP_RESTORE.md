# Backup & Restore Runbook

Runbook for backing up and restoring the vaporRMM database. Run a drill end-to-end before going live with real customer data.

## What's in scope

- **PostgreSQL** (production): full SQL dump via `pg_dump`. Recovers everything including audit logs.
- **SQLite** (dev / single-host): file-level snapshot via `sqlite3 .backup`. Atomic — safe while the server is running.

## What's out of scope

- Caddy TLS data (`vaporrmm-caddy-data`). Re-issued automatically on first start of a fresh stack — no need to back up.
- Redis (`vaporrmm-redis-data`). Cache only; data is reconstructible.
- Agent binaries (re-shipped from the server `/download/agent-...` endpoint).

## Backup

```bash
# PostgreSQL — set DATABASE_URL first
./scripts/backup.sh postgres

# SQLite — set DATABASE_PATH or use the default ./data/vapor_rmm.db
./scripts/backup.sh sqlite
```

Backups land in `./backups/` by default (override with `BACKUP_DIR=/path/to/dir`). Filenames include a timestamp.

### Recommended schedule

- **Hourly**: append-only WAL snapshot (Postgres) or `.backup` to a hot directory (SQLite). 24 retained.
- **Daily**: copy the latest hourly to cold storage (S3, B2, GCS). 30 retained.
- **Weekly**: archive a daily snapshot offsite. 12 retained.

Run via cron / systemd timer / OpenRC's cron-equivalent. Example systemd timer:

```ini
# /etc/systemd/system/vaporrmm-backup.service
[Service]
Type=oneshot
WorkingDirectory=/srv/vaporrmm
ExecStart=/srv/vaporrmm/scripts/backup.sh postgres
Environment=DATABASE_URL=postgres://vaporrmm:%i@localhost:5432/vaporrmm
EnvironmentFile=/etc/vaporrmm/backup.env

# /etc/systemd/system/vaporrmm-backup.timer
[Timer]
OnCalendar=hourly
Persistent=true
[Install]
WantedBy=timers.target
```

OpenRC equivalent: drop `vaporrmm-backup` script in `/etc/cron.hourly/`.

## Restore

**Stop the server first.** The script refuses to run if it sees `:8080` listening.

```bash
# Stop server
docker compose stop server     # OR  rc-service vaporrmm-server stop

# PostgreSQL
DATABASE_URL='postgres://...' ./scripts/restore.sh postgres ./backups/vaporrmm_postgres_20260508_120000.sql

# SQLite
DATABASE_PATH='./data/vapor_rmm.db' ./scripts/restore.sh sqlite ./backups/vaporrmm_sqlite_20260508_120000.db

# Start server again
docker compose start server     # OR  rc-service vaporrmm-server start
```

The script always saves the existing DB as `<path>.before-restore-<timestamp>` before clobbering, and runs `PRAGMA integrity_check` (SQLite) before declaring success.

## Drill — required before go-live

Run this end-to-end on a non-prod copy. Time-box to under an hour.

1. Take a backup. Note the file path + size.
2. Note a few specific records: pick a tenant, a user, and a device. Capture their IDs.
3. **Wreck the DB**: drop a table, delete a tenant, scramble user roles. Pick something destructive that you'd notice.
4. Restore from the backup.
5. Start the server. Verify:
   - Migrations log clean
   - The records you noted in step 2 are back exactly as before
   - You can log in with the pre-wreck admin password
   - At least one device shows up online again after agent heartbeat
6. Document the time taken from step 4 to step 5. That's your **RTO**.

## Recovery objectives (target for 10-tenant MSP)

- **RPO** (max data loss): 1 hour (matches hourly backup cadence)
- **RTO** (max downtime): 30 minutes (restore + start + smoke test)

If you can't hit those, raise backup frequency or invest in streaming replication.

## Common pitfalls

- **Restoring with the server still running**: corrupts the DB. The script blocks this.
- **PostgreSQL extension drift**: restore can fail if your prod has extensions the dump doesn't include. Use `pg_dump --create --clean` for full reconstructibility.
- **JWT_SECRET mismatch after restore**: existing user sessions are invalidated (signature won't verify). Users must log in again. Expected behaviour.
- **SECRETS_ENCRYPTION_KEY mismatch**: SMTP passwords + TOTP secrets won't decrypt. Restore must use the same key. Store it offsite separately from the backup.

## Per-tenant restore (selective)

For "tenant X had a bad day, roll back just their data":

1. Take a fresh full backup (insurance).
2. Restore the historical backup to a SCRATCH database (different name).
3. `pg_dump` only that tenant's tables from scratch into another file (filter by `WHERE tenant_id = 'tenant-x'`).
4. On prod, `DELETE FROM ... WHERE tenant_id = 'tenant-x'` then load the filtered dump.

Or use the tenant export endpoint (see `docs/DATA_EXPORT.md`) for a JSON-based path.
