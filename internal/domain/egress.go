package domain

import (
	"encoding/json"
	"time"
)

// EgressBinding là ràng buộc một instance với một PGW client/mapping —
// bản ghi egress_bindings, theo Phần VI mục 2.5 và Phần VII (PGW
// Integration Contract). Chỉ có tối đa một active binding cho mỗi
// instance (partial unique index PENDING/ACTIVE/SUSPENDED).
type EgressBinding struct {
	ID                string
	InstanceID        string
	PGWClientID       *string
	PGWMappingID      *string
	PGWPolicyID       *string
	State             string
	ExpectedExit      json.RawMessage
	DesiredGeneration *int64
	LastProofAt       *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
