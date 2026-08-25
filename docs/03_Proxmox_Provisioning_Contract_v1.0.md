---
title: "VM Factory - Proxmox Provisioning Contract"
subtitle: "API boundary, asynchronous task handling, VM configuration, placement và recovery"
author: "VM Factory Engineering"
date: "2026-08-25"
lang: vi-VN
---

**Loại tài liệu:** External Integration Contract  
**Repository mục tiêu:** `vm-factory`  
**Phiên bản tài liệu:** 1.0  
**Trạng thái:** Dev-ready proposal  
**Ngày chốt:** 25/08/2026

> VM Factory & Fleet Control Plane là một control plane độc lập để tạo, cấu hình, xác minh, vận hành và thu hồi Linux VM trên Proxmox. PGW được tích hợp qua API như một dependency bên ngoài; VM Factory không chứa logic data-plane của PGW và không phụ thuộc vào bất kỳ workload cụ thể nào.


# 1. Mục đích

Tài liệu này định nghĩa cách VM Factory giao tiếp với Proxmox. Nó không khóa implementation vào một SDK cụ thể; adapter phải map contract domain sang REST API của phiên bản Proxmox đang triển khai và được kiểm chứng bằng API viewer của cluster thực tế.

# 2. Authentication and Transport

- HTTPS bắt buộc.
- Ưu tiên API token thay user/password session.
- Token có role riêng, scope theo pool/node/storage cần thiết.
- Không dùng token root toàn cluster trong steady state.
- CA validation bắt buộc; không `InsecureSkipVerify` production.
- Timeout riêng cho connect, request và overall task.

Config tham chiếu:

```yaml
proxmox:
  clusters:
    - id: pve-main
      base_url: https://pve.example:8006/api2/json
      token_id: vmfactory@pve!automation
      token_secret_file: /run/credentials/pve-token
      ca_file: /etc/vm-factory/pve-ca.pem
      request_timeout: 30s
      task_timeout: 15m
```

# 3. Resource Discovery

Adapter phải discover và cache có TTL:

- cluster/node status;
- VM inventory và template flag;
- storage availability/content type/free space;
- bridge/network existence;
- resource pool;
- next VMID;
- task status;
- guest agent availability.

Discovery không thay validation tại mutation time. Ví dụ bridge tồn tại trong cache nhưng đã bị xóa thì configure phải fail rõ.

# 4. Placement Policy

P0 hỗ trợ placement explicit hoặc simple scheduler:

```text
eligible nodes
→ node online
→ storage available
→ template accessible
→ resource headroom
→ lowest weighted load
```

Weights configurable:

```yaml
placement:
  cpu_weight: 0.4
  memory_weight: 0.3
  storage_weight: 0.2
  active_jobs_weight: 0.1
```

P0 không tự live-migrate để cân bằng. Placement decision được lưu trong job trước clone.

# 5. Clone Contract

## 5.1 Request

```json
{
  "cluster_id": "pve-main",
  "source_node": "pve01",
  "source_vmid": 9000,
  "target_node": "pve01",
  "target_vmid": 201,
  "name": "node-0001",
  "storage": "local-lvm",
  "mode": "full",
  "pool": "vmfactory"
}
```

## 5.2 Rules

- Template phải ở state `ACTIVE` trong VM Factory.
- Full clone mặc định.
- Clone response async task phải được lưu trước polling.
- Timeout không được hiểu là fail chắc chắn; phải query VM inventory và task history.
- VM description/tags phải chứa stable external reference:

```text
vmf.instance_id=ins_...
vmf.job_id=job_...
vmf.template_version=ubuntu-2204-v1
```

Nhờ vậy reconciler tìm được VM nếu process chết sau clone.

## 5.3 Terminal conditions

Task success chỉ được chấp nhận khi:

```text
task terminal success
+ VM object tồn tại
+ VMID/name/external tag đúng
+ disk volumes attached
```

# 6. Configure Contract

Desired config P0:

```json
{
  "cores": 1,
  "sockets": 1,
  "memory_mb": 1024,
  "balloon_mb": 0,
  "cpu_type": "host-or-approved-baseline",
  "scsi_controller": "virtio-scsi-pci",
  "disk": {"size_gb": 8, "discard": true, "ssd": true},
  "agent": true,
  "onboot": true,
  "startup_order": 30,
  "net0": {
    "model": "virtio",
    "bridge": "vmbr20",
    "mac": "generated-by-proxmox",
    "firewall": true,
    "ipv4": "10.20.0.11/24",
    "gateway": "10.20.0.1",
    "ipv6": "disabled"
  }
}
```

## 6.1 Config hash

VM Factory canonicalize desired config và tính SHA-256. Hash lưu DB và VM description. Configure retry đọc observed config; nếu hash/fields đã đúng thì step success idempotently.

## 6.2 NIC invariant

P0 chỉ cho một NIC workload. Nếu template hoặc VM observed có NIC ngoài allowlist, validation fail và quarantine.

# 7. Cloud-init Contract

Cloud-init fields:

```text
ciuser
sshkeys
ipconfig0
nameserver
searchdomain
cicustom (optional snippets)
```

Không dùng long-lived password. User data phải:

- set hostname;
- đảm bảo QEMU guest agent enabled;
- apply IPv6 deny guest-level nếu policy yêu cầu;
- install minimal bootstrap dependencies;
- write one-time bootstrap marker;
- không chứa application-specific secret dài hạn.

VM Factory phải lưu hash của rendered cloud-init, không lưu secret plaintext vào audit.

# 8. Start and Guest Readiness

## 8.1 Boot order

```text
PGW client/mapping created
→ mapping active and reconciled
→ VM start
```

## 8.2 QGA readiness

Polling:

```text
VM status == running
→ guest-ping success
→ network interfaces reported
→ cloud-init status --wait completed
```

Timeout là policy theo template, mặc định 5 phút. Không dùng `sleep 60` rồi giả định ready.

## 8.3 SSH fallback

Chỉ dùng khi:

- QGA không available nhưng VM network/SSH reachable;
- template policy cho phép;
- host key được verify theo bootstrap mechanism.

# 9. Stop/Delete Contract

## 9.1 Graceful stop

- guest shutdown qua Proxmox/QGA;
- poll status;
- force stop chỉ sau timeout và audit reason.

## 9.2 Delete

Delete success khi:

```text
VM object absent
+ volumes absent hoặc được inventory là retained theo policy
+ Proxmox lock/backup/HA references đã xử lý
```

Not-found được coi là success idempotent sau khi verify external references không trỏ sang VM khác.

# 10. Task Polling

Backoff mẫu:

```text
1s, 2s, 3s, 5s, 8s, sau đó 10s có jitter
```

Không poll quá nhanh. Mỗi task có:

```text
upid/reference
started_at
last_polled_at
status
exit_status
raw_error_redacted
```

# 11. Error Mapping

| Proxmox condition | VM Factory error |
|---|---|
| 401/403 | `PVE_AUTH_FAILED` |
| target VMID exists | `PVE_VMID_CONFLICT` |
| bridge missing | `PVE_BRIDGE_NOT_FOUND` |
| storage full | `PVE_STORAGE_CAPACITY` |
| task timeout unknown | `PVE_TASK_UNKNOWN` |
| VM locked | `PVE_VM_LOCKED` |
| template invalid | `PVE_TEMPLATE_INVALID` |
| guest agent unavailable | `GUEST_AGENT_UNAVAILABLE` |

# 12. Reconciliation Cases

## Worker crash after clone request

```text
DB has intent but no task ref
→ search VM by vmf.instance_id tag
→ if found, attach external ref and continue
→ if not found, query recent tasks
→ only then retry clone
```

## Task timeout but VM exists

```text
observed VM matches desired external tag
→ verify config
→ mark clone step recovered
```

## VM exists but wrong tag/config

Không chiếm đoạt VM. Quarantine job, alert operator.

# 13. Permission Matrix

Role automation tối thiểu cần được kiểm chứng trên cluster thực tế, nhưng nguyên tắc:

- read cluster/nodes/storage/VM;
- allocate/clone/configure/start/stop/delete VM trong pool được chỉ định;
- read task status;
- guest agent commands cần thiết;
- không quản lý user/realm/cluster config;
- không thay network bridge hoặc storage config.

# 14. Contract Tests

- Auth valid/invalid.
- Discover nodes/storage/templates.
- Full clone success.
- VMID conflict.
- Clone timeout recovery.
- Configure idempotency.
- Start already-running.
- QGA unavailable fallback.
- Delete already-absent.
- Permission denied maps đúng error.
- API version drift phát hiện ở startup self-check.
