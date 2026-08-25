# Test Lab & Chaos (P0-11, phần không phụ thuộc PGW)

Mục tiêu: làm cho `docs/appendices/acceptance_test_matrix.csv` (35 dòng)
có bằng chứng test thật thay vì chỉ là kế hoạch. Phiên làm việc này
**không** làm phần cần PGW staging thật (PGW-001..004, và PGW chaos ở
docs/09 mục 8.3) — xem lý do ở [MEMORY]/hội thoại: P0-11 dừng ở phần
không cần cluster PGW thật.

## `testlab/proxmoxmock`

Mock Proxmox API (`httptest.Server`) implement đúng tập endpoint
`internal/proxmox` gọi — đủ để lái `stateengine` handler qua các kịch
bản chaos PVE-xxx mà KHÔNG cần cluster Proxmox thật. Test dùng mock này
chỉ cần `DATABASE_URL` (như mọi integration test khác trong repo) —
**chạy được trong CI công khai**, khác hẳn `TestEngine_FullPipeline_RealCluster`
(cần `PVE_BASE_URL` thật, chỉ chạy thủ công).

## Coverage map — `docs/appendices/acceptance_test_matrix.csv`

| ID | Trạng thái | Bằng chứng |
|---|---|---|
| FNC-001 | Một phần | `internal/stateengine/engine_integration_test.go:TestEngine_FullPipeline_RealCluster` chạy hết REQUESTED→WAITING_GUEST thật trên cluster thật; dừng ở QUARANTINED (không phải READY) vì `pgw.NoopAdapter` không rubber-stamp evidence giả — đúng thiết kế, không phải bug |
| FNC-002/003 | Có test | `internal/httpapi/idempotency_integration_test.go` (P0-09) |
| RES-001 | Có test | `internal/ipam/repository_integration_test.go:TestRepository_ReserveNextFree_ConcurrentWorkers_NoDuplicateAddress` (P0-03) |
| RES-002 | Có test | `internal/ipam/repository_integration_test.go:TestRepository_ReserveNextFree_ExhaustedReturnsCapacityError` (P0-03) |
| PVE-001 | Có test (mới) | `internal/stateengine/chaos_integration_test.go:TestChaos_PVE001_VMIDConflict_FailsControlledNoDuplicate` |
| PVE-002 | **Blocked** | Cần reconciliation qua external tag (CloningHandler chưa có discovery API) — gap đã biết, ghi ở comment `handlers_provision.go` |
| PVE-003 | Có test (mới) | `chaos_integration_test.go:TestChaos_PVE003_WorkerDiesAfterClone_ResumeAttachesExistingTask` |
| PVE-004 | Có test (mới) | `chaos_integration_test.go:TestChaos_PVE004_BridgeMissing_FailsControlled` |
| PVE-005 | Có test (mới) | `chaos_integration_test.go:TestChaos_PVE005_StorageFull_FailsControlled` |
| PVE-006 | Có test (mới) | `chaos_integration_test.go:TestChaos_PVE006_VMAlreadyRunning_NoDuplicateStart` |
| PGW-001..004 | **Loại trừ phiên này** | Cần PGW staging thật (P0-04) |
| GST-001 | **Blocked — gap mới phát hiện** | "QGA unavailable, SSH works" giả định có SSH fallback thật, nhưng `internal/guest` chỉ có QGA-based collector — SSH fallback (ADR-005, doc nói "triển khai ở P0-02") **chưa từng được implement**, dù `config.GuestConfig.SSHFallback/SSHUser/SSHPrivateKeyFile` đã có trong config schema. Không tự ý implement trong phiên này — scope P0-02, không phải P0-11 |
| GST-002 | Chưa có test | cloud-init terminal failure — cần cluster thật hoặc mock guest layer riêng, chưa làm |
| ID-001/002, NET-001/002/003 | Có test | `internal/validation/rules_identity_network_test.go` (P0-07) |
| WRK-001/002 | Có test | `internal/workload/sample_test.go` (P0-08) |
| JOB-001 | Có test | `internal/jobs/repository_integration_test.go:TestRepository_ReclaimExpiredLeases` (P0-01) |
| JOB-002 | Có test | `internal/jobs/repository_integration_test.go:TestRepository_Claim_ConcurrentWorkers_NoDuplicateOwnership` (P0-01/P0-05) |
| RBK-001 | **Không test — gap mới phát hiện** | "VM delete not found → treat as success after verify" (docs/03 mục 9.2) chưa implement: `internal/proxmox/errors.go` không có mã lỗi riêng cho "not found" (chỉ Unknown/AuthFailed/VMIDConflict/BridgeNotFound/StorageCapacity/VMLocked/TemplateInvalid) nên `Rollback.Execute` hiện coi MỌI lỗi Delete() là failure → QUARANTINED, không phân biệt được. Cần biết đúng chuỗi lỗi thật Proxmox trả cho VM không tồn tại — **không verify được trong phiên này (không có cluster thật)**, không đoán mò |
| RBK-002 | Có test (mới) | `internal/stateengine/rollback_integration_test.go:TestChaos_RBK002_PGWDeleteFails_QuarantinesWithLeftoverResource` — viết test này lộ ra VÀ đã sửa 1 bug thật: `Rollback.Execute` từng vẫn `Release()` IP dù bước trước đó (PGW delete) đã fail, có thể làm IP bị tái cấp phát trong khi PGW mapping cũ còn treo (leak thật). Đã fix: không release IP nếu đã có failure trước đó, giữ nguyên leftover cùng nhau |
| DEC-001/002 | **Blocked** | `stateengine` chưa có transition handler chain cho DRAINING→RETIRED (gap đã biết từ lúc nối worker loop — xem PR #9) |
| OBS-001 | Bao phủ gián tiếp | `observability.WithCorrelation` + `audit.Append` trong MỌI transition (Engine.Step) + metrics (P0-10) đã wiring theo construction; không có test end-to-end riêng khẳng định "mọi transition" |
| SEC-001 | Có test | CI job `security` (gitleaks) chạy trên mọi PR |
| SEC-002 | Có test (mới) | `internal/workload/sample_test.go:TestSampleAdapter_Health_MaliciousServiceName_NoShellInjection` — xác nhận ServiceName chứa ký tự shell đặc biệt vẫn đi qua QGA nguyên vẹn dưới dạng MỘT phần tử argv, không có điểm nào trong code nối chuỗi thành shell command |
| DR-001 | P1, chưa làm | Cần drift scanner (Epic P0-11 phần orphan scanner) |
| PERF-001 | P1, chưa làm | Cần cluster thật + load test — ngoài phạm vi phiên này |

## Chaos Plan (docs/09 mục 8) — mapping

- **8.1 Worker chaos**: "hai worker race cùng job" = JOB-002 (đã có test). "kill -9 giữa external request và checkpoint" tương ứng PVE-003 (đã có test qua mock — mô phỏng bằng cách KHÔNG gọi lại Clone(), chỉ resume từ checkpoint có sẵn). "DB connection reset"/"lease expiration" = JOB-001 (đã có test qua `ReclaimExpiredLeases`).
- **8.2 Proxmox chaos**: PVE-001/004/005/006 đã có test qua mock (API 502/timeout tương ứng lỗi injected; VM lock/node reboot/template missing chưa có kịch bản test riêng — có thể mở rộng `proxmoxmock` sau nếu cần).
- **8.3 PGW chaos**: loại trừ phiên này (P0-04).
- **8.4 Guest chaos**: GST-001 blocked (SSH fallback chưa implement), GST-002 chưa có test.

## Gaps mới phát hiện trong phiên này (ngoài phạm vi sửa ở đây)

1. **SSH fallback (GST-001)** chưa từng được implement dù có trong config schema — thuộc scope P0-02, cần quyết định riêng có làm hay bỏ config field.
2. **RBK-001** cần biết chuỗi lỗi thật Proxmox trả cho "VM not found" để phân loại đúng — cần verify trên cluster thật.
3. **PVE-002** cần reconciliation qua external tag (discovery API) — đã biết từ P0-05, chưa đổi.
