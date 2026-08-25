#!/usr/bin/env bash
# backup.sh — DB logical backup (pg_dump custom format) + config non-secret,
# theo docs/10 muc 8 (Backup):
#   "DB daily logical plus RPO strategy"
#   "Config, CA, migration binaries"
#   "Template build manifests/checksums"
#   "Do not back up plaintext secret outside secret system"
#
# CO Y KHONG backup /run/credentials/* (secret dsn/token/hmac-key) —
# secret phai o rieng trong secret manager cua ha tang (Phan II muc
# 15), khong nam trong backup archive nay. --config-dir mac dinh
# /etc/vm-factory CHI chua config.yaml (khong chua secret, xem
# deploy/systemd/README.md) nen an toan de dua nguyen vao archive.
#
# Usage:
#   scripts/backup.sh [--dsn-file PATH] [--config-dir /etc/vm-factory] [--out-dir /var/backups/vmf]

set -euo pipefail

DSN_FILE="/run/credentials/postgres-dsn"
CONFIG_DIR="/etc/vm-factory"
OUT_DIR="/var/backups/vmf"

while [ $# -gt 0 ]; do
  case "$1" in
    --dsn-file)
      DSN_FILE="${2:-}"
      shift 2
      ;;
    --config-dir)
      CONFIG_DIR="${2:-}"
      shift 2
      ;;
    --out-dir)
      OUT_DIR="${2:-}"
      shift 2
      ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^#//'
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

log() { echo "[backup] $*"; }

if ! command -v pg_dump >/dev/null 2>&1; then
  echo "pg_dump not found in PATH" >&2
  exit 1
fi
if [ ! -f "$DSN_FILE" ]; then
  echo "DSN file not found: $DSN_FILE" >&2
  exit 1
fi
DSN="$(tr -d '[:space:]' < "$DSN_FILE")"

mkdir -p "$OUT_DIR"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"

DB_DUMP="$OUT_DIR/vmf-db-$STAMP.dump"
log "pg_dump (custom format) -> $DB_DUMP"
pg_dump --format=custom --file="$DB_DUMP" "$DSN"
sha256sum "$DB_DUMP" > "$DB_DUMP.sha256"

CONFIG_ARCHIVE="$OUT_DIR/vmf-config-$STAMP.tar.gz"
if [ -d "$CONFIG_DIR" ]; then
  log "archiving config from $CONFIG_DIR -> $CONFIG_ARCHIVE (/run/credentials KHONG nam trong day)"
  tar -czf "$CONFIG_ARCHIVE" -C "$(dirname "$CONFIG_DIR")" "$(basename "$CONFIG_DIR")"
  sha256sum "$CONFIG_ARCHIVE" > "$CONFIG_ARCHIVE.sha256"
else
  log "config dir not found ($CONFIG_DIR) — skipping config archive"
fi

log "backup complete: $DB_DUMP"
log "restore with: scripts/restore.sh $DB_DUMP --dsn-file <isolated-environment-dsn-file>"
