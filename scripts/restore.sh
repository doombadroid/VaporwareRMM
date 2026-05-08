#!/bin/bash
# vaporRMM database restore script
# Usage: ./scripts/restore.sh sqlite <backup-file>
#        ./scripts/restore.sh postgres <backup-file>
#
# IMPORTANT: stop the server before running this. The script refuses to run
# if the dev server (:8080) or compose stack is still up.

set -euo pipefail

DB_TYPE="${1:-}"
BACKUP_FILE="${2:-}"

if [ -z "$DB_TYPE" ] || [ -z "$BACKUP_FILE" ]; then
    echo "Usage: $0 <sqlite|postgres> <backup-file>"
    exit 1
fi
if [ ! -f "$BACKUP_FILE" ]; then
    echo "[!] Backup file not found: $BACKUP_FILE"
    exit 1
fi

# Refuse to clobber a running server
if ss -tln 2>/dev/null | grep -q ":8080 "; then
    echo "[!] Server is listening on :8080. Stop it before restoring."
    echo "    sudo systemctl stop vaporrmm-server  OR  docker compose stop server"
    exit 1
fi

case "$DB_TYPE" in
    sqlite)
        DB_PATH="${DATABASE_PATH:-./data/vapor_rmm.db}"
        # Make sure parent dir exists
        mkdir -p "$(dirname "$DB_PATH")"
        # Move existing DB out of the way (don't delete — paranoia)
        if [ -f "$DB_PATH" ]; then
            BACKUP_OF_OLD="$DB_PATH.before-restore-$(date +%Y%m%d_%H%M%S)"
            cp "$DB_PATH" "$BACKUP_OF_OLD"
            echo "[OK] Saved current DB to $BACKUP_OF_OLD"
        fi
        # Stale WAL / SHM files from the previous DB are dangerous: SQLite would
        # apply them on first open, corrupting the freshly-restored content.
        # Remove them BEFORE laying the backup down.
        rm -f "$DB_PATH-wal" "$DB_PATH-shm" "$DB_PATH-journal"
        # SQLite backups produced by .backup are valid DB files; just copy them in.
        cp "$BACKUP_FILE" "$DB_PATH"
        # Verify integrity before declaring victory
        if ! sqlite3 "$DB_PATH" 'PRAGMA integrity_check;' | grep -q '^ok$'; then
            echo "[!] integrity_check failed — restoring previous DB"
            cp "$BACKUP_OF_OLD" "$DB_PATH"
            exit 1
        fi
        echo "[OK] SQLite restored. Start the server to verify."
        ;;
    postgres)
        DATABASE_URL="${DATABASE_URL:-}"
        if [ -z "$DATABASE_URL" ]; then
            echo "[!] DATABASE_URL not set"
            exit 1
        fi
        # We expect a plain SQL dump produced by pg_dump (default format).
        # WARNING: this DROPS and recreates schema objects. If your dump was
        # taken with --create or --clean it'll handle that itself.
        echo "[OK] Restoring Postgres dump from $BACKUP_FILE"
        psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$BACKUP_FILE"
        echo "[OK] Postgres restore complete. Run migrations on next server start to confirm schema."
        ;;
    *)
        echo "[!] Unknown DB type: $DB_TYPE"
        exit 1
        ;;
esac
