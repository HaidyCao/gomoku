#!/usr/bin/env bash
# Online, WAL-safe backup of the gomoku SQLite database.
#
# Run this on the HOST, not inside the container: the runtime image is minimal
# and has no sqlite3 CLI. Point DB_PATH at the bind-mounted / volume database.
# `sqlite3 .backup` produces a consistent copy even while the server is running,
# so it correctly captures data still in the -wal file.
#
# Usage:
#   DB_PATH=/var/lib/gomoku/wuziqi.db BACKUP_DIR=/var/backups/gomoku ./backup.sh
set -euo pipefail

DB_PATH="${DB_PATH:-/var/lib/gomoku/wuziqi.db}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/gomoku}"
KEEP="${KEEP:-14}"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "error: sqlite3 CLI not found; install it on the host (e.g. apt-get install sqlite3)" >&2
  exit 1
fi
if [ ! -f "$DB_PATH" ]; then
  echo "error: database not found at $DB_PATH" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"
timestamp="$(date +%Y%m%d-%H%M%S)"
dest="$BACKUP_DIR/wuziqi-$timestamp.db"

sqlite3 "$DB_PATH" ".backup '$dest'"
if ! sqlite3 "$dest" 'PRAGMA integrity_check;' | grep -q '^ok$'; then
  echo "error: integrity check failed for $dest" >&2
  rm -f "$dest"
  exit 1
fi
gzip -f "$dest"
echo "backup written: $dest.gz"

# Rotation: keep the newest $KEEP archives, delete older ones.
ls -1t "$BACKUP_DIR"/wuziqi-*.db.gz 2>/dev/null | tail -n +"$((KEEP + 1))" | xargs -r rm -f
