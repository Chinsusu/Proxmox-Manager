#!/usr/bin/env bash
# deploy-release.sh — giai nen release tarball (scripts/build-release.sh)
# vao /opt/vmf/releases/<version>, chay migration, doi symlink
# /opt/vmf/current, roi restart systemd units — theo docs/10 muc 3
# (Initial Deployment) va deploy/systemd/README.md.
#
# KHONG tu dong migrate neu --skip-migrate - migration co the doi
# schema khong tuong thich nguoc, nen mac dinh van chay nhung script se
# dung ngay va KHONG doi symlink/restart neu migrate that bai (tranh
# chay binary moi tren schema cu/nua-migrate).
#
# Usage:
#   sudo scripts/deploy-release.sh <release-tarball.tar.gz> \
#     [--base-dir /opt/vmf] [--dsn-file /run/credentials/postgres-dsn] \
#     [--skip-migrate] [--no-restart]

set -euo pipefail

BASE_DIR="/opt/vmf"
DSN_FILE="/run/credentials/postgres-dsn"
SKIP_MIGRATE=0
NO_RESTART=0
TARBALL=""

while [ $# -gt 0 ]; do
  case "$1" in
    --base-dir)
      BASE_DIR="${2:-}"
      shift 2
      ;;
    --dsn-file)
      DSN_FILE="${2:-}"
      shift 2
      ;;
    --skip-migrate)
      SKIP_MIGRATE=1
      shift
      ;;
    --no-restart)
      NO_RESTART=1
      shift
      ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^#//'
      exit 0
      ;;
    *)
      if [ -n "$TARBALL" ]; then
        echo "unexpected extra argument: $1" >&2
        exit 2
      fi
      TARBALL="$1"
      shift
      ;;
  esac
done

if [ -z "$TARBALL" ] || [ ! -f "$TARBALL" ]; then
  echo "usage: deploy-release.sh <release-tarball.tar.gz> [options] — file not found: ${TARBALL:-<missing>}" >&2
  exit 1
fi

log() { echo "[deploy-release] $*"; }

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

log "extracting $TARBALL"
tar -xzf "$TARBALL" -C "$WORK_DIR"
RELEASE_NAME="$(find "$WORK_DIR" -mindepth 1 -maxdepth 1 -type d -printf '%f\n')"
if [ -z "$RELEASE_NAME" ]; then
  echo "could not find release directory inside tarball" >&2
  exit 1
fi
VERSION="$(cat "$WORK_DIR/$RELEASE_NAME/VERSION")"
log "release=$RELEASE_NAME version=$VERSION"

RELEASES_DIR="$BASE_DIR/releases"
TARGET_DIR="$RELEASES_DIR/$VERSION"
mkdir -p "$RELEASES_DIR"
if [ -e "$TARGET_DIR" ]; then
  echo "release already deployed at $TARGET_DIR — remove it first if you intend to redeploy the same version" >&2
  exit 1
fi
cp -r "$WORK_DIR/$RELEASE_NAME" "$TARGET_DIR"
log "staged at $TARGET_DIR"

if [ "$SKIP_MIGRATE" -eq 0 ]; then
  if [ ! -f "$DSN_FILE" ]; then
    echo "DSN file not found: $DSN_FILE (pass --dsn-file or --skip-migrate)" >&2
    exit 1
  fi
  log "running migrations against DSN from $DSN_FILE"
  "$(dirname "$0")/migrate.sh" up --dsn-file "$DSN_FILE" --migrations-dir "$TARGET_DIR/migrations"
else
  log "--skip-migrate set — NOT running migrations (operator responsibility)"
fi

CURRENT_LINK="$BASE_DIR/current"
PREVIOUS_TARGET=""
if [ -L "$CURRENT_LINK" ]; then
  PREVIOUS_TARGET="$(readlink -f "$CURRENT_LINK")"
fi
ln -sfn "$TARGET_DIR" "$CURRENT_LINK"
log "symlink $CURRENT_LINK -> $TARGET_DIR (previous: ${PREVIOUS_TARGET:-none})"

if [ "$NO_RESTART" -eq 0 ]; then
  if command -v systemctl >/dev/null 2>&1; then
    log "restarting vmf-api vmf-worker"
    systemctl restart vmf-api vmf-worker
  else
    log "systemctl not found — skipping restart (not running under systemd, or --no-restart intended)"
  fi
else
  log "--no-restart set — symlink updated but services NOT restarted"
fi

log "deploy complete: version=$VERSION"
if [ -n "$PREVIOUS_TARGET" ]; then
  log "rollback with: scripts/rollback.sh --base-dir $BASE_DIR $(basename "$PREVIOUS_TARGET")"
fi
exit 0
