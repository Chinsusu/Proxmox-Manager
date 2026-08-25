#!/usr/bin/env bash
# migrate.sh — wrapper quanh golang-migrate CLI doc DSN tu file (Phan II
# muc 15: "secret qua file", khop config.Database.DSNFile), thay vi doi
# hoi bien moi truong DATABASE_URL nhu `make migrate-up` (tien loi cho
# CI/dev, khong phu hop production dung systemd credential file).
#
# Yeu cau: golang-migrate CLI da cai (`go install -tags postgres
# github.com/golang-migrate/migrate/v4/cmd/migrate@latest`), khop cach
# CI cai o .github/workflows/ci.yml.
#
# Usage:
#   scripts/migrate.sh <up|down|status> [--dsn-file PATH] [--migrations-dir PATH] [-- <extra migrate args>]
#
# Vi du:
#   scripts/migrate.sh up --dsn-file /run/credentials/postgres-dsn
#   scripts/migrate.sh down --dsn-file /run/credentials/postgres-dsn -- 1

set -euo pipefail

DSN_FILE="/run/credentials/postgres-dsn"
MIGRATIONS_DIR="$(cd "$(dirname "$0")/.." && pwd)/migrations"
ACTION=""
EXTRA_ARGS=()

while [ $# -gt 0 ]; do
  case "$1" in
    --dsn-file)
      DSN_FILE="${2:-}"
      shift 2
      ;;
    --migrations-dir)
      MIGRATIONS_DIR="${2:-}"
      shift 2
      ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^#//'
      exit 0
      ;;
    --)
      shift
      EXTRA_ARGS=("$@")
      break
      ;;
    up|down|status)
      ACTION="$1"
      shift
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [ -z "$ACTION" ]; then
  echo "usage: migrate.sh <up|down|status> [--dsn-file PATH] [--migrations-dir PATH] [-- extra args]" >&2
  exit 2
fi

if ! command -v migrate >/dev/null 2>&1; then
  echo "golang-migrate CLI not found in PATH — install with:" >&2
  echo "  go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@latest" >&2
  exit 1
fi

if [ ! -f "$DSN_FILE" ]; then
  echo "DSN file not found: $DSN_FILE" >&2
  exit 1
fi
DSN="$(tr -d '[:space:]' < "$DSN_FILE")"
if [ -z "$DSN" ]; then
  echo "DSN file is empty: $DSN_FILE" >&2
  exit 1
fi

echo "[migrate] action=$ACTION migrations-dir=$MIGRATIONS_DIR" >&2
migrate -path "$MIGRATIONS_DIR" -database "$DSN" "$ACTION" "${EXTRA_ARGS[@]}"
