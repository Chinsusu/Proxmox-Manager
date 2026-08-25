---
title: "VM Factory - PGW Integration Contract"
subtitle: "Boundary giữa VM lifecycle và external egress gateway"
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


# 1. Boundary

PGW là repository và service độc lập. VM Factory chỉ quản lý reference/lifecycle của client, mapping và proof qua public/service API. VM Factory không:

- render/apply nftables;
- giữ upstream proxy credential;
- quyết định protocol capability nội bộ;
- truy cập PGW database trực tiếp;
- gọi shell trên PGW host.

# 2. Required PGW Operations

P0 dependency:

```text
POST   /v2/clients
GET    /v2/clients/{id}
DELETE /v2/clients/{id}
POST   /v2/clients/{id}/verify-identity
GET    /v2/clients/{id}/egress-proof

POST   /v2/mappings
GET    /v2/mappings/{id}
POST   /v2/mappings/{id}/activate
POST   /v2/mappings/{id}/suspend
POST   /v2/mappings/{id}/verify
DELETE /v2/mappings/{id}
```

Nếu PGW implementation thực tế khác, adapter map endpoint nhưng domain contract giữ nguyên.

# 3. Client Create Request

```json
{
  "name": "node-0001",
  "ip_cidr": "10.20.0.11/32",
  "mac_address": "BC:24:11:AA:BB:CC",
  "vlan_id": 101,
  "enabled": true,
  "metadata": {
    "vmf_instance_id": "ins_...",
    "proxmox_vmid": 201
  }
}
```

VM Factory lưu `pgw_client_id` ngay sau response. Request phải có idempotency key nếu PGW hỗ trợ; nếu không, name/metadata deterministic và list/search reconciliation bắt buộc.

# 4. Mapping Lifecycle

```text
create SUSPENDED
→ validate compatibility
→ activate
→ wait desired generation applied
→ boot guest
```

Không tạo mapping ACTIVE sau khi guest đã boot, vì first-boot traffic có thể đi khi network policy chưa sẵn sàng.

# 5. Egress Proof

Một proof tối thiểu:

```json
{
  "client_id": "cli_...",
  "mapping_id": "map_...",
  "result": "PASS",
  "checked_at": "...",
  "ipv4": "observed-exit-ip",
  "ipv6": "BLOCKED",
  "policy": "web_only",
  "direct_leak_packets": 0,
  "proxy_health": "ACTIVE",
  "rules_generation": 42
}
```

VM Factory lưu evidence snapshot và timestamp, không chỉ boolean.

# 6. Failure Semantics

| Condition | VM Factory behavior |
|---|---|
| PGW API unavailable trước boot | retry, không boot |
| create client timeout | reconcile by metadata/name |
| mapping incompatible | fail before clone hoặc rollback |
| activation pending | wait generation with timeout |
| egress proof fail | quarantine, suspend mapping |
| delete not found | success after verify no orphan |
| PGW auth failure | pause jobs, alert |

# 7. Decommission Order

```text
stop/drain workload
→ stop VM
→ suspend mapping
→ delete mapping
→ delete client (policy)
→ release IP
→ delete VM / retire record
```

Nếu cần giữ client lịch sử, PGW client chuyển disabled thay vì delete; policy phải explicit.

# 8. Reconciliation

Periodic scanner so sánh:

```text
DB active binding ↔ PGW client/mapping exists
DB instance IP/MAC ↔ PGW client identity
READY ↔ mapping active + recent proof
RETIRED ↔ no active mapping
PGW metadata vmf_instance_id ↔ DB record
```

# 9. Security

- Scoped service credential.
- TLS verify.
- Secret không log.
- Request/response redaction.
- Correlation ID truyền qua header.
- API rate limit/backoff.
- No direct DB integration.

# 10. Contract Tests

- create client/mapping idempotent;
- activate and generation wait;
- identity mismatch;
- egress proof PASS/FAIL;
- PGW restart while mapping active;
- auth failure;
- delete already absent;
- stale mapping orphan detection;
- MAC/IP update requires explicit workflow.
