#!/usr/bin/env bash
# build-release.sh — build vmf-api/vmf-worker/vmf (CLI) tinh (CGO_ENABLED=0)
# va dong goi thanh mot release tarball, theo
# docs/11_Engineering_Standards_and_Git_Workflow_v1.0.md Phan 8 (Build &
# Release): -trimpath, embed version/commit/build-time qua -ldflags,
# SHA-256 checksum, SBOM best-effort (syft neu co), changelog tu
# Conventional Commit (feat/fix/perf) ke tu tag truoc.
#
# Usage:
#   scripts/build-release.sh [--out-dir dist]
#
# Output: dist/vmf-release-<version>.tar.gz + dist/vmf-release-<version>.tar.gz.sha256

set -euo pipefail

OUT_DIR="dist"

while [ $# -gt 0 ]; do
  case "$1" in
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

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT" || exit 1

log() { echo "[build-release] $*"; }

VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-X main.version=$VERSION -X main.commit=$COMMIT -X main.buildTime=$BUILD_TIME"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

RELEASE_NAME="vmf-release-$VERSION"
STAGE_DIR="$WORK_DIR/$RELEASE_NAME"
mkdir -p "$STAGE_DIR/bin"

log "building version=$VERSION commit=$COMMIT build_time=$BUILD_TIME"
for pkg in api worker cli; do
  bin_name="vmf-$pkg"
  if [ "$pkg" = "cli" ]; then
    bin_name="vmf"
  fi
  log "go build ./cmd/$pkg -> bin/$bin_name"
  CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$STAGE_DIR/bin/$bin_name" "./cmd/$pkg"
done

log "staging migrations/configs/deploy artifacts"
cp -r migrations "$STAGE_DIR/migrations"
mkdir -p "$STAGE_DIR/configs"
cp configs/vm-factory.example.yaml "$STAGE_DIR/configs/"
mkdir -p "$STAGE_DIR/deploy/systemd"
cp deploy/systemd/*.service deploy/systemd/README.md "$STAGE_DIR/deploy/systemd/"
if [ -d deploy/observability ]; then
  cp -r deploy/observability "$STAGE_DIR/deploy/observability"
fi
echo "$VERSION" > "$STAGE_DIR/VERSION"

log "generating changelog (feat/fix/perf since previous tag)"
PREV_TAG="$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || true)"
{
  echo "# Changelog for $VERSION"
  echo
  if [ -n "$PREV_TAG" ]; then
    git log "$PREV_TAG"..HEAD --pretty='format:%s' \
      | grep -E '^(feat|fix|perf)(\(.+\))?:' || echo "(no user-facing feat/fix/perf commits since $PREV_TAG)"
  else
    echo "(no previous tag found - full history not enumerated)"
  fi
} > "$STAGE_DIR/CHANGELOG.md"

if command -v syft >/dev/null 2>&1; then
  log "generating SBOM via syft"
  syft "dir:$STAGE_DIR/bin" -o spdx-json > "$STAGE_DIR/sbom.spdx.json"
else
  log "syft not found - skipping SBOM (docs/11 muc 8: 'syft hoac tuong duong', best-effort)"
fi

mkdir -p "$OUT_DIR"
TARBALL="$OUT_DIR/$RELEASE_NAME.tar.gz"
tar -C "$WORK_DIR" -czf "$TARBALL" "$RELEASE_NAME"
( cd "$OUT_DIR" && sha256sum "$(basename "$TARBALL")" > "$(basename "$TARBALL").sha256" )

log "release package: $TARBALL"
log "checksum:         $TARBALL.sha256"
