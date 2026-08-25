#!/usr/bin/env bash
# prepare-golden-template.sh — chuẩn bị Ubuntu 22.04 golden template
# theo docs/04_Ubuntu_2204_Golden_Template_Specification_v1.0.md.
#
# CHỈ chạy script này bên trong một VM Ubuntu 22.04 MỚI dựng riêng để
# làm template (Phần IV mục 7, bước 1-6), KHÔNG chạy trên server đang
# phục vụ thật — script này XOÁ /etc/machine-id, SSH host key và mọi
# state cloud-init/network hiện có (Phần IV mục 5), không thể hoàn tác.
#
# Sau khi script chạy xong và validator PASS: tắt máy ngay (KHÔNG
# reboot — Phần IV mục 7 bước 7 "Power off immediately", mục 11 liệt
# "không reboot template sau cleanup" là prohibited practice), rồi
# convert VM thành Proxmox template và đăng ký DRAFT qua `vmf template
# register` (P0-09, chưa triển khai) hoặc trực tiếp qua
# internal/template.Repository.Create.
#
# Usage:
#   sudo ./prepare-golden-template.sh --confirm-fresh-vm [--denylist-file /path/to/denylist.txt]
#
# --confirm-fresh-vm bắt buộc — an toàn tối thiểu để tránh chạy nhầm
# trên máy đang dùng thật.

set -euo pipefail

DENYLIST_FILE=""
CONFIRMED=0

while [ $# -gt 0 ]; do
  case "$1" in
    --confirm-fresh-vm)
      CONFIRMED=1
      shift
      ;;
    --denylist-file)
      DENYLIST_FILE="${2:-}"
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

if [ "$CONFIRMED" -ne 1 ]; then
  echo "Refusing to run: pass --confirm-fresh-vm to acknowledge this VM is a" >&2
  echo "fresh, disposable template-prep VM. Script destroys machine-id, SSH" >&2
  echo "host keys and cloud-init/network state irreversibly." >&2
  exit 1
fi

if [ "$(id -u)" -ne 0 ]; then
  echo "must run as root" >&2
  exit 1
fi

log() { echo "[prepare-golden-template] $*"; }

# ---------------------------------------------------------------------
# Phần IV mục 3: required packages
# ---------------------------------------------------------------------
log "installing baseline packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends \
  cloud-init \
  qemu-guest-agent \
  openssh-server \
  ca-certificates \
  curl \
  jq \
  chrony

# ---------------------------------------------------------------------
# Phần IV mục 4: hardening baseline
# ---------------------------------------------------------------------
log "applying hardening baseline"

# SSH: khong dang nhap bang password, khong cho root dang nhap qua SSH.
sshd_config="/etc/ssh/sshd_config"
sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' "$sshd_config"
sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin no/' "$sshd_config"
if ! grep -q '^PasswordAuthentication no' "$sshd_config"; then
  echo 'PasswordAuthentication no' >> "$sshd_config"
fi
if ! grep -q '^PermitRootLogin no' "$sshd_config"; then
  echo 'PermitRootLogin no' >> "$sshd_config"
fi

# Khoa mat khau root - truy cap chi qua authorized key/SSH certificate.
passwd -l root || true

# QGA va time sync phai enable (Phan IV muc 4, muc 8.1 kiem tra lai).
systemctl enable --now qemu-guest-agent
systemctl enable --now chrony

# Log rotation: dung logrotate mac dinh cua Ubuntu, khong sua them tru
# khi du an co baseline rieng - ghi nhan da co san qua package logrotate.
apt-get install -y --no-install-recommends logrotate

log "hardening applied — review sshd_config/services manually before proceeding if this VM has project-specific baseline requirements"

# ---------------------------------------------------------------------
# Phần IV mục 5: identity generalization (bước cuối cùng, không revert)
# ---------------------------------------------------------------------
log "running identity generalization — irreversible from this point"

# 5.1 Machine ID — preferred qua cloud-init clean --machine-id nếu hỗ
# trợ, fallback thủ công nếu phiên bản cloud-init không có cờ này.
if cloud-init clean --help 2>/dev/null | grep -q -- '--machine-id'; then
  cloud-init clean --logs --seed --machine-id
else
  cloud-init clean --logs --seed
  truncate -s 0 /etc/machine-id
  rm -f /var/lib/dbus/machine-id
  ln -s /etc/machine-id /var/lib/dbus/machine-id
fi

# 5.2 SSH host keys — first boot phải tạo lại.
rm -f /etc/ssh/ssh_host_*

# 5.3 Cloud-init state (cloud-init clean ở trên đã lo phần lớn; xoá
# tường minh thêm cho các đường dẫn tài liệu liệt kê, phòng phiên bản
# cloud-init khác nhau xử lý không đầy đủ).
rm -rf /var/lib/cloud/instances/*
rm -f /var/lib/cloud/instance

# 5.4 Network state — xoá lease cũ, template không mang IP/hostname
# của máy build.
rm -f /var/lib/dhcp/dhclient*.leases
rm -f /var/lib/NetworkManager/*.lease

# 5.5 Application state — denylist do workload adapter cung cấp,
# master template không biết tên workload cụ thể (Phần IV mục 5.5).
if [ -n "$DENYLIST_FILE" ]; then
  if [ ! -f "$DENYLIST_FILE" ]; then
    echo "denylist file not found: $DENYLIST_FILE" >&2
    exit 1
  fi
  log "checking application state denylist from $DENYLIST_FILE"
  while IFS= read -r path; do
    [ -z "$path" ] && continue
    if [ -e "$path" ]; then
      echo "application state present at denylisted path: $path" >&2
      exit 1
    fi
  done < "$DENYLIST_FILE"
fi

log "cleanup complete"

# ---------------------------------------------------------------------
# Phần IV mục 8.1: offline validator — chạy NGAY sau cleanup, KHÔNG
# reboot trước khi chạy (Phần IV mục 7, mục 11).
# ---------------------------------------------------------------------
validator_args=()
if [ -n "$DENYLIST_FILE" ]; then
  validator_args+=(--denylist-file "$DENYLIST_FILE")
fi
# set +e tam thoi: validator FAIL (exit != 0) la ket qua hop le can
# doc lai va bao cao, khong phai loi script - set -e o tren se thoat
# ngay truoc khi doc duoc $? neu khong tat no o day.
set +e
"$(dirname "$0")/validate-template-offline.sh" "${validator_args[@]}"
validator_status=$?
set -e

if [ "$validator_status" -eq 0 ]; then
  log "PASS — tắt máy ngay (KHÔNG reboot), sau đó convert VM này thành Proxmox template"
else
  log "FAIL — xem output validator ở trên, KHÔNG convert VM này thành template"
fi
exit "$validator_status"
