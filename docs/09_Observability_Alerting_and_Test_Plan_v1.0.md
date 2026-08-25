---
title: "Observability, Alerting & Verification Plan"
subtitle: "Metrics, logs, dashboards, SLO, functional/integration/chaos/security tests"
author: "VM Factory Engineering"
date: "2026-08-25"
lang: vi-VN
---

**Loại tài liệu:** Operations and Test Specification  
**Repository mục tiêu:** `vm-factory`  
**Phiên bản tài liệu:** 1.0  
**Trạng thái:** Dev-ready proposal  
**Ngày chốt:** 25/08/2026

> VM Factory & Fleet Control Plane là một control plane độc lập để tạo, cấu hình, xác minh, vận hành và thu hồi Linux VM trên Proxmox. PGW được tích hợp qua API như một dependency bên ngoài; VM Factory không chứa logic data-plane của PGW và không phụ thuộc vào bất kỳ workload cụ thể nào.


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
