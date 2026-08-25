package domain

// TemplateState là vòng đời template theo Phần III mục 9 của bộ tài liệu
// thiết kế (docs/04_Ubuntu_2204_Golden_Template_Specification_v1.0.md).
type TemplateState string

// Các giá trị hợp lệ của TemplateState, khớp enum template_state trong
// migrations/000001_init.up.sql.
const (
	TemplateDraft      TemplateState = "DRAFT"
	TemplateCandidate  TemplateState = "CANDIDATE"
	TemplateActive     TemplateState = "ACTIVE"
	TemplateDeprecated TemplateState = "DEPRECATED"
	TemplateRevoked    TemplateState = "REVOKED"
)

// InstanceState là VM lifecycle state machine theo Phần V (docs/05).
type InstanceState string

// Các giá trị hợp lệ của InstanceState, khớp enum instance_state.
const (
	InstanceRequested          InstanceState = "REQUESTED"
	InstanceReserving          InstanceState = "RESERVING"
	InstanceCloning            InstanceState = "CLONING"
	InstanceConfiguring        InstanceState = "CONFIGURING"
	InstanceNetworkBinding     InstanceState = "NETWORK_BINDING"
	InstanceBooting            InstanceState = "BOOTING"
	InstanceWaitingGuest       InstanceState = "WAITING_GUEST"
	InstanceValidatingIdentity InstanceState = "VALIDATING_IDENTITY"
	InstanceValidatingEgress   InstanceState = "VALIDATING_EGRESS"
	InstanceApplyingWorkload   InstanceState = "APPLYING_WORKLOAD"
	InstanceReady              InstanceState = "READY"
	InstanceRetryWait          InstanceState = "RETRY_WAIT"
	InstanceDegraded           InstanceState = "DEGRADED"
	InstanceQuarantined        InstanceState = "QUARANTINED"
	InstanceRollingBack        InstanceState = "ROLLING_BACK"
	InstanceFailed             InstanceState = "FAILED"
	InstanceDraining           InstanceState = "DRAINING"
	InstanceDecommissioning    InstanceState = "DECOMMISSIONING"
	InstanceReleasingResources InstanceState = "RELEASING_RESOURCES"
	InstanceRetired            InstanceState = "RETIRED"
)

// JobOperation là loại thao tác một provisioning job thực hiện.
type JobOperation string

// Các giá trị hợp lệ của JobOperation, khớp enum job_operation.
const (
	JobOpProvision    JobOperation = "PROVISION"
	JobOpRetry        JobOperation = "RETRY"
	JobOpRebuild      JobOperation = "REBUILD"
	JobOpQuarantine   JobOperation = "QUARANTINE"
	JobOpDecommission JobOperation = "DECOMMISSION"
	JobOpReconcile    JobOperation = "RECONCILE"
)

// JobState là state machine thực thi job, tách khỏi InstanceState theo
// quyết định ở docs/02 mục 6.1 (sửa v1.1) — checkpoint mới là nơi lưu vị
// trí lifecycle của instance mà job đang xử lý.
type JobState string

// Các giá trị hợp lệ của JobState, khớp enum job_state.
const (
	JobQueued    JobState = "QUEUED"
	JobRunning   JobState = "RUNNING"
	JobRetryWait JobState = "RETRY_WAIT"
	JobSucceeded JobState = "SUCCEEDED"
	JobFailed    JobState = "FAILED"
	JobCancelled JobState = "CANCELLED"
)

// AllocationState là vòng đời một địa chỉ IPv4 trong IPAM, theo Phần VI
// mục 3.1: FREE → RESERVED → ASSIGNED → QUARANTINED → RELEASED.
type AllocationState string

// Các giá trị hợp lệ của AllocationState, khớp enum allocation_state.
const (
	AllocationFree        AllocationState = "FREE"
	AllocationReserved    AllocationState = "RESERVED"
	AllocationAssigned    AllocationState = "ASSIGNED"
	AllocationQuarantined AllocationState = "QUARANTINED"
	AllocationReleased    AllocationState = "RELEASED"
)

// ValidationResult là kết quả một validation run (identity/network/egress/
// workload/template), theo Phần VIII mục 8.
type ValidationResult string

// Các giá trị hợp lệ của ValidationResult, khớp enum validation_result.
const (
	ValidationPass    ValidationResult = "PASS"
	ValidationWarn    ValidationResult = "WARN"
	ValidationFail    ValidationResult = "FAIL"
	ValidationUnknown ValidationResult = "UNKNOWN"
)
