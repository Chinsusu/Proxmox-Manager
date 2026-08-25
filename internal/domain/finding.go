package domain

import (
	"encoding/json"
	"time"
)

// FindingSeverity là mức độ nghiêm trọng của một finding từ invariant
// scanner, theo Phần VI mục 11 và OpenAPI Finding schema.
type FindingSeverity string

// Các giá trị hợp lệ của FindingSeverity.
const (
	FindingInfo     FindingSeverity = "info"
	FindingWarning  FindingSeverity = "warning"
	FindingCritical FindingSeverity = "critical"
)

// FindingState là vòng đời xử lý một finding.
type FindingState string

// Các giá trị hợp lệ của FindingState.
const (
	FindingOpen         FindingState = "OPEN"
	FindingAcknowledged FindingState = "ACKNOWLEDGED"
	FindingRemediating  FindingState = "REMEDIATING"
	FindingResolved     FindingState = "RESOLVED"
)

// Finding là một bất thường do invariant scanner phát hiện — bản ghi
// findings. Kết quả không tự xoá; tạo remediation job có approval
// policy riêng, theo Phần VI mục 11.
type Finding struct {
	ID           string
	Category     string
	Severity     FindingSeverity
	ResourceType *string
	ResourceID   *string
	Summary      string
	Details      json.RawMessage
	State        FindingState
	DetectedAt   time.Time
	ResolvedAt   *time.Time
}
