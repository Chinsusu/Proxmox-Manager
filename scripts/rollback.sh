#!/usr/bin/env bash
# rollback.sh — doi symlink /opt/vmf/current sang mot release da deploy
# truoc do roi restart systemd units, theo docs/10 muc 12 (Change
# Management) va deploy/systemd/README.md.
#
# KHONG tu dong rollback migration DB — migration down co the mat du
# lieu, luon la quyet dinh thu cong co review rieng (chay
# `scripts/migrate.sh down` tach biet neu that su can, sau khi xac nhan
# release dang rollback ve KHONG phu thuoc migration da apply sau no).
#
# Usage:
#   sudo scripts/rollback.sh [<version>] [--base-dir /opt/vmf] [--no-restart]
#
# <version> bo trong: rollback ve release truoc release hien tai theo
# thu tu ten thu muc trong releases/ (sap theo ten, tuong ung version tag).

set -euo pipefail

BASE_DIR="/opt/vmf"
NO_RESTART=0
TARGET_VERSION=""

while [ $# -gt 0 ]; do
  case "$1" in
    --base-dir)
      BASE_DIR="${2:-}"
      shift 2
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
      if [ -n "$TARGET_VERSION" ]; then
        echo "unexpected extra argument: $1" >&2
        exit 2
      fi
      TARGET_VERSION="$1"
      shift
      ;;
  esac
done

log() { echo "[rollback] $*"; }

RELEASES_DIR="$BASE_DIR/releases"
CURRENT_LINK="$BASE_DIR/current"

if [ ! -d "$RELEASES_DIR" ]; then
  echo "releases dir not found: $RELEASES_DIR" >&2
  exit 1
fi

CURRENT_VERSION=""
if [ -L "$CURRENT_LINK" ]; then
  CURRENT_VERSION="$(basename "$(readlink -f "$CURRENT_LINK")")"
fi

if [ -z "$TARGET_VERSION" ]; then
  # releases/ duoc dat ten theo version tag (vd v0.1.0, v0.1.1) - sort
  # -V (version sort) de tim release NGAY TRUOC current theo thu tu
  # semver, khong phai thu tu tao file (deploy co the chay khong theo
  # dung trinh tu thoi gian trong moi truong test/lab).
  mapfile -t VERSIONS < <(find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | sort -V)
  TARGET_VERSION=""
  for v in "${VERSIONS[@]}"; do
    if [ "$v" = "$CURRENT_VERSION" ]; then
      break
    fi
    TARGET_VERSION="$v"
  done
  if [ -z "$TARGET_VERSION" ]; then
    echo "could not auto-detect a previous release before '$CURRENT_VERSION' in $RELEASES_DIR — pass <version> explicitly" >&2
    exit 1
  fi
  log "auto-detected previous release: $TARGET_VERSION (current: ${CURRENT_VERSION:-none})"
fi

TARGET_DIR="$RELEASES_DIR/$TARGET_VERSION"
if [ ! -d "$TARGET_DIR" ]; then
  echo "release not found: $TARGET_DIR" >&2
  exit 1
fi

ln -sfn "$TARGET_DIR" "$CURRENT_LINK"
log "symlink $CURRENT_LINK -> $TARGET_DIR (rolled back from ${CURRENT_VERSION:-none})"

if [ "$NO_RESTART" -eq 0 ]; then
  if command -v systemctl >/dev/null 2>&1; then
    log "restarting vmf-api vmf-worker"
    systemctl restart vmf-api vmf-worker
  else
    log "systemctl not found — skipping restart"
  fi
else
  log "--no-restart set — symlink updated but services NOT restarted"
fi

log "rollback complete: now on $TARGET_VERSION"
