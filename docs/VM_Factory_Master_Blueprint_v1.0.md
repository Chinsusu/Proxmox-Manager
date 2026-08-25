---
title: "VM Factory & Fleet Control Plane"
subtitle: "Project Description, Product Requirements, Technical Design, Implementation Contracts and Operations Runbook"
author: "VM Factory Engineering"
date: "2026-08-25"
lang: vi-VN
---

**Repository:** `vm-factory`  
**Phiên bản:** 1.0  
**Trạng thái:** Dev-ready proposal  
**Ngày chốt:** 25/08/2026

> Một control plane độc lập để biến yêu cầu provisioning thành Linux VM sạch, identity duy nhất, network/egress đúng, evidence đầy đủ và lifecycle có thể retry/rollback.

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# Document Control

| Trường | Giá trị |
|---|---|
| Tên dự án | VM Factory & Fleet Control Plane |
| Repository | `vm-factory` |
| Phiên bản tài liệu | 1.0 |
| Trạng thái | Dev-ready proposal |
| Nền tảng P0 | Proxmox + Ubuntu 22.04 + PostgreSQL + Go |
| External dependency | PGW API |
| Workload | Adapter-based, generic |
| Ngày | 25/08/2026 |

# Mục lục điều hành

| Phần | Nội dung |
|---|---|
| I | Project Charter & Product Requirements |
| II | System Architecture & Technical Design |
| III | Proxmox Provisioning Contract |
| IV | Ubuntu 22.04 Golden Template |
| V | VM Lifecycle State Machine |
| VI | Data Model, IPAM & Resource Registry |
| VII | PGW Integration Contract |
| VIII | Identity, Network & Egress Validation |
| IX | Observability, Alerting & Test Plan |
| X | Operations Runbook & Implementation Roadmap |
| Appendices | OpenAPI, SQL schema, config, ADR, risk và test matrix |

# Cách sử dụng bộ hồ sơ

- Product/lead đọc Phần I để chốt mục tiêu và non-goal.
- Architect/dev đọc Phần II-VIII như implementation contract.
- QA/SRE dùng Phần IX-X và appendices.
- Mọi thay đổi kiến trúc quan trọng phải có ADR mới.

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN I - PROJECT CHARTER & PRODUCT REQUIREMENTS

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

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN II - SYSTEM ARCHITECTURE & TECHNICAL DESIGN

# 1. Architecture Drivers

Thiết kế bị chi phối bởi bảy driver:

1. **Idempotency:** retry không được tạo resource trùng.
2. **External systems are eventually consistent:** clone/start/configure là async và có thể timeout dù action vẫn tiếp tục.
3. **Partial failure is normal:** Proxmox success nhưng PGW fail, hoặc ngược lại.
4. **Identity must be observed, not assumed:** dữ liệu cloud-init request không thay thế validation từ guest.
5. **Network must exist before first workload traffic:** binding được tạo trước boot.
6. **Source of truth must be explicit:** DB lưu desired/lifecycle state; Proxmox và PGW là external resource state.
7. **Reconciliation beats imperative scripting:** worker liên tục đưa external state về desired state thay vì tin một chuỗi command một lần.

# 2. System Context

![Kiến trúc hệ thống](assets/architecture.png){width=95%}

## 2.1 Boundaries

```text
VM Factory owns:
- request, state machine, resource reservation
- instance inventory
- template registry
- validation evidence
- audit and lifecycle

Proxmox owns:
- VM object, virtual hardware, task execution
- storage volume and runtime state

PGW owns:
- client identity at gateway
- mapping and egress policy
- data-plane health and egress proof

Guest OS owns:
- machine identity, SSH host keys, OS/network state

Workload adapter owns:
- application-specific install/health/remove contract
```

# 3. Target Components

## 3.1 `vmf-api`

Trách nhiệm:

- AuthN/AuthZ.
- Validate request và policy.
- Idempotency key.
- Tạo desired instance/job.
- Read model: instance, job, template, capacity, validation, audit.
- Không gọi lệnh shell và không giữ quyền Proxmox trực tiếp nếu có thể tách credential sang worker.

## 3.2 `vmf-worker`

Trách nhiệm:

- Lease job từ PostgreSQL.
- Thực thi state transition.
- Gọi Proxmox, PGW, QGA/SSH và workload adapter.
- Ghi checkpoint sau mỗi side effect.
- Reconcile desired/external state.
- Retry/backoff/timeout.
- Compensating action.

Worker model:

```text
PostgreSQL job lease
+ bounded worker pool
+ per-cluster semaphore
+ per-storage semaphore
+ per-network-segment semaphore
+ context deadline
```

## 3.3 `vmf-cli`

- Operator workflow không cần UI.
- Tạo instance/batch.
- Xem job/state/evidence.
- Retry/quarantine/rebuild/decommission.
- Xuất JSON cho automation.

## 3.4 PostgreSQL

Vai trò:

- Source of truth.
- Unique constraint cho resource.
- Job queue/lease.
- Idempotency store.
- Audit append-only.
- Outbox event.

Không dùng database như log store dung lượng lớn; log chi tiết đi tới logging backend.

## 3.5 Adapters

```go
type ProxmoxAdapter interface {
    AllocateNextVMID(ctx context.Context) (int, error)
    Clone(ctx context.Context, req CloneRequest) (TaskRef, error)
    Configure(ctx context.Context, req ConfigureRequest) (TaskRef, error)
    Start(ctx context.Context, ref VMRef) (TaskRef, error)
    Stop(ctx context.Context, ref VMRef) (TaskRef, error)
    Delete(ctx context.Context, ref VMRef, purge bool) (TaskRef, error)
    GetTask(ctx context.Context, task TaskRef) (TaskStatus, error)
    GetVM(ctx context.Context, ref VMRef) (VMObservedState, error)
    GuestPing(ctx context.Context, ref VMRef) error
}
```

```go
type PGWAdapter interface {
    CreateClient(ctx context.Context, req ClientRequest) (ClientRef, error)
    CreateMapping(ctx context.Context, req MappingRequest) (MappingRef, error)
    ActivateMapping(ctx context.Context, id string) (Generation, error)
    SuspendMapping(ctx context.Context, id string) error
    DeleteMapping(ctx context.Context, id string) error
    EgressProof(ctx context.Context, clientID string) (EgressEvidence, error)
}
```

```go
type GuestTransport interface {
    WaitReady(ctx context.Context, vm VMRef) error
    Exec(ctx context.Context, vm VMRef, cmd Command) (CommandResult, error)
    Upload(ctx context.Context, vm VMRef, artifact Artifact) error
    ReadFacts(ctx context.Context, vm VMRef) (GuestFacts, error)
}
```

```go
type WorkloadAdapter interface {
    Name() string
    Validate(ctx context.Context, vm VMRef) (ValidationReport, error)
    Install(ctx context.Context, vm VMRef, spec WorkloadSpec) error
    Health(ctx context.Context, vm VMRef) (HealthReport, error)
    Remove(ctx context.Context, vm VMRef) error
}
```

# 4. Deployment Architecture

![Deployment topology](assets/deployment_topology.png){width=95%}

## 4.1 P0 deployment

```text
1 x vmf-api
1-3 x vmf-worker
1 x PostgreSQL
1 x Prometheus-compatible collector
external: Proxmox API, PGW API, secret files/store
```

API và worker có thể chạy trên một management VM riêng, không chạy trực tiếp trên Proxmox host. Lý do:

- tránh thay đổi package/firewall trên hypervisor;
- blast radius nhỏ hơn;
- nâng cấp control plane độc lập;
- audit và secret isolation rõ hơn.

## 4.2 Network flows

| Source | Destination | Purpose | Policy |
|---|---|---|---|
| Operator | vmf-api | REST/CLI | TLS + JWT/API token |
| vmf-worker | PostgreSQL | state/lease | TLS hoặc private LAN |
| vmf-worker | Proxmox API | lifecycle | API token scoped |
| vmf-worker | PGW API | client/mapping/proof | service token/mTLS |
| vmf-worker | Guest | QGA via PVE, SSH fallback | allowlist |
| vmf-api/worker | Metrics/logging | telemetry | outbound only |

# 5. Domain Model

## 5.1 Aggregate: VM Instance

Một `vm_instance` là aggregate root cho lifecycle. Nó tham chiếu:

- template version;
- Proxmox placement;
- virtual hardware desired state;
- IP/network segment;
- egress binding;
- current state/checkpoint;
- current job;
- identity observations;
- validations;
- workload assignment.

### Invariants

1. Một instance có tối đa một active VMID.
2. Một IPv4 active chỉ thuộc một instance.
3. Một hostname active chỉ thuộc một instance trong scope.
4. Một instance READY phải có validation run PASS cho identity, network và egress.
5. Một instance RETIRED không được giữ active PGW mapping hoặc active IP allocation.
6. Template không `ACTIVE` không được dùng cho job mới.
7. Rebuild phải tạo generation mới; không ghi đè lịch sử identity observation.

## 5.2 Desired vs observed state

```text
desired_state: READY
observed_state:
  proxmox: running
  guest: reachable
  network: pass
  egress: pass
  workload: healthy
```

Nếu desired và observed lệch, reconciler quyết định:

- chờ;
- retry;
- repair;
- degrade;
- quarantine;
- rollback.

# 6. Job and State Engine

## 6.1 Job lease

Job execution state là state machine riêng (`job_state`: `QUEUED`, `RUNNING`, `RETRY_WAIT`, `SUCCEEDED`, `FAILED`, `CANCELLED`), không dùng chung enum với instance lifecycle state. Vị trí lifecycle mà job đang xử lý nằm trong cột `checkpoint`.

Worker claim job bằng transaction:

```sql
SELECT id
FROM provisioning_jobs
WHERE next_attempt_at <= now()
  AND state IN ('QUEUED','RETRY_WAIT')
ORDER BY priority DESC, created_at
FOR UPDATE SKIP LOCKED
LIMIT 1;
```

Sau đó set:

```text
lease_owner
lease_expires_at
attempt
started_at
```

Worker phải heartbeat lease. Nếu worker chết, job quay lại pool sau grace period.

## 6.2 Checkpoint

Checkpoint không chỉ là string state; nó phải chứa external references:

```json
{
  "state": "CONFIGURING",
  "pve": {
    "node": "pve01",
    "vmid": 201,
    "clone_task": "UPID:...",
    "config_generation": 3
  },
  "pgw": {
    "client_id": "cli_...",
    "mapping_id": "map_...",
    "desired_generation": 42
  }
}
```

## 6.3 Idempotency at every step

| Step | Idempotency mechanism |
|---|---|
| Create request | `Idempotency-Key` unique per tenant/scope |
| Reserve VMID/IP | DB unique constraint + reservation state |
| Clone | Search instance tag/description before new clone |
| Configure | Compare desired config hash |
| PGW client | External reference stored + create key/name deterministic |
| Mapping | One active binding constraint |
| Boot | Read current VM status before start |
| Workload install | Adapter version marker + checksum |
| Delete | Treat not-found as success after verification |

# 7. External Side-effect Pattern

Mỗi side effect dùng năm bước:

```text
1. Persist intent
2. Call external API
3. Persist external task/reference
4. Poll/read-after-write
5. Persist observed result + audit
```

Không được:

```text
call external API
→ crash
→ không lưu reference
→ retry mù
```

# 8. Resource Reservation

## 8.1 VMID

VM Factory có thể gọi `cluster/nextid`, nhưng vẫn phải reserve trong DB trước side effect. Nếu Proxmox trả VMID đã bị dùng giữa hai bước, adapter retry với VMID mới trong cùng job.

## 8.2 IPAM

Allocation state:

```text
FREE → RESERVED → ASSIGNED → QUARANTINED → RELEASED
```

Reservation có TTL. Job giữ lease sống khi provisioning đang hợp lệ.

## 8.3 Hostname

Hostname được generate từ policy:

```text
{prefix}-{sequence:04d}
```

Ví dụ:

```text
node-0001
node-0002
```

Không derive hostname từ IP để tránh coupling.

# 9. Template Registry

Template record gồm:

```text
id
name
version
os_family/os_version
architecture
pve_cluster/pve_node/template_vmid
storage
clone_mode_allowed
image_checksum
build_manifest
validation_status
state: DRAFT|CANDIDATE|ACTIVE|DEPRECATED|REVOKED
```

Promotion:

```text
DRAFT → CANDIDATE → validation suite → ACTIVE
```

Rollback template không sửa record cũ; promote version trước làm active.

# 10. API Design Principles

- REST + JSON, OpenAPI 3.1.
- Mutation trả `202 Accepted` khi có async job.
- `Idempotency-Key` bắt buộc cho create/rebuild/decommission.
- Error envelope thống nhất.
- Optimistic concurrency qua `version` hoặc `If-Match`.
- Pagination cursor-based.
- Secret write-only.

Error envelope:

```json
{
  "error": {
    "code": "IP_POOL_EXHAUSTED",
    "message": "No free IPv4 address in segment vm-lan-a",
    "details": {"segment_id": "seg_..."},
    "request_id": "req_..."
  }
}
```

# 11. Security Architecture

## 11.1 Credential model

| Credential | Scope |
|---|---|
| Proxmox token | VM/template/storage operations cần thiết |
| PGW token | clients/mappings/egress proof |
| PostgreSQL | service-specific user |
| SSH CA/key | guest bootstrap/operations |
| Workload artifact key | verify signature/checksum |

Secrets được tham chiếu bằng `secret_ref`, không lưu plaintext trong domain table.

## 11.2 Cloud-init secret hygiene

Cloud-init user data có thể tồn tại trong Proxmox config và guest logs. Vì vậy:

- không đặt long-lived API token;
- dùng short-lived bootstrap token một lần;
- rotate/revoke ngay sau bootstrap;
- tắt `emit_keys_to_console`;
- không ghi secret trong `runcmd` command line nếu có thể dùng file credential mode 0600.

## 11.3 Identity storage

Không cần lưu raw `/etc/machine-id`. Lưu:

```text
HMAC-SHA256(identity_key, machine_id)
```

để phát hiện trùng mà không biến DB thành nơi chứa identifier có thể correlation ngoài hệ.

## 11.4 No arbitrary shell

Guest command phải qua allowlisted operation:

```text
read_facts
wait_cloud_init
install_artifact
service_status
service_restart
collect_bounded_logs
```

P0 có thể dùng SSH executor nội bộ, nhưng API không nhận raw command từ user.

# 12. Observability Architecture

Correlation fields bắt buộc:

```text
request_id
job_id
instance_id
pve_cluster
pve_node
vmid
state
attempt
external_task_id
```

Metric nhóm:

- provisioning duration theo state;
- external API latency/error;
- job backlog/lease expiry;
- resource pool utilization;
- validation pass/fail;
- rollback outcome;
- orphan detection;
- template version distribution.

# 13. Failure Taxonomy

| Class | Ví dụ | Xử lý |
|---|---|---|
| Retryable transient | timeout, 502, task still running | exponential backoff |
| Retryable conflict | VMID collision, DB lock | reserve lại có giới hạn |
| Validation failure | duplicate identity, second NIC | quarantine |
| Capacity | storage full, IP pool exhausted | fail fast / alert |
| Authentication | token expired | stop retries dài, alert |
| Permanent config | bridge không tồn tại | fail + rollback |
| Unknown side effect | timeout sau clone request | reconcile/search trước retry |

# 14. Rollback Model

Compensating actions chạy ngược thứ tự side effect:

```text
remove workload
stop/delete VM
suspend/delete PGW mapping
release IP
release VMID reservation
mark job failed
```

Rollback cũng là state machine, có checkpoint riêng. Nếu rollback thất bại, instance chuyển `QUARANTINED` với resource inventory còn lại; không xóa record để che mất lỗi.

# 15. Technology Stack

| Layer | Chọn |
|---|---|
| Core | Go |
| API | `net/http` + router project-approved |
| DB | PostgreSQL, migrations versioned |
| Queue | PostgreSQL lease / SKIP LOCKED |
| Config | YAML + environment overrides |
| Metrics | Prometheus format |
| Logs | JSON structured logs |
| Tracing | OpenTelemetry optional P1 |
| CLI | Go, JSON/table output |
| UI | P1; frontend tách khỏi core |

# 16. Release and Compatibility

- Semantic versioning.
- Reproducible Go build.
- Embedded version/commit/build time.
- SHA-256 checksum và SBOM.
- DB migration forward-only; backup trước migration.
- External API contract tests với PGW và Proxmox mock.
- Feature flags cho batch, linked clone, node agent và auto-remediation.

# 17. Architecture Decision Records

Các ADR nằm trong thư mục `adr/`:

1. Go cho core services.
2. PostgreSQL làm source of truth và queue P0.
3. Reconciler/state machine thay script tuyến tính.
4. Proxmox API thay shelling out `qm` trong service.
5. QGA-first, SSH fallback.
6. PGW là external dependency.
7. HMAC identity digest, không lưu raw machine ID.
8. Full clone mặc định production.
9. IPv6 deny mặc định.
10. Không node agent trong P0.

# 18. Implementation Guardrails

Dev không được tự quyết khác tài liệu ở các điểm sau nếu chưa có ADR mới:

- Không đổi core sang Python/Node.
- Không dùng in-memory state làm production source of truth.
- Không gọi `qm` qua shell từ API service.
- Không dùng sleep cố định để chờ VM.
- Không tạo VM trước khi reserve resource.
- Không boot trước khi network/egress binding sẵn sàng.
- Không lưu plaintext secret.
- Không expose raw shell endpoint.
- Không retry external mutation mà chưa reconcile.
- Không xóa audit/resource record để “dọn lỗi”.

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN III - PROXMOX PROVISIONING CONTRACT

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

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN IV - UBUNTU GOLDEN TEMPLATE

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

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN V - VM LIFECYCLE STATE MACHINE

# 1. Nguyên tắc

State machine là contract trung tâm của VM Factory. Không cho phép code cập nhật state tùy ý. Mọi transition phải qua một transition handler có:

```text
from_state
allowed_to_state
guard
side_effect
checkpoint
timeout
retry policy
compensation
audit event
```

![VM lifecycle state machine](assets/state_machine.png){width=98%}

# 2. State Groups

## 2.1 Provisioning states

```text
REQUESTED
RESERVING
CLONING
CONFIGURING
NETWORK_BINDING
BOOTING
WAITING_GUEST
VALIDATING_IDENTITY
VALIDATING_EGRESS
APPLYING_WORKLOAD
READY
```

## 2.2 Exception states

```text
RETRY_WAIT
DEGRADED
QUARANTINED
ROLLING_BACK
FAILED
```

## 2.3 Retirement states

```text
DRAINING
DECOMMISSIONING
RELEASING_RESOURCES
RETIRED
```

# 3. State Definitions

| State | Entry condition | Exit evidence |
|---|---|---|
| REQUESTED | request validated | instance/job persisted |
| RESERVING | job leased | VMID/IP/hostname/placement reserved |
| CLONING | resources reserved | VM exists and clone task success/recovered |
| CONFIGURING | VM exists | desired config observed |
| NETWORK_BINDING | VM config known | PGW binding active/reconciled |
| BOOTING | network ready | VM running |
| WAITING_GUEST | VM running | QGA/SSH + cloud-init ready |
| VALIDATING_IDENTITY | guest ready | identity report PASS |
| VALIDATING_EGRESS | network report PASS prerequisite | egress proof PASS |
| APPLYING_WORKLOAD | validations PASS | adapter health PASS |
| READY | all gates PASS | steady-state health evidence |
| DEGRADED | health breach | recovered or quarantine decision |
| QUARANTINED | unsafe/unknown state | operator/remediation decision |
| ROLLING_BACK | provisioning fatal | compensation terminal |
| FAILED | job terminal failure | manual retry/new job only |
| DRAINING | retire requested | workload stopped/drained |
| RETIRED | resources released | immutable historical record |

# 4. Transition Contract

## 4.1 REQUESTED → RESERVING

Guard:

- template active;
- segment active;
- request idempotency valid;
- capacity policy not hard-blocked.

Side effects: none ngoài DB.

## 4.2 RESERVING → CLONING

Transaction reserve:

```text
placement
VMID
IPv4
hostname
workload slot
```

Nếu thiếu capacity, fail `CAPACITY_UNAVAILABLE` không gọi external API.

## 4.3 CLONING → CONFIGURING

Evidence:

- external VM ref persisted;
- clone task terminal success hoặc recovered by reconciliation;
- external tag matches instance ID.

## 4.4 CONFIGURING → NETWORK_BINDING

Evidence:

- CPU/RAM/disk/NIC/cloud-init desired config observed;
- no unexpected NIC;
- config hash matches.

## 4.5 NETWORK_BINDING → BOOTING

Evidence:

- PGW client exists;
- mapping exists and active;
- desired generation applied;
- optional initial egress path probe at gateway.

## 4.6 BOOTING → WAITING_GUEST

Read-before-write:

- if VM already running, do not issue duplicate start;
- start task persisted;
- observed running.

## 4.7 WAITING_GUEST → VALIDATING_IDENTITY

Evidence:

- QGA ping or approved SSH fallback;
- cloud-init terminal success;
- expected IPv4 observed.

## 4.8 VALIDATING_IDENTITY → VALIDATING_EGRESS

Checks:

- machine ID digest unique;
- MAC/hostname/IP match inventory;
- SSH host key fingerprint unique;
- one NIC;
- one IPv4 default route;
- no IPv6 default route;
- no stale application state.

## 4.9 VALIDATING_EGRESS → APPLYING_WORKLOAD

- PGW egress proof PASS;
- expected mapping/client;
- no direct-route anomaly;
- proof timestamp within policy window.

## 4.10 APPLYING_WORKLOAD → READY

- artifact checksum/signature verified;
- install marker version matches;
- service health PASS;
- final evidence snapshot persisted.

# 5. Retry Policy

Retry classification:

```text
TRANSIENT: network timeout, 502/503, task running
CONFLICT: VMID race, DB lock
CAPACITY: storage/IP exhausted
AUTH: invalid/expired token
VALIDATION: identity/network mismatch
PERMANENT: bad bridge/template/config
UNKNOWN_SIDE_EFFECT: timeout after mutation
```

Backoff:

```text
transient: exponential + jitter, max attempts configurable
conflict: short retry + resource re-reservation
capacity: no hot loop; retry after capacity event/manual
validation: quarantine, not blind retry
auth: alert and pause integration
unknown side effect: reconcile before any reissue
```

# 6. Rollback State Machine

```text
ROLLING_BACK
  1. stop/remove workload if applied
  2. stop/delete VM if owned by instance
  3. suspend/delete PGW mapping/client
  4. release IP/hostname/VMID reservation
  5. mark external leftovers
  6. terminal FAILED or QUARANTINED
```

Compensation step cũng idempotent. Không stop rollback vì một delete trả not-found.

# 7. Quarantine Policy

Quarantine dùng khi:

- duplicate identity;
- unknown VM ownership/tag mismatch;
- second NIC/default route unexpected;
- egress proof mismatch;
- rollback không xóa được resource;
- workload state không xác định;
- manual security hold.

Quarantine action:

```text
suspend egress mapping
optionally stop VM
retain evidence/log references
block automatic reuse of IP/proxy binding
alert operator
```

# 8. Rebuild Workflow

Rebuild không mutate instance generation cũ. Nó tạo:

```text
same logical instance_id
+ generation N+1
+ new provisioning job
+ new VM external reference
+ new identity observation
```

Policy có thể giữ hoặc đổi IP/hostname/egress binding. Generation cũ chuyển `REPLACED`/retired sau cutover.

# 9. Decommission Workflow

```text
READY/QUARANTINED
→ DRAINING
→ DECOMMISSIONING
→ RELEASING_RESOURCES
→ RETIRED
```

Operator không được xóa VM trực tiếp qua VM Factory API mà bỏ qua drain/release. Emergency purge là quyền riêng, bắt buộc reason và audit.

# 10. Event Model

Mỗi transition emit domain event vào outbox:

```json
{
  "event_id": "evt_...",
  "type": "vm_instance.state_changed",
  "instance_id": "ins_...",
  "job_id": "job_...",
  "from": "CONFIGURING",
  "to": "NETWORK_BINDING",
  "occurred_at": "...",
  "correlation_id": "req_..."
}
```

# 11. State Engine Tests

- Mọi allowed transition.
- Mọi forbidden transition.
- Worker crash trước/sau checkpoint.
- Lease expiry and takeover.
- Retry không tăng resource count.
- Rollback từng step fail.
- Quarantine guard.
- Rebuild generation.
- Decommission idempotency.
- Audit/outbox transaction cùng state update.

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN VI - DATA MODEL, IPAM & RESOURCE REGISTRY

# 1. Data Principles

1. PostgreSQL là source of truth cho desired/lifecycle state.
2. External resource state được lưu như observation, không thay source of truth của Proxmox/PGW.
3. Unique constraint thay cho kiểm tra application-only.
4. Resource reservation là first-class entity.
5. Audit append-only.
6. Secret tách khỏi business tables.
7. Raw machine ID không lưu; dùng HMAC digest.
8. Soft-delete/retention cho record lịch sử; external resource có thể bị delete thật.

![Data model giản lược](assets/data_model.png){width=92%}

# 2. Core Tables

## 2.1 `vm_templates`

```text
id UUID PK
name TEXT
version TEXT
family TEXT
os_family TEXT
os_version TEXT
architecture TEXT
pve_cluster_id UUID
pve_node TEXT
pve_template_vmid INT
storage TEXT
source_checksum TEXT
build_manifest JSONB
state ENUM
validation_status ENUM
created_at/updated_at
UNIQUE(family, version)
```

## 2.2 `vm_instances`

```text
id UUID PK
logical_name TEXT
hostname TEXT
state ENUM
generation INT
template_id UUID FK
pve_cluster_id UUID
pve_node TEXT
vmid INT
resource_pool TEXT
desired_config JSONB
desired_config_hash TEXT
workload_adapter TEXT
workload_spec JSONB
version BIGINT
created_at/updated_at/retired_at
```

Unique partial indexes:

```text
(pve_cluster_id, vmid) WHERE retired_at IS NULL
hostname WHERE retired_at IS NULL
(logical_name, generation)
```

## 2.3 `network_segments`

```text
id UUID PK
name TEXT UNIQUE
cidr CIDR
gateway INET
bridge TEXT
dns_servers INET[]
ipv6_policy TEXT
allocation_strategy TEXT
state TEXT
```

## 2.4 `ip_allocations`

```text
id UUID PK
segment_id UUID FK
address INET
instance_id UUID NULL
state ENUM
reserved_until TIMESTAMPTZ
assigned_at/released_at
UNIQUE(segment_id, address)
```

## 2.5 `egress_bindings`

```text
id UUID PK
instance_id UUID FK
pgw_client_id TEXT
pgw_mapping_id TEXT
pgw_policy_id TEXT
state ENUM
expected_exit JSONB
desired_generation BIGINT
last_proof_at TIMESTAMPTZ
created_at/updated_at
```

Một partial unique active binding per instance.

## 2.6 `provisioning_jobs`

`state` dùng enum `job_state` riêng (`QUEUED|RUNNING|RETRY_WAIT|SUCCEEDED|FAILED|CANCELLED`), không dùng chung `instance_state`; `checkpoint` giữ vị trí lifecycle.

```text
id UUID PK
instance_id UUID FK
operation ENUM
state ENUM(job_state)
checkpoint TEXT
checkpoint_data JSONB
priority INT
attempt INT
max_attempts INT
next_attempt_at TIMESTAMPTZ
lease_owner TEXT
lease_expires_at TIMESTAMPTZ
error_code TEXT
error_message TEXT
created_at/started_at/finished_at
```

## 2.7 `external_tasks`

Lưu task reference từ Proxmox/PGW:

```text
id UUID PK
job_id UUID FK
system TEXT
operation TEXT
external_id TEXT
status TEXT
request_hash TEXT
started_at/last_polled_at/finished_at
result JSONB
UNIQUE(system, external_id)
```

## 2.8 `identity_observations`

```text
id UUID PK
instance_id UUID FK
generation INT
machine_id_digest TEXT
ssh_host_fingerprint TEXT
cloud_init_instance_id TEXT
hostname TEXT
mac_addresses MACADDR[]
ip_addresses INET[]
boot_id TEXT
facts JSONB
observed_at TIMESTAMPTZ
```

Unique digest policy có thể theo active instances hoặc toàn lịch sử tùy compliance; mặc định cảnh báo nếu digest từng xuất hiện ở logical instance khác.

## 2.9 `validation_runs`

```text
id UUID PK
instance_id UUID FK
job_id UUID FK
type ENUM(identity, network, egress, workload, template)
result ENUM(pass, fail, warn)
evidence JSONB
ruleset_version TEXT
started_at/finished_at
```

## 2.10 `audit_events`

```text
id UUID PK
occurred_at TIMESTAMPTZ
actor_type TEXT
actor_id TEXT
action TEXT
resource_type TEXT
resource_id TEXT
request_id TEXT
correlation_id TEXT
before JSONB
after JSONB
metadata JSONB
```

Application không expose update/delete. Retention/archival qua privileged maintenance procedure.

## 2.11 `idempotency_keys`

```text
scope TEXT
key TEXT
request_hash TEXT
response_status INT
response_body JSONB
resource_id UUID
expires_at TIMESTAMPTZ
PRIMARY KEY(scope, key)
```

Cùng key nhưng request hash khác trả `IDEMPOTENCY_CONFLICT`.

## 2.12 `resource_locks`

Dùng cho reservation cross-worker:

```text
resource_type
resource_key
owner_job_id
lease_expires_at
PRIMARY KEY(resource_type, resource_key)
```

# 3. IPAM Allocation

## 3.1 Strategy

P0 hỗ trợ:

- sequential-lowest-free;
- explicit requested IP;
- reserved ranges/exclusions.

Không cấp:

```text
network address
broadcast
gateway
reserved/excluded
active/reserved/quarantined address
```

## 3.2 Transaction

```sql
SELECT id, address
FROM ip_allocations
WHERE segment_id = $1 AND state = 'FREE'
ORDER BY address
FOR UPDATE SKIP LOCKED
LIMIT 1;
```

Update `RESERVED`, set `instance_id` và TTL trong cùng transaction tạo job/resource reservation.

## 3.3 Reaper

Reservation reaper chỉ release khi:

- TTL expired;
- job không active/leased;
- no external VM or mapping observed;
- audit event written.

# 4. Capacity Registry

Tables/observations:

```text
pve_clusters
pve_nodes
pve_storages
capacity_snapshots
```

Capacity snapshot là observation có TTL, không được coi là hard transaction guarantee. Mutation vẫn phải xử lý race.

# 5. Consistency Model

## 5.1 Strong consistency trong DB

- instance + job + reservations created cùng transaction;
- state transition + audit + outbox cùng transaction;
- mapping reference persist trước activate;
- unique constraints enforce identity/resource.

## 5.2 Eventual consistency external

- Proxmox task async;
- PGW reconcile async;
- guest readiness async.

Worker poll/reconcile đến khi evidence terminal hoặc timeout policy.

# 6. Retention

| Data | Retention |
|---|---|
| Active instance | permanent |
| Retired instance metadata | >= 1 năm hoặc policy |
| Audit | >= 1 năm; archive cold storage |
| Validation evidence | >= 180 ngày |
| Job details | >= 180 ngày |
| Raw logs | logging backend policy |
| Secret versions | until rotation grace ends |

# 7. Migration Strategy

- `schema_migrations` forward-only.
- Migration chạy bởi dedicated command trước app rollout.
- Backup bắt buộc trước destructive migration.
- Expand/contract cho zero/low downtime.
- Application startup kiểm tra compatible schema version.

# 8. Backup and Restore

Backup:

```text
PostgreSQL logical backup daily
+ WAL/physical backup theo RPO
+ encrypted off-host copy
+ restore drill định kỳ
```

Restore không tự động tái chạy external side effects. Sau restore phải chạy reconciliation mode read-only trước, inventory Proxmox/PGW, sau đó operator approve repair.

# 9. Secrets

Secret table chỉ lưu ciphertext metadata nếu không dùng external secret manager:

```text
id
provider
ciphertext
nonce
key_version
created_at
rotated_at
```

AES-GCM envelope key từ systemd credential hoặc secret manager. Không lưu key trong DB.

# 10. Query/Index Requirements

Indexes tối thiểu:

- jobs by state/next_attempt/priority;
- instances by state/pve node/template;
- active IP allocations;
- external tasks by external ID;
- validation by instance/type/time;
- audit by resource/time/correlation;
- identity digest.

# 11. Data Quality Checks

Periodic invariant scanner:

```text
active instance without active IP
READY without PASS validations
retired instance with active mapping
duplicate VMID/IP/hostname/digest
job lease expired but state running
DB VM missing in Proxmox
Proxmox VM tagged VMF missing in DB
PGW mapping missing/orphaned
```

Kết quả không tự delete; tạo finding và remediation job có approval policy.

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN VII - PGW INTEGRATION CONTRACT

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

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN VIII - IDENTITY, NETWORK & EGRESS VALIDATION

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

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN IX - OBSERVABILITY, ALERTING & TEST PLAN

# 1. Observability Goals

Hệ thống phải trả lời trong vài phút:

```text
Job nào đang kẹt ở đâu?
External task nào đang chạy?
Resource nào đã tạo nhưng chưa được quản lý?
Template/version nào có failure rate cao?
Node/storage/network nào là bottleneck?
Instance READY có evidence gì?
Rollback còn resource nào chưa dọn?
```

# 2. Structured Logging

JSON log bắt buộc:

```json
{
  "ts": "...",
  "level": "info",
  "component": "vmf-worker",
  "event": "state_transition",
  "request_id": "req_...",
  "job_id": "job_...",
  "instance_id": "ins_...",
  "from": "CLONING",
  "to": "CONFIGURING",
  "attempt": 1,
  "pve_node": "pve01",
  "vmid": 201,
  "duration_ms": 84231
}
```

Redaction:

- token/password/private key;
- cloud-init raw user data nếu chứa secret;
- full command output không bounded;
- raw machine-id.

# 3. Metrics

## 3.1 Job metrics

```text
vmf_jobs_total{operation,result}
vmf_jobs_active{state}
vmf_job_duration_seconds{operation}
vmf_state_duration_seconds{state}
vmf_job_retries_total{error_code}
vmf_job_lease_expired_total
vmf_job_backlog
```

## 3.2 Proxmox metrics

```text
vmf_pve_api_requests_total{operation,status}
vmf_pve_api_latency_seconds{operation}
vmf_pve_tasks_active{operation,node}
vmf_pve_task_duration_seconds{operation,node}
vmf_pve_capacity{node,resource}
```

## 3.3 Resource metrics

```text
vmf_ip_pool_addresses{segment,state}
vmf_instances{state,template_version,pve_node}
vmf_orphans_total{system,type}
vmf_identity_duplicates_total
vmf_validation_total{type,result,rule_id}
```

## 3.4 PGW/egress adapter metrics

```text
vmf_pgw_requests_total{operation,status}
vmf_pgw_binding_state{state}
vmf_egress_proof_total{result}
vmf_egress_proof_age_seconds
```

# 4. Alerts

| Alert | Condition | Severity |
|---|---|---|
| ProvisioningBacklogHigh | backlog/age vượt threshold | warning/critical |
| JobStuckInState | state duration > policy | warning |
| WorkerLeaseChurn | lease expiry tăng | critical |
| DuplicateIdentity | any active duplicate | critical |
| ResourceLeak | retired/failed giữ resource | critical |
| ProxmoxAuthFailed | 401/403 | critical |
| PGWAuthFailed | 401/403 | critical |
| StorageCapacityLow | free < threshold | warning/critical |
| IPPoolLow | free addresses < threshold | warning |
| TemplateFailureRate | version failure > baseline | warning |
| EgressProofFail | READY/boot validation fail | critical |
| RollbackIncomplete | compensation terminal fail | critical |

# 5. Dashboards

## Executive/Fleet

- total instances by state;
- provisioning success/SLO;
- capacity and IP pool;
- top failure codes;
- template distribution;
- active alerts.

## Operations

- job waterfall by state;
- external task latency;
- worker leases/backlog;
- Proxmox node/storage pressure;
- PGW binding/proof state;
- rollback/orphan queue.

## Instance Detail

- desired vs observed;
- state timeline;
- external references;
- identity/network/egress evidence;
- audit trail;
- allowed actions.

# 6. Test Pyramid

```text
Unit tests
→ domain/state/validation/config canonicalization

Contract tests
→ Proxmox adapter, PGW adapter, workload adapter

Integration tests
→ PostgreSQL + fake external services

System lab
→ real Proxmox test VM/template + PGW staging

Chaos tests
→ kill worker, timeout APIs, storage/network failures
```

# 7. Functional Acceptance Matrix

Các case đầy đủ nằm trong `appendices/acceptance_test_matrix.csv`. Gate P0 gồm:

- create happy path;
- idempotent duplicate request;
- worker crash at each checkpoint;
- clone timeout recovery;
- VMID/IP conflict;
- PGW unavailable before boot;
- QGA unavailable + SSH fallback;
- duplicate identity quarantine;
- IPv6 route quarantine;
- workload install rollback;
- decommission releases all resources.

# 8. Chaos Plan

## 8.1 Worker chaos

- kill -9 giữa external request và checkpoint;
- DB connection reset;
- lease expiration;
- two workers race same job.

Expected: one owner at a time; next worker reconciles, không tạo duplicate.

## 8.2 Proxmox chaos

- API 502/timeout;
- task lâu hơn timeout;
- VM lock;
- node reboot;
- storage full;
- template missing;
- bridge missing.

## 8.3 PGW chaos

- API unavailable;
- activation delayed;
- proof fail;
- mapping deleted externally;
- credential revoked.

## 8.4 Guest chaos

- cloud-init fail;
- QGA stopped;
- SSH unavailable;
- wrong route;
- duplicate machine ID;
- workload service crash.

# 9. Security Tests

- RBAC: viewer/operator/admin/service.
- Secret redaction in log/audit/API.
- Cloud-init data exposure review.
- No raw shell endpoint.
- SQL injection/input bounds.
- SSRF prevention: external base URLs allowlisted/config-only.
- TLS verification and CA rotation.
- Idempotency key collision.
- Audit append-only behavior.
- API token rotation/revocation.

# 10. Performance Tests

Pilot load:

```text
1, 3, 5, 10 concurrent provision jobs
```

Measure:

- clone duration;
- storage IOPS;
- API rate/latency;
- DB lock contention;
- worker memory/goroutines;
- job queue latency;
- PGW activation latency.

Concurrency limits chốt từ evidence, không từ cảm giác.

# 11. Release Gates

Một release không deploy production nếu:

- migration dry-run fail;
- state-machine golden tests fail;
- idempotency test fail;
- rollback test fail;
- duplicate identity test fail;
- secret scan fail;
- OpenAPI breaking change không bump version;
- system lab không pass.

# 12. Soak Test

Wave 3-5 VM chạy qua:

```text
provision
reboot guest
restart worker/API
restart PGW
restart Proxmox test node if possible
quarantine/recover
rebuild
decommission
```

Theo dõi ít nhất một chu kỳ vận hành đủ dài để lộ lease, retry và log rotation issues trước scale.

```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# PHẦN X - OPERATIONS RUNBOOK & ROADMAP

# 1. Repository Layout

```text
vm-factory/
├── cmd/
│   ├── api/
│   ├── worker/
│   └── cli/
├── internal/
│   ├── domain/
│   ├── stateengine/
│   ├── proxmox/
│   ├── pgw/
│   ├── guest/
│   ├── workload/
│   ├── ipam/
│   ├── validation/
│   ├── storage/
│   ├── jobs/
│   ├── audit/
│   └── observability/
├── api/openapi.yaml
├── migrations/
├── deploy/systemd/
├── configs/
├── scripts/
├── testlab/
├── docs/
└── go.mod
```

Python chỉ được dùng cho test/tooling nếu cần, không thay core services.

# 2. Environment Promotion

```text
local mocks
→ integration lab
→ staging Proxmox/PGW
→ production canary
→ production wave
```

Mỗi environment có credential, DB và resource pool riêng. Không dùng production template/segment cho integration test.

# 3. Initial Deployment

1. Provision management VM.
2. Install PostgreSQL hoặc connect managed DB.
3. Create DB users/schemas.
4. Install API/worker binaries và config.
5. Load systemd credentials.
6. Run migrations.
7. Register Proxmox cluster/PGW endpoint.
8. Register network segment/IP pool.
9. Register template as DRAFT.
10. Run template validation and promote.
11. Provision one canary instance.
12. Validate dashboards/alerts/audit.

# 4. Standard Operations

## 4.1 Create instance

```bash
vmf instance create \
  --template ubuntu-2204-2026.08.1 \
  --segment vm-lan-a \
  --egress-policy default-web \
  --workload sample-v1 \
  --idempotency-key create-node-0001
```

## 4.2 Inspect

```bash
vmf instance get ins_...
vmf job get job_... --events
vmf instance evidence ins_...
```

## 4.3 Retry

Retry chỉ khi job terminal/retryable và không có unresolved ownership conflict:

```bash
vmf job retry job_... --reason "transient PVE timeout"
```

## 4.4 Quarantine

```bash
vmf instance quarantine ins_... --reason "network validation mismatch"
```

Expected: suspend egress, state change, audit, alert.

## 4.5 Rebuild

```bash
vmf instance rebuild ins_... --template ubuntu-2204-2026.08.2
```

Rebuild tạo generation/job mới, không overwrite history.

## 4.6 Decommission

```bash
vmf instance decommission ins_... --reason "retired"
```

Theo dõi tới `RETIRED`, xác minh không còn VM/mapping/IP active.

# 5. Stuck Job Runbook

## 5.1 Identify

```text
job state duration
lease owner/expiry
external task reference
Proxmox VM/tag
PGW client/mapping
last checkpoint/audit
```

## 5.2 Decision

- External resource exists and owned: attach/reconcile.
- External action still running: extend/wait.
- External resource absent: retry mutation.
- Ownership ambiguous: quarantine/manual.
- DB state wrong but evidence clear: use repair command có audit, không sửa SQL tay.

# 6. Orphan Reconciliation

Scanner categories:

```text
PVE VM tagged VMF but missing DB
DB instance with missing VM
PGW mapping with missing DB binding
DB retired instance with active resource
IP allocated without active instance
```

Không auto-delete unknown orphan. Create finding + remediation plan.

# 7. Credential Rotation

## Proxmox/PGW token

1. Create new scoped token.
2. Deploy credential alongside old.
3. Health/self-check new token.
4. Switch config atomically/restart gracefully.
5. Verify requests.
6. Revoke old token.
7. Audit.

## SSH/bootstrap

Use short-lived certificates/token; revoke/expire old automatically.

# 8. Backup/Restore

## Backup

- DB daily logical plus RPO strategy.
- Config, CA, migration binaries.
- Template build manifests/checksums.
- Do not back up plaintext secret outside secret system.

## Restore

1. Restore DB to isolated environment.
2. Start API read-only, workers disabled.
3. Run schema/invariant checks.
4. Inventory external systems.
5. Produce drift report.
6. Operator approve reconciliation scope.
7. Enable workers gradually.

# 9. Disaster Recovery

RTO/RPO cần chốt theo production. P0 đề xuất:

```text
RPO <= 24h tối thiểu; tốt hơn với WAL
RTO <= 4h cho control plane
External VMs tiếp tục chạy khi control plane down
```

VM Factory outage không được làm PGW hoặc VM data plane dừng. Chỉ lifecycle operations tạm dừng.

# 10. Implementation Backlog

## Epic P0-00 Foundation

Deliverables:

- repo skeleton;
- config loader;
- structured logs;
- request/correlation IDs;
- auth/RBAC baseline;
- CI, lint, test.

Acceptance: binaries build reproducibly; no secret in logs.

## Epic P0-01 Domain & PostgreSQL

- migrations;
- entities/constraints;
- idempotency;
- job lease;
- audit/outbox;
- repositories.

Acceptance: race tests show no duplicate IP/VMID/job ownership.

## Epic P0-02 Proxmox Adapter

- auth/discovery;
- clone/config/start/stop/delete;
- task polling;
- QGA;
- reconciliation/tagging;
- contract tests.

## Epic P0-03 IPAM & Resource Reservation

- segments/pools/exclusions;
- reserve/release TTL;
- hostname allocator;
- capacity errors;
- invariant scanner.

## Epic P0-04 PGW Adapter

- create client/mapping;
- activate/suspend/delete;
- generation/proof;
- reconcile/orphan checks.

## Epic P0-05 State Engine

- transition registry;
- checkpoints;
- retry/backoff;
- rollback;
- quarantine;
- rebuild/decommission.

## Epic P0-06 Golden Template Tooling

- prepare script;
- offline validator;
- canary validator;
- manifest/registry/promotion.

## Epic P0-07 Validation Engine

- guest facts collector;
- HMAC identity digest;
- identity/network/egress rules;
- evidence storage;
- drift scanner.

## Epic P0-08 Workload Adapter

- interface;
- noop/sample adapter;
- artifact verification;
- install/health/remove;
- bounded logs.

## Epic P0-09 API & CLI

- OpenAPI implementation;
- create/list/get/actions;
- job/events/evidence;
- JSON/table CLI;
- error envelope.

## Epic P0-10 Observability

- metrics/logs;
- dashboards;
- alert rules;
- SLO reports.

## Epic P0-11 Test Lab & Chaos

- mocks;
- integration DB;
- Proxmox staging;
- PGW staging;
- chaos scripts;
- acceptance matrix.

## Epic P0-12 Deployment & Ops

- systemd units;
- credential files;
- migrations/backup;
- release package;
- runbooks;
- rollback.

# 11. Wave Plan

## Wave 0: one canary

Gate: complete evidence, reboot/rebuild/decommission PASS.

## Wave 1: 3-5 instances

Gate: no duplicate/leak; worker restart and external outage recovery PASS.

## Wave 2: 10 instances

Gate: capacity/backlog/IOPS acceptable; manual intervention <5%.

## Wave 3: 25+

Gate: stable SLO, rollback/orphan scanner clean, on-call runbook proven.

# 12. Change Management

- Every architecture deviation needs ADR.
- Breaking API requires version bump/migration plan.
- Template updates follow promotion pipeline.
- Production manual repair command requires reason/audit.
- Database hand-edit prohibited except incident procedure with peer review.

# 13. Final Handoff Checklist

```text
[ ] Project charter approved
[ ] TDD reviewed
[ ] OpenAPI validated
[ ] DB schema reviewed
[ ] Proxmox/PGW credentials scoped
[ ] Ubuntu template candidate built
[ ] State machine golden tests ready
[ ] Integration lab available
[ ] Dashboards/alerts loaded
[ ] Backup/restore tested
[ ] Wave 0 acceptance signed
```


```{=openxml}
<w:p><w:r><w:br w:type="page"/></w:r></w:p>
```

# APPENDIX A - API, DATA & CONFIG ARTIFACTS

Các artifact máy đọc được nằm trong thư mục `appendices/`:

- `vm-factory-openapi-v1.0.yaml`
- `vm-factory-schema-v1.0.sql`
- `vm-factory-config.example.yaml`
- `acceptance_test_matrix.csv`
- `risk_register.csv`
- `official_references.md`

# APPENDIX B - ARCHITECTURE DECISIONS

ADR-001 đến ADR-010 nằm trong thư mục `adr/`. ADR là nguồn quyết định khi implementation cần giải thích vì sao chọn một hướng và khi nào được phép thay đổi.

# APPENDIX C - OFFICIAL REFERENCES

- Proxmox VE API Viewer: https://pve.proxmox.com/pve-docs/api-viewer/
- Proxmox VE QEMU/KVM: https://pve.proxmox.com/pve-docs/chapter-qm.html
- Proxmox VE Cloud-Init Support: https://pve.proxmox.com/wiki/Cloud-Init_Support
- cloud-init: https://cloudinit.readthedocs.io/en/latest/
- systemd machine-id: https://www.freedesktop.org/software/systemd/man/latest/machine-id.html
- QEMU Guest Agent: https://www.qemu.org/docs/master/interop/qemu-ga.html
- OpenAPI 3.1: https://spec.openapis.org/oas/v3.1.0
- PostgreSQL: https://www.postgresql.org/docs/
- Prometheus: https://prometheus.io/docs/

# FINAL DEVELOPER HANDOFF

```text
Không bắt đầu bằng dashboard.
Không bắt đầu bằng shell script clone.
Không bỏ qua DB constraints/state machine.

Bắt đầu bằng:
1. domain + migrations
2. job lease + idempotency
3. Proxmox mock/adapter
4. resource reservation
5. state engine
6. PGW adapter
7. validation/evidence
8. system lab
```

> Gate quan trọng nhất: một request retry nhiều lần vẫn chỉ tạo đúng một VM và một resource set; mọi external side effect đều có reference, evidence và đường rollback.
