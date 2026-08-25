---
title: "Identity, Network & Egress Validation Specification"
subtitle: "Rules, evidence, severity, quarantine policy và validation engine"
author: "VM Factory Engineering"
date: "2026-08-25"
lang: vi-VN
---

**Loại tài liệu:** Validation Specification  
**Repository mục tiêu:** `vm-factory`  
**Phiên bản tài liệu:** 1.0  
**Trạng thái:** Dev-ready proposal  
**Ngày chốt:** 25/08/2026

> VM Factory & Fleet Control Plane là một control plane độc lập để tạo, cấu hình, xác minh, vận hành và thu hồi Linux VM trên Proxmox. PGW được tích hợp qua API như một dependency bên ngoài; VM Factory không chứa logic data-plane của PGW và không phụ thuộc vào bất kỳ workload cụ thể nào.


# 1. Validation Philosophy

Validation phải tạo **evidence có thể audit**, không chỉ trả `true/false`. Mỗi rule có:

```text
rule_id
version
severity
expected
observed
result
collected_at
collector
```

Ruleset được version hóa để biết instance READY theo tiêu chuẩn nào.

# 2. Validation Stages

```text
Template validation (offline/canary)
→ Guest identity validation
→ Network validation
→ Egress validation
→ Workload validation
→ Periodic drift validation
```

# 3. Identity Facts

Guest facts P0:

```text
/etc/machine-id
/proc/sys/kernel/random/boot_id
hostname
cloud-init instance-id
SSH host public key fingerprints
MAC addresses
IPv4 addresses
OS release
kernel version
QGA reported interfaces
```

Raw machine-id chỉ tồn tại trong memory collector; gửi về API dạng HMAC digest.

# 4. Identity Rules

| Rule | Severity | PASS |
|---|---|---|
| ID-001 machine-id format | BLOCK | 32 lowercase hex |
| ID-002 machine-id uniqueness | BLOCK | digest chưa thuộc instance khác |
| ID-003 SSH host key uniqueness | BLOCK | fingerprint mới |
| ID-004 hostname match | BLOCK | đúng inventory |
| ID-005 MAC match | BLOCK | đúng Proxmox/PGW |
| ID-006 cloud-init instance ID | WARN/BLOCK policy | đúng generation |
| ID-007 stale application state | BLOCK | denylist absent |
| ID-008 boot ID present | WARN | valid UUID |

# 5. Network Rules

| Rule | Severity | PASS |
|---|---|---|
| NET-001 NIC count | BLOCK | đúng một workload NIC |
| NET-002 IPv4 address | BLOCK | đúng allocated IP |
| NET-003 default route | BLOCK | duy nhất qua expected gateway |
| NET-004 IPv6 default route | BLOCK | absent khi policy deny |
| NET-005 global IPv6 | BLOCK/WARN | absent khi policy deny |
| NET-006 DNS | WARN/BLOCK | expected resolver policy |
| NET-007 unexpected tunnel interface | WARN | none unless policy allows |
| NET-008 PGW client identity | BLOCK | IP/MAC/VLAN match |
| NET-009 second route/NIC | BLOCK | absent |

# 6. Egress Rules

Nguồn evidence chính là PGW egress proof, cộng kiểm tra guest bounded:

| Rule | Severity | PASS |
|---|---|---|
| EGR-001 mapping active | BLOCK | correct mapping active |
| EGR-002 generation applied | BLOCK | desired==applied |
| EGR-003 IPv4 exit | BLOCK | matches proof/policy |
| EGR-004 IPv6 | BLOCK | blocked when deny |
| EGR-005 direct leak counter | BLOCK | zero |
| EGR-006 proxy health | BLOCK/WARN | ACTIVE or policy threshold |
| EGR-007 proof freshness | BLOCK | within configured age |

# 7. Workload Rules

Workload adapter cung cấp:

```text
artifact version/checksum
install marker
service state
health endpoint/command
bounded log evidence
state-path denylist for template validation
```

VM Factory không hiểu business semantics của workload; adapter map về contract chung.

# 8. Results

```text
PASS: all BLOCK rules pass
WARN: no BLOCK failure, one or more WARN
FAIL: at least one BLOCK rule fails
UNKNOWN: collector/error prevents conclusion
```

`UNKNOWN` ở identity/network/egress không được chuyển READY.

# 9. Quarantine Actions

BLOCK failure sau boot:

```text
persist evidence
→ suspend PGW mapping
→ optional stop VM based on rule
→ state QUARANTINED
→ alert with remediation hint
```

Không tự đổi identity hoặc route để làm validation pass nếu không có repair policy được phê duyệt.

# 10. Duplicate Detection

Uniqueness scope:

```text
active fleet: hard block
retired history: alert/block configurable
same logical instance rebuild generation: old digest reuse vẫn phải giải thích; mặc định block
```

Duplicate investigation hiển thị:

- first/last seen;
- instance IDs/generations;
- template version;
- Proxmox node/VMID;
- job correlation.

# 11. Evidence Example

```json
{
  "ruleset_version": "identity-network-egress-1.0",
  "instance_id": "ins_...",
  "result": "PASS",
  "checks": [
    {
      "rule_id": "ID-002",
      "result": "PASS",
      "expected": "unique",
      "observed": "hmac-sha256:..."
    },
    {
      "rule_id": "NET-004",
      "result": "PASS",
      "expected": "no IPv6 default route",
      "observed": []
    },
    {
      "rule_id": "EGR-005",
      "result": "PASS",
      "expected": 0,
      "observed": 0
    }
  ]
}
```

# 12. Periodic Drift Validation

READY instance được kiểm tra định kỳ:

```text
PGW binding/proof
Proxmox VM config hash
NIC/route facts
workload health
identity digest stability
```

Drift classification:

- benign observation change;
- expected update;
- repairable drift;
- quarantine-worthy drift.

# 13. Test Cases

- clean clone PASS;
- duplicate machine-id FAIL;
- duplicate SSH key FAIL;
- wrong hostname FAIL;
- second NIC FAIL;
- second default route FAIL;
- IPv6 route FAIL;
- PGW mapping mismatch FAIL;
- proof stale UNKNOWN/FAIL;
- workload health fail;
- collector timeout UNKNOWN;
- evidence redaction.
