---
title: "VM Factory - Project Charter & Product Requirements"
subtitle: "Mô tả dự án, mục tiêu, phạm vi, yêu cầu sản phẩm và tiêu chuẩn thành công"
author: "VM Factory Engineering"
date: "2026-08-25"
lang: vi-VN
---

**Loại tài liệu:** Project Charter + PRD  
**Repository mục tiêu:** `vm-factory`  
**Phiên bản tài liệu:** 1.0  
**Trạng thái:** Dev-ready proposal  
**Ngày chốt:** 25/08/2026

> VM Factory & Fleet Control Plane là một control plane độc lập để tạo, cấu hình, xác minh, vận hành và thu hồi Linux VM trên Proxmox. PGW được tích hợp qua API như một dependency bên ngoài; VM Factory không chứa logic data-plane của PGW và không phụ thuộc vào bất kỳ workload cụ thể nào.


# 1. Executive Summary

VM Factory giải quyết một vấn đề rất cụ thể: biến quá trình tạo một Linux VM sạch trên Proxmox từ chuỗi thao tác thủ công thành một **dây chuyền có trạng thái, có bằng chứng, có khả năng retry và rollback**. Hệ thống không chỉ gọi API `clone`; nó quản lý toàn bộ vòng đời từ lúc đặt chỗ tài nguyên đến khi instance được xác minh là duy nhất, có đúng cấu hình mạng, đi qua đúng egress policy, cài workload thành công và được ghi nhận vào inventory.

Đầu ra của dự án không phải một tập script rời rạc. Đầu ra là một control plane có source of truth, state machine và contract rõ giữa các hệ:

```text
Request tạo instance
→ reserve tài nguyên
→ clone/configure trên Proxmox
→ ràng buộc network/egress
→ boot lần đầu
→ xác minh identity
→ xác minh network
→ áp workload adapter
→ READY hoặc rollback có kiểm soát
```

![Kiến trúc tổng quan](assets/architecture.png){width=95%}

# 2. Vấn đề cần giải quyết

## 2.1 Hiện trạng phổ biến

Khi provisioning được thực hiện bằng GUI, shell script hoặc nhiều script rời rạc, các lỗi sau xuất hiện rất nhanh:

- VMID, IP, hostname hoặc MAC bị trùng.
- Clone thành công nhưng network mapping chưa sẵn sàng.
- Worker chết giữa chừng, lần chạy lại tạo thêm VM thứ hai.
- Template chứa state cũ: machine identity, SSH host key, cloud-init cache hoặc application data.
- Một bước thất bại nhưng các tài nguyên đã đặt chỗ không được thu hồi.
- Không biết VM đang ở bước nào; operator chỉ thấy "đang chạy" hoặc "lỗi".
- Không có bằng chứng rằng instance đi đúng egress, chỉ có một lần kiểm tra thủ công.
- Xóa VM trực tiếp trên Proxmox để lại IP, mapping, credential và inventory mồ côi.
- Không có audit trail để trả lời ai đã tạo, sửa, rebuild hoặc retire một instance.

## 2.2 Root cause

Root cause không phải Proxmox thiếu API. Root cause là **thiếu orchestration contract** giữa hypervisor, network gateway, guest OS và inventory. API của từng hệ chỉ cung cấp primitive; VM Factory phải chịu trách nhiệm ghép primitive thành transaction nghiệp vụ có thể phục hồi.

# 3. Tầm nhìn sản phẩm

> Một yêu cầu có cấu trúc phải tạo ra một Linux VM sạch, có identity duy nhất, network đúng, evidence đầy đủ và trạng thái READY; mọi thất bại phải có checkpoint, retry policy và đường rollback xác định trước.

## 3.1 North-star workflow

Operator hoặc hệ thống gọi:

```http
POST /v1/instances
Idempotency-Key: create-node-2026-000001
```

VM Factory trả `202 Accepted` cùng `job_id`. Từ đó operator chỉ theo dõi state machine; không phải đăng nhập Proxmox, PGW hoặc guest để nối tay các bước.

## 3.2 Giá trị mang lại

| Giá trị | Trước VM Factory | Sau VM Factory |
|---|---|---|
| Tốc độ | Từng bước thủ công | Một request có state |
| Tính lặp lại | Phụ thuộc người làm | Template + contract + validation |
| Chống trùng | Kiểm tra bằng mắt | Unique constraint + identity registry |
| Khả năng retry | Chạy lại dễ nhân đôi | Idempotent checkpoint |
| Khả năng rollback | Xóa tay từng nơi | Compensating actions có thứ tự |
| Audit | Rời rạc | Append-only audit |
| Scale | Vài VM | Batch provisioning có rate limit |
| Vận hành | Phản ứng sau lỗi | Health, alert, quarantine, rebuild |

# 4. Mục tiêu và tiêu chuẩn thành công

## 4.1 Mục tiêu P0

1. Tạo được instance từ Ubuntu 22.04 golden template bằng Proxmox API.
2. Reserve duy nhất VMID, IPv4, hostname và network binding.
3. Tạo và activate PGW client/mapping trước lần boot đầu của guest.
4. Chờ Proxmox task và guest readiness bằng cơ chế có timeout.
5. Xác minh machine identity, SSH host key, route, IPv6 policy và egress proof.
6. Áp một workload adapter mẫu theo contract generic.
7. Đưa instance vào `READY`, hoặc rollback sạch về `FAILED`/`RETIRED`.
8. API và CLI hỗ trợ create, get status, retry, quarantine, rebuild và decommission.
9. Mọi mutation có idempotency key, audit event và correlation ID.
10. Có test lab, acceptance matrix và runbook vận hành.

## 4.2 SLO khởi điểm

Các ngưỡng dưới đây là target ban đầu; pilot phải đo baseline thật rồi chốt lại:

| SLO | Target P0 |
|---|---:|
| Provisioning success lần đầu | >= 95% |
| Provisioning success sau retry tự động | >= 99% |
| P95 request-to-READY trên local NVMe | <= 10 phút |
| Duplicate VMID/IP/MAC/machine identity | 0 |
| Resource leak sau rollback | 0 |
| Mutation có audit event | 100% |
| Job có correlation ID | 100% |
| Instance READY có validation evidence | 100% |
| Manual intervention ở steady state | < 5% job |

## 4.3 Success gate trước khi scale

Không mở wave 10+ instance nếu còn một trong các lỗi:

```text
duplicate identity
unreleased IP/VMID/mapping
fail-open network
job retry tạo resource mới
operator phải sửa DB thủ công
rebuild không thể hoàn tất bằng workflow chuẩn
```

# 5. Phạm vi

## 5.1 In scope

- Proxmox QEMU VM, không dùng LXC trong P0.
- Ubuntu 22.04 LTS cloud image và template versioning.
- Full clone mặc định; linked clone là option cho lab.
- Một Proxmox cluster hoặc node độc lập ở P0.
- Một PGW deployment được tích hợp qua API.
- PostgreSQL làm source of truth.
- Go services: API, worker và CLI.
- cloud-init, QEMU Guest Agent và SSH fallback.
- IPAM nội bộ cho một hoặc nhiều IPv4 segment.
- Identity/network/egress validation.
- Generic workload adapter.
- Prometheus metrics, structured logs và alert rules.
- Lifecycle: create, retry, quarantine, rebuild, drain, decommission.

## 5.2 Out of scope P0

- Không thay thế Proxmox UI hoặc Proxmox cluster manager.
- Không triển khai SDN tổng quát.
- Không quản trị bare metal.
- Không tự quản lý upstream proxy inventory bên trong VM Factory.
- Không chứa nftables/data-plane logic của PGW.
- Không hỗ trợ Windows hoặc Android guest ở P0.
- Không xây CMDB doanh nghiệp đầy đủ.
- Không có arbitrary remote shell từ control plane.
- Không sửa hoặc giả mạo hardware/system identity; chỉ đảm bảo uniqueness và lifecycle correctness.
- Không HA đa datacenter trong P0.

# 6. Persona và use cases

## 6.1 Platform Operator

Cần:

- tạo một hoặc nhiều instance theo template/policy;
- xem tiến độ theo state;
- retry bước lỗi mà không tạo trùng;
- quarantine instance có validation fail;
- rebuild instance mà không rò tài nguyên;
- retire instance theo quy trình;
- xem evidence và audit.

## 6.2 Infrastructure Engineer

Cần:

- đăng ký Proxmox cluster, storage, bridge và template;
- quản lý resource pool và capacity;
- theo dõi clone latency, storage IOPS và task backlog;
- rotate credential;
- kiểm tra drift giữa DB và Proxmox.

## 6.3 Developer / Automation Agent

Cần:

- API contract rõ;
- idempotency rõ;
- error codes ổn định;
- schema migration;
- local lab/mocks;
- acceptance criteria theo từng epic.

# 7. Functional requirements

| ID | Yêu cầu | Mức |
|---|---|---|
| FR-001 | Tạo instance từ template version đã `ACTIVE` | MUST |
| FR-002 | Reserve VMID/IP/hostname bằng transaction | MUST |
| FR-003 | Tạo full clone và poll task tới terminal state | MUST |
| FR-004 | Configure CPU/RAM/disk/NIC/cloud-init/QGA | MUST |
| FR-005 | Tạo PGW binding trước khi boot | MUST |
| FR-006 | Chờ guest readiness không dùng sleep cố định | MUST |
| FR-007 | Validate machine identity uniqueness | MUST |
| FR-008 | Validate một NIC, một default route, IPv6 deny | MUST |
| FR-009 | Validate egress proof qua PGW API | MUST |
| FR-010 | Apply workload adapter có checksum/signature policy | MUST |
| FR-011 | Retry idempotent theo checkpoint | MUST |
| FR-012 | Rollback theo compensating action | MUST |
| FR-013 | Quarantine và decommission | MUST |
| FR-014 | Batch create có concurrency/rate limit | SHOULD |
| FR-015 | Operator UI | SHOULD P1 |
| FR-016 | Multi-cluster scheduler | COULD P2 |

# 8. Non-functional requirements

## 8.1 Reliability

- Worker crash không mất checkpoint.
- API restart không làm mất job.
- External API timeout không được coi là success.
- Mọi external mutation phải có read-after-write verification.
- Job lease hết hạn phải được worker khác tiếp quản an toàn.

## 8.2 Security

- Proxmox/PGW credential không nằm trong DB plaintext.
- API token có scope tối thiểu.
- Cloud-init không chứa long-lived secret.
- SSH chỉ dùng key hoặc certificate; password login mặc định off.
- Audit không chứa secret.
- Không có endpoint chạy shell tùy ý.

## 8.3 Performance

- API mutation trả `202` nhanh; provisioning chạy async.
- Concurrency được giới hạn theo Proxmox node, storage và network segment.
- Không poll Proxmox với nhịp quá dày.
- Batch job phải có backpressure.

## 8.4 Maintainability

- Ports/adapters cho Proxmox, PGW, guest transport và workload.
- Domain không phụ thuộc trực tiếp SDK client cụ thể.
- OpenAPI và DB migration nằm trong repository.
- Structured error code và contract test.

# 9. Các quyết định sản phẩm P0

| Quyết định | Chốt |
|---|---|
| Core language | Go, cùng chuẩn toolchain với PGW nhưng repo độc lập |
| Source of truth | PostgreSQL |
| Queue | PostgreSQL lease + `FOR UPDATE SKIP LOCKED` |
| Guest OS | Ubuntu 22.04 LTS |
| Clone mode | Full clone mặc định |
| Guest readiness | QGA trước, SSH fallback |
| Network | Một NIC trên bridge isolated; IPv6 deny mặc định |
| Egress | PGW API binding + egress proof |
| Template | Immutable, versioned, checksumed |
| Agent trong guest | Không bắt buộc P0 |
| UI | CLI/API P0; UI P1 |

# 10. Roadmap cấp cao

```text
Phase 0A - Foundation
Domain, DB, migrations, API skeleton, auth, audit

Phase 0B - Provisioning Core
Proxmox adapter, resource reservation, state engine

Phase 0C - Network and Validation
PGW adapter, identity/network/egress validators

Phase 0D - Workload and Operations
Workload adapter, metrics, runbook, chaos tests

Phase 1 - Operator Experience
Dashboard, batch workflows, template promotion, richer policy

Phase 2 - Scale and HA
Multi-cluster scheduler, HA workers, external secret manager, DR
```

# 11. Deliverables

- Master blueprint.
- OpenAPI 3.1 specification.
- PostgreSQL schema/migrations specification.
- Proxmox provisioning contract.
- Ubuntu golden template specification.
- Lifecycle state machine.
- PGW integration contract.
- Identity/network/egress validation specification.
- Observability, alerting và chaos test plan.
- Operations runbook.
- Acceptance test matrix và risk register.

# 12. Definition of Done cấp dự án

VM Factory P0 được xem là hoàn thành khi:

```text
Một request hợp lệ
→ tạo đúng một VM
→ cấp đúng một identity/resource set
→ tạo đúng một PGW binding
→ boot và validate tự động
→ áp workload adapter
→ trả READY có evidence
```

và khi bất kỳ bước nào thất bại:

```text
job có error code rõ
→ retry không tạo trùng
→ rollback giải phóng resource đúng policy
→ audit truy được toàn bộ chuỗi sự kiện
```
