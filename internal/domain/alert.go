package domain

import "time"

// AlertStatus là vòng đời một alert persisted (UI integration,
// API_UI_Gap_Register mục 3.5) — đối tác vm-factory-native của một
// phần alert rule Prometheus (deploy/observability/prometheus-rules.yml),
// KHÔNG thay thế Alertmanager.
type AlertStatus string

// Các giá trị hợp lệ của AlertStatus.
const (
	AlertFiring       AlertStatus = "firing"
	AlertAcknowledged AlertStatus = "acknowledged"
	AlertResolved     AlertStatus = "resolved"
)

// Alert là một bản ghi bảng alerts.
type Alert struct {
	ID               string
	Fingerprint      string
	Status           AlertStatus
	Severity         string
	ResourceType     string
	ResourceID       string
	Title            string
	Description      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	AcknowledgedAt   *time.Time
	AcknowledgedBy   *string
	AcknowledgedNote *string
	Version          int
}
