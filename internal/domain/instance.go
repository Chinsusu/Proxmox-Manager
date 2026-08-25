package domain

import (
	"encoding/json"
	"time"
)

// VMInstance là aggregate root cho lifecycle VM — bản ghi vm_instances,
// theo Phần II mục 5.1 và Phần VI mục 2.2.
//
// Invariants (Phần II mục 5.1, enforce bằng DB constraint ở tầng storage):
//  1. Một instance có tối đa một active VMID (unique partial index).
//  2. Một hostname active chỉ thuộc một instance (unique partial index).
//  3. READY phải có validation PASS cho identity/network/egress — domain
//     không tự set READY, chỉ state engine (epic P0-05) mới được phép.
type VMInstance struct {
	ID                string
	LogicalName       string
	Hostname          string
	State             InstanceState
	Generation        int
	TemplateID        string
	PVEClusterID      *string
	PVENode           *string
	VMID              *int
	ResourcePool      *string
	DesiredConfig     json.RawMessage
	DesiredConfigHash *string
	WorkloadAdapter   *string
	WorkloadSpec      json.RawMessage
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
	RetiredAt         *time.Time
}

// IsRetired báo hiệu instance đã kết thúc vòng đời — không được giữ
// active PGW mapping hoặc active IP allocation (Phần II mục 5.1, invariant 5).
func (i VMInstance) IsRetired() bool {
	return i.RetiredAt != nil
}
