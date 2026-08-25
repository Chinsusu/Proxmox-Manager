# VM Factory & Fleet Control Plane - Dev-ready Documentation Package v1.2

Ngày chốt v1.0: 25/08/2026. Bản v1.1/v1.2 cập nhật ngày 25/08/2026.

## Changelog v1.2

- Thêm `11_Engineering_Standards_and_Git_Workflow_v1.0.md`: coding standard (Go), branching model, commit convention, PR workflow/checklist, merge strategy, CI pipeline (map với Release Gates ở tài liệu 09), build/release, local dev workflow, Definition of Done cấp PR.

## Changelog v1.1

- **SQL schema**: tách enum `job_state` (`QUEUED|RUNNING|RETRY_WAIT|SUCCEEDED|FAILED|CANCELLED`) khỏi `instance_state` — sửa lỗi query claim job dùng `'QUEUED'` không tồn tại trong enum cũ; `provisioning_jobs.state` chuyển sang `job_state`, `checkpoint` giữ vị trí lifecycle.
- **SQL schema**: bổ sung các bảng đã mô tả trong Phần VI nhưng thiếu DDL: `pve_nodes`, `pve_storages`, `capacity_snapshots`, `secrets`, `hostname_sequences`; chú thích workload slot dùng `resource_locks`; chú thích digest index không unique là chủ ý (policy application-level).
- **OpenAPI 1.1.0**: thêm `POST /v1/jobs/{id}/retry` (khớp CLI `vmf job retry`); định nghĩa response schemas (`Template`, `Instance`, `Job`, `JobEvent`, `ValidationRun`, `NetworkSegment`, `Finding`, `InstanceState`, `JobState`) và gắn vào các GET endpoint.
- **Config example**: thay `identity.duplicate_scope` bằng `identity.duplicate_policy` (`active_fleet: block`, `retired_history: warn`) khớp Phần VIII mục 10 và Phần VI mục 2.8.
- **Tài liệu 02/06 + Master Blueprint (md)**: chú thích rõ job state là state machine riêng.
- **Đã xóa**: `VM_Factory_Master_Blueprint_v1.0.docx` — bản Word chưa render lại nên nội dung lệch với v1.1; Markdown là nguồn chuẩn duy nhất. Cần bản Word thì render lại từ `VM_Factory_Master_Blueprint_v1.0.md`.

## Mục tiêu

Bộ hồ sơ này mô tả dự án `vm-factory` độc lập: tạo, cấu hình, xác minh, vận hành và thu hồi Linux VM trên Proxmox. PGW chỉ là external API dependency. Workload được adapter hóa, không hard-code một ứng dụng cụ thể.

## Bản đọc tổng hợp

- `VM_Factory_Master_Blueprint_v1.0.md` — nguồn Markdown tổng hợp, là bản chuẩn duy nhất.

## Tài liệu chính

1. `01_Project_Charter_and_PRD_v1.0.md`
2. `02_System_Architecture_and_Technical_Design_v1.0.md`
3. `03_Proxmox_Provisioning_Contract_v1.0.md`
4. `04_Ubuntu_2204_Golden_Template_Specification_v1.0.md`
5. `05_VM_Lifecycle_State_Machine_v1.0.md`
6. `06_Data_Model_IPAM_and_Resource_Registry_v1.0.md`
7. `07_PGW_Integration_Contract_v1.0.md`
8. `08_Identity_Network_Egress_Validation_v1.0.md`
9. `09_Observability_Alerting_and_Test_Plan_v1.0.md`
10. `10_Operations_Runbook_and_Implementation_Roadmap_v1.0.md`
11. `11_Engineering_Standards_and_Git_Workflow_v1.0.md`

## Appendices

- OpenAPI 3.1 YAML.
- PostgreSQL schema blueprint.
- Config example.
- Acceptance test matrix.
- Risk register.
- Official references.
- Architecture/state/sequence/ERD diagrams.
- ADR set.

## Implementation order

```text
P0-00 Foundation
→ P0-01 Domain/DB
→ P0-02 Proxmox Adapter
→ P0-03 IPAM
→ P0-04 PGW Adapter
→ P0-05 State Engine
→ P0-06 Template Tooling
→ P0-07 Validation
→ P0-08 Workload Adapter
→ P0-09 API/CLI
→ P0-10 Observability
→ P0-11 Test/Chaos
→ P0-12 Deployment/Ops
```
