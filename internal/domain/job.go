package domain

import (
	"encoding/json"
	"time"
)

// ProvisioningJob là một job lease trong hàng đợi PostgreSQL — bản ghi
// provisioning_jobs, theo Phần II mục 6. State dùng JobState riêng biệt
// khỏi InstanceState; Checkpoint giữ vị trí lifecycle của instance mà
// job đang xử lý (docs/02 mục 6.1, sửa v1.1).
type ProvisioningJob struct {
	ID             string
	InstanceID     string
	Operation      JobOperation
	State          JobState
	Checkpoint     InstanceState
	CheckpointData json.RawMessage
	Priority       int
	Attempt        int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LeaseOwner     *string
	LeaseExpiresAt *time.Time
	ErrorCode      *string
	ErrorMessage   *string
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

// IsLeased báo hiệu job đang được một worker giữ lease còn hiệu lực.
func (j ProvisioningJob) IsLeased(now time.Time) bool {
	return j.LeaseOwner != nil && j.LeaseExpiresAt != nil && now.Before(*j.LeaseExpiresAt)
}

// IsClaimable báo hiệu job đủ điều kiện để một worker claim — khớp
// điều kiện WHERE trong query claim job (docs/02 mục 6.1).
func (j ProvisioningJob) IsClaimable(now time.Time) bool {
	if j.State != JobQueued && j.State != JobRetryWait {
		return false
	}
	return !j.NextAttemptAt.After(now)
}

// ExternalTask lưu tham chiếu task async từ Proxmox/PGW — bản ghi
// external_tasks, theo Phần VI mục 2.7. Bắt buộc phải persist trước khi
// poll, theo External Side-effect Pattern (Phần II mục 7).
type ExternalTask struct {
	ID           string
	JobID        string
	System       string
	Operation    string
	ExternalID   string
	Status       string
	RequestHash  *string
	Result       json.RawMessage
	StartedAt    time.Time
	LastPolledAt *time.Time
	FinishedAt   *time.Time
}

// HostnameSequence theo dõi bộ đếm sinh hostname {prefix}-{sequence:04d}
// (Phần II mục 8.3) — bản ghi hostname_sequences.
type HostnameSequence struct {
	Prefix    string
	NextValue int
	UpdatedAt time.Time
}
