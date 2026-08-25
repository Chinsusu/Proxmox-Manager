package domain

import (
	"encoding/json"
	"time"
)

// IdentityObservation là một lần thu thập guest facts sau boot — bản ghi
// identity_observations, theo Phần VI mục 2.8 và Phần VIII mục 3.
// MachineIDDigest là HMAC-SHA256 của /etc/machine-id (ADR-007), không
// bao giờ lưu raw machine-id (Phần II mục 11.3).
type IdentityObservation struct {
	ID                  string
	InstanceID          string
	Generation          int
	MachineIDDigest     string
	SSHHostFingerprint  string
	CloudInitInstanceID *string
	Hostname            string
	MACAddresses        []string
	IPAddresses         []string
	BootID              *string
	Facts               json.RawMessage
	ObservedAt          time.Time
}

// ValidationRun là kết quả một lần chạy ruleset identity/network/egress/
// workload/template — bản ghi validation_runs, theo Phần VI mục 2.9 và
// Phần VIII mục 1 (evidence phải audit được, không chỉ true/false).
type ValidationRun struct {
	ID             string
	InstanceID     string
	JobID          *string
	Type           string
	Result         ValidationResult
	RulesetVersion string
	Evidence       json.RawMessage
	StartedAt      time.Time
	FinishedAt     *time.Time
}
