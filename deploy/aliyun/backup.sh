#!/usr/bin/env bash
# Gateway database backup — runs ON the Aliyun DB host (where the database
# is local), not on the small-disk overseas gateway server. Dumps over
# loopback, compresses with gzip, keeps the newest $KEEP copies.
#
# Setup (once, on the Aliyun host):
#   1. Put the DB password in ~/.gateway-backup.env (mode 600):
#        DB_PASS=...
#      (user comes from the gateway's deploy/.env DATABASE_URL)
#   2. cron, e.g. crontab -e:  20 4 * * * ~/gateway-backups/backup.sh >> ~/gateway-backups/backup.log 2>&1
set -euo pipefail

DB_HOST=${DB_HOST:-127.0.0.1}
DB_PORT=${DB_PORT:-15432}
DB_USER=${DB_USER:-gateway}
DB_NAME=${DB_NAME:-gateway}
BACKUP_DIR=${BACKUP_DIR:-$HOME/gateway-backups}
KEEP=${KEEP:-14}

if [[ -f "$HOME/.gateway-backup.env" ]]; then
  # shellcheck disable=SC1091
  . "$HOME/.gateway-backup.env"
fi
: "${DB_PASS:?set DB_PASS in ~/.gateway-backup.env or the environment}"
export PGPASSWORD="$DB_PASS"

mkdir -p "$BACKUP_DIR"
out="$BACKUP_DIR/gateway-$(date +%Y%m%dT%H%M%S).sql.gz"

pg_dump -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" | gzip > "$out"
gzip -t "$out"

# Rotate: everything older than the newest $KEEP.
ls -1t "$BACKUP_DIR"/gateway-*.sql.gz | tail -n +"$((KEEP + 1))" | xargs -r rm -f

echo "$(date -Is) backup ok: $out ($(du -h "$out" | cut -f1))"
