#!/usr/bin/env bash
# restore.sh — script hoa PHAN CO THE TU DONG cua Restore procedure
# (docs/10 muc 8, muc 9): buoc 1 (restore DB vao moi truong CO LAP) va
# phan may mac cua buoc 3 (schema o dung version, khong dirty). CAC
# BUOC CON LAI BAT BUOC THAO TAC THU CONG, khong duoc tu dong hoa an
# toan trong script nay:
#   2. Start API read-only, workers disabled — deploy topology/config
#      quyet dinh cua operator, khong phai thu tu lenh.
#   4. Inventory external systems (Proxmox/PGW that con gi) — can nguoi
#      doi chieu, chua co discovery API tu dong (Phan III muc 12, gap
#      da biet).
#   5. Produce drift report — can orphan/drift scanner (Epic P0-11),
#      chua trien khai.
#   6. Operator approve reconciliation scope — quyet dinh con nguoi,
#      co chu dinh KHONG script hoa.
#   7. Enable workers gradually — van hanh thu cong theo Wave Plan
#      (docs/10 muc 11).
#
# An toan: BAT BUOC --confirm-isolated-environment — script KHONG tu
# xac minh duoc DSN truyen vao thuc su tro toi mot moi truong co lap
# (do la quyet dinh ha tang cua nguoi goi script), chi buoc nguoi van
# hanh xac nhan tuong minh truoc khi ghi de DB tai dich.
#
# Usage:
#   scripts/restore.sh <dump-file> --dsn-file PATH --confirm-isolated-environment [--migrations-dir PATH]

set -euo pipefail

DUMP_FILE=""
DSN_FILE=""
MIGRATIONS_DIR="$(cd "$(dirname "$0")/.." && pwd)/migrations"
CONFIRMED=0

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
    --confirm-isolated-environment)
      CONFIRMED=1
      shift
      ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^#//'
      exit 0
      ;;
    *)
      if [ -n "$DUMP_FILE" ]; then
        echo "unexpected extra argument: $1" >&2
        exit 2
      fi
      DUMP_FILE="$1"
      shift
      ;;
  esac
done

log() { echo "[restore] $*"; }

if [ -z "$DUMP_FILE" ] || [ ! -f "$DUMP_FILE" ]; then
  echo "usage: restore.sh <dump-file> --dsn-file PATH --confirm-isolated-environment — dump not found: ${DUMP_FILE:-<missing>}" >&2
  exit 1
fi
if [ -z "$DSN_FILE" ] || [ ! -f "$DSN_FILE" ]; then
  echo "--dsn-file is required and must point to a readable DSN file" >&2
  exit 1
fi
if [ "$CONFIRMED" -ne 1 ]; then
  echo "Refusing to run: pass --confirm-isolated-environment to acknowledge the DSN" >&2
  echo "in $DSN_FILE points to an ISOLATED restore environment, not production." >&2
  exit 1
fi
if ! command -v pg_restore >/dev/null 2>&1; then
  echo "pg_restore not found in PATH" >&2
  exit 1
fi

DSN="$(tr -d '[:space:]' < "$DSN_FILE")"

log "step 1/7 (scripted): pg_restore $DUMP_FILE into target DSN"
pg_restore --clean --if-exists --no-owner --dbname="$DSN" "$DUMP_FILE"

log "step 3/7 (scripted phan may mac): kiem tra migration schema version"
"$(dirname "$0")/migrate.sh" status --dsn-file "$DSN_FILE" --migrations-dir "$MIGRATIONS_DIR" || {
  echo "migrate status bao loi/dirty — XU LY TRUOC KHI TIEP TUC, KHONG bo qua." >&2
  exit 1
}

cat <<'EOF' >&2

[restore] Buoc con lai BAT BUOC thao tac thu cong (docs/10 muc 8):
  2. Start vmf-api read-only, vmf-worker DISABLED (systemctl mask/stop vmf-worker).
  3. Chay day du acceptance/invariant test (khong chi migrate status) truoc khi tin DB nay.
  4. Doi chieu inventory Proxmox/PGW that con nhung gi (khong co discovery API tu dong).
  5. Ghi drift report thu cong (chua co orphan scanner - P0-11).
  6. Operator PHE DUYET pham vi reconciliation truoc khi lam gi tiep.
  7. Bat vmf-worker tro lai TUNG BUOC theo Wave Plan (docs/10 muc 11), khong bat toan bo cung luc.
EOF
