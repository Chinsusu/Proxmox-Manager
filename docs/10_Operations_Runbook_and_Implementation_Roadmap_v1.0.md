---
title: "VM Factory - Operations Runbook & Implementation Roadmap"
subtitle: "Triển khai, vận hành, sự cố, backup/restore, rollout và backlog dev-ready"
author: "VM Factory Engineering"
date: "2026-08-25"
lang: vi-VN
---

**Loại tài liệu:** Runbook + Delivery Plan  
**Repository mục tiêu:** `vm-factory`  
**Phiên bản tài liệu:** 1.0  
**Trạng thái:** Dev-ready proposal  
**Ngày chốt:** 25/08/2026

> VM Factory & Fleet Control Plane là một control plane độc lập để tạo, cấu hình, xác minh, vận hành và thu hồi Linux VM trên Proxmox. PGW được tích hợp qua API như một dependency bên ngoài; VM Factory không chứa logic data-plane của PGW và không phụ thuộc vào bất kỳ workload cụ thể nào.


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
