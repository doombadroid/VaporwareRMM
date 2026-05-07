#!/bin/bash
# vaporRMM database backup script
# Usage: ./scripts/backup.sh [sqlite|postgres]
#
# For SQLite: backs up ./data/vapor_rmm.db
# For PostgreSQL: uses pg_dump

set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-./backups}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
mkdir -p "$BACKUP_DIR"

DB_TYPE="${1:-sqlite}"

if [ "$DB_TYPE" = "sqlite" ]; then
    DB_PATH="${DATABASE_PATH:-./data/vapor_rmm.db}"
    if [ ! -f "$DB_PATH" ]; then
        echo "[!] SQLite database not found at $DB_PATH"
        exit 1
    fi
    BACKUP_FILE="$BACKUP_DIR/vaporrmm_sqlite_$TIMESTAMP.db"
    echo "[OK] Backing up SQLite database..."
    sqlite3 "$DB_PATH" ".backup '$BACKUP_FILE'"
    echo "[OK] Backup saved to $BACKUP_FILE"

elif [ "$DB_TYPE" = "postgres" ]; then
    DATABASE_URL="${DATABASE_URL:-}"
    if [ -z "$DATABASE_URL" ]; then
        echo "[!] DATABASE_URL not set"
        exit 1
    fi
    BACKUP_FILE="$BACKUP_DIR/vaporrmm_postgres_$TIMESTAMP.sql"
    echo "[OK] Backing up PostgreSQL database..."
    pg_dump "$DATABASE_URL" > "$BACKUP_FILE"
    echo "[OK] Backup saved to $BACKUP_FILE"
else
    echo "[!] Unknown database type: $DB_TYPE"
    echo "    Usage: $0 [sqlite|postgres]"
    exit 1
fi

# Clean up old backups (keep last 30)
echo "[OK] Cleaning up old backups..."
ls -t "$BACKUP_DIR"/vaporrmm_* 2>/dev/null | tail -n +31 | xargs -r rm -f
echo "[OK] Backup complete"
