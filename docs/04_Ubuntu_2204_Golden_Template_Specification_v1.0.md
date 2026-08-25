---
title: "Ubuntu 22.04 Golden Template Specification"
subtitle: "Build manifest, generalization, first-boot identity, validation và promotion"
author: "VM Factory Engineering"
date: "2026-08-25"
lang: vi-VN
---

**Loại tài liệu:** Image/Template Specification  
**Repository mục tiêu:** `vm-factory`  
**Phiên bản tài liệu:** 1.0  
**Trạng thái:** Dev-ready proposal  
**Ngày chốt:** 25/08/2026

> VM Factory & Fleet Control Plane là một control plane độc lập để tạo, cấu hình, xác minh, vận hành và thu hồi Linux VM trên Proxmox. PGW được tích hợp qua API như một dependency bên ngoài; VM Factory không chứa logic data-plane của PGW và không phụ thuộc vào bất kỳ workload cụ thể nào.


# 1. Mục tiêu

Golden template phải là image phần mềm tái lập được nhưng **không mang identity của một installation đã sử dụng**. Mọi clone phải sinh identity riêng ở lần boot đầu.

# 2. Baseline

```text
OS: Ubuntu Server 22.04 LTS
Architecture: amd64
Image source: official cloud image hoặc ISO build được kiểm soát
Init: systemd
Provisioning: cloud-init
Guest integration: qemu-guest-agent
Network: one VirtIO NIC
Disk: VirtIO SCSI
GUI: none
```

Ubuntu 24.04 chỉ được thêm như template version khác sau canary; không silently thay baseline P0.

# 3. Required Packages

Tối thiểu:

```text
cloud-init
qemu-guest-agent
openssh-server
ca-certificates
curl
jq
chrony hoặc systemd-timesyncd
```

Optional theo policy:

```text
node_exporter
rsyslog/vector agent
unattended-upgrades
```

Không cài workload cụ thể vào template P0.

# 4. Hardening Baseline

- SSH password login off.
- Root password locked; access bằng authorized key/SSH certificate.
- UFW/nft policy theo baseline dự án, không xung đột network gateway.
- Disable unnecessary services.
- Time sync enabled.
- Log rotation configured.
- Package repository chính thức.
- QGA service enabled.
- Console không in secret cloud-init.

# 5. Identity Generalization

## 5.1 Machine ID

Template final phải có `/etc/machine-id` rỗng, không chứa ID hợp lệ. `/var/lib/dbus/machine-id` phải là symlink tới `/etc/machine-id` hoặc không chứa ID cũ.

Preferred nếu cloud-init hỗ trợ:

```bash
cloud-init clean --logs --seed --machine-id
```

Fallback kiểm soát:

```bash
cloud-init clean --logs --seed
truncate -s 0 /etc/machine-id
rm -f /var/lib/dbus/machine-id
ln -s /etc/machine-id /var/lib/dbus/machine-id
```

Không chạy `systemd-machine-id-setup` trên template trước convert; việc đó sẽ tạo ID rồi bị clone.

## 5.2 SSH host keys

```bash
rm -f /etc/ssh/ssh_host_*
```

Cloud-init/first boot phải tạo lại. Validation sau clone thu fingerprint và kiểm tra uniqueness.

## 5.3 Cloud-init state

Xóa:

```text
/var/lib/cloud/instances/*
/var/lib/cloud/instance
cached datasource state
seed-specific instance-id
```

Sử dụng `cloud-init clean`, không xóa mù mọi thư mục nếu phiên bản image có behavior khác; validate bằng boot clone test.

## 5.4 Network state

Xóa lease cũ:

```bash
rm -f /var/lib/dhcp/dhclient*.leases
rm -f /var/lib/NetworkManager/*.lease
```

Template không chứa static IP/hostname của máy build.

## 5.5 Application state

Validator phải fail nếu tồn tại các path được khai báo trong denylist, ví dụ:

```text
/etc/<workload>
/var/lib/<workload>
/opt/<workload>/state
```

Denylist do workload adapter cung cấp; master template không biết tên workload.

# 6. Build Manifest

Mỗi template version có manifest:

```json
{
  "name": "ubuntu-2204",
  "version": "2026.08.1",
  "source_image": "ubuntu-22.04-server-cloudimg-amd64.img",
  "source_sha256": "...",
  "packages": ["cloud-init", "qemu-guest-agent", "openssh-server"],
  "kernel": "observed-at-build",
  "builder_commit": "git-sha",
  "built_at": "2026-08-25T00:00:00Z",
  "validation_suite": "template-v1",
  "pve_template_vmid": 9000
}
```

# 7. Template Preparation Procedure

```text
1. Import official image / build VM
2. Install packages and baseline config
3. Update packages under change control
4. Enable QGA and cloud-init datasource
5. Run vulnerability/config scans
6. Run cleanup/generalization
7. Power off immediately
8. Convert to Proxmox template
9. Register DRAFT version in VM Factory
10. Clone canary and run validation suite
11. Promote CANDIDATE → ACTIVE
```

Không reboot template sau cleanup.

# 8. Validation Suite

## 8.1 Offline checks trước convert

```bash
[ ! -s /etc/machine-id ]
[ -L /var/lib/dbus/machine-id ]
! compgen -G '/etc/ssh/ssh_host_*' >/dev/null
cloud-init clean --help >/dev/null
systemctl is-enabled qemu-guest-agent
```

## 8.2 Canary clone checks

- machine-id 32 hex và khác template/clone khác;
- SSH host key mới;
- cloud-init instance ID riêng;
- hostname/IP theo metadata;
- QGA ping;
- one NIC;
- one IPv4 default route;
- no IPv6 default route;
- time sync;
- package baseline;
- no application state;
- reboot giữ machine-id ổn định;
- second clone không trùng identity.

# 9. Versioning and Promotion

States:

```text
DRAFT → CANDIDATE → ACTIVE → DEPRECATED → REVOKED
```

Rules:

- một template family có một `ACTIVE` default, nhưng job có thể pin version;
- không sửa content của version đã active;
- security fix tạo version mới;
- `REVOKED` ngăn job mới và kích hoạt impact report cho instance đang dùng;
- rollback là promote version trước, không mutate version lỗi.

# 10. Template Validator Output

```json
{
  "template_version": "ubuntu-2204-2026.08.1",
  "passed": true,
  "checks": [
    {"name":"machine_id_empty","result":"PASS"},
    {"name":"ssh_host_keys_absent","result":"PASS"},
    {"name":"cloud_init_clean","result":"PASS"},
    {"name":"qga_enabled","result":"PASS"},
    {"name":"application_state_absent","result":"PASS"}
  ]
}
```

# 11. Prohibited Practices

- Clone template đang running.
- Chụp snapshot sau khi workload đã đăng ký state rồi dùng làm base.
- Để machine-id/SSH host keys trong template.
- Hard-code IP, DNS, hostname hoặc credential.
- Cài auto-update không kiểm soát làm image drift ngay khi boot.
- Sửa trực tiếp template active mà không bump version.
- Dùng một template không có build manifest/checksum.

# 12. Definition of Done

Một template được `ACTIVE` khi:

```text
build reproducible enough để audit
+ offline validator PASS
+ ít nhất hai canary clone PASS uniqueness
+ network/QGA/cloud-init PASS
+ reboot PASS
+ manifest và checksum được lưu
```
