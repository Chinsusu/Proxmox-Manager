package domain

import (
	"encoding/json"
	"time"
)

// Template là bản ghi vm_templates — theo Phần VI mục 2.1 và Phần IV
// (Ubuntu Golden Template Specification). Một job chỉ được clone từ
// template ở state ACTIVE (Phần III mục 5.2).
type Template struct {
	ID               string
	Name             string
	Family           string
	Version          string
	OSFamily         string
	OSVersion        string
	Architecture     string
	PVEClusterID     string
	PVENode          string
	PVETemplateVMID  int
	Storage          string
	CloneModeAllowed []string
	SourceChecksum   string
	BuildManifest    json.RawMessage
	State            TemplateState
	ValidationStatus ValidationResult
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// IsUsable báo hiệu template có thể dùng cho job provisioning mới hay
// không — chỉ ACTIVE mới hợp lệ (Phần III mục 5.2, Phần IV mục 9).
func (t Template) IsUsable() bool {
	return t.State == TemplateActive
}
