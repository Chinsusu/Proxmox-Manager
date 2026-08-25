#!/usr/bin/env bash
# validate-template-offline.sh — offline validator checks theo
# docs/04_Ubuntu_2204_Golden_Template_Specification_v1.0.md mục 8.1,
# output JSON theo format mục 10. Chạy TRÊN guest, ngay trước khi tắt
# máy để convert thành Proxmox template (không dùng jq để tránh phụ
# thuộc cứng — script này phải tự chạy được kể cả khi gọi độc lập
# trước khi prepare-golden-template.sh cài đặt xong package).
#
# Usage:
#   ./validate-template-offline.sh [--template-version NAME] [--denylist-file /path]
#
# Exit 0 nếu mọi check BLOCK-level đều PASS, exit 1 nếu có FAIL.

set -uo pipefail

TEMPLATE_VERSION="unknown"
DENYLIST_FILE=""

while [ $# -gt 0 ]; do
  case "$1" in
    --template-version)
      TEMPLATE_VERSION="${2:-unknown}"
      shift 2
      ;;
    --denylist-file)
      DENYLIST_FILE="${2:-}"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

checks_json=""
overall_pass=1

# add_check ghi một check vào checks_json, cập nhật overall_pass.
# $1=name $2=0-hoac-1 (0=pass giống exit code shell)
add_check() {
  name="$1"
  ok="$2"
  if [ "$ok" -eq 0 ]; then
    result="PASS"
  else
    result="FAIL"
    overall_pass=0
  fi
  entry="{\"name\":\"${name}\",\"result\":\"${result}\"}"
  if [ -z "$checks_json" ]; then
    checks_json="$entry"
  else
    checks_json="${checks_json},${entry}"
  fi
  printf '%-32s %s\n' "$name" "$result" >&2
}

# ID-... theo Phan IV muc 8.1, ten check khop Phan IV muc 10.

[ ! -s /etc/machine-id ]
add_check "machine_id_empty" $?

[ -L /var/lib/dbus/machine-id ]
add_check "dbus_machine_id_symlink" $?

! compgen -G '/etc/ssh/ssh_host_*' >/dev/null 2>&1
add_check "ssh_host_keys_absent" $?

command -v cloud-init >/dev/null 2>&1 && cloud-init clean --help >/dev/null 2>&1
add_check "cloud_init_clean" $?

systemctl is-enabled qemu-guest-agent >/dev/null 2>&1
add_check "qga_enabled" $?

if [ -n "$DENYLIST_FILE" ] && [ -f "$DENYLIST_FILE" ]; then
  app_state_found=0
  while IFS= read -r path; do
    [ -z "$path" ] && continue
    if [ -e "$path" ]; then
      app_state_found=1
    fi
  done < "$DENYLIST_FILE"
  add_check "application_state_absent" "$app_state_found"
fi

passed="false"
[ "$overall_pass" -eq 1 ] && passed="true"

printf '{"template_version":"%s","passed":%s,"checks":[%s]}\n' \
  "$TEMPLATE_VERSION" "$passed" "$checks_json"

[ "$overall_pass" -eq 1 ]
