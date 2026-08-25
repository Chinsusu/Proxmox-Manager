package workload

import (
	"context"
	"encoding/json"

	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// simulatedMarker xuất hiện trong mọi giá trị NoopAdapter trả về — đảm
// bảo không nhầm với workload thật nếu lọt vào log/evidence (cùng quy
// ước với pgw.NoopAdapter).
const simulatedMarker = "SIMULATED-NOOP-NOT-REAL-WORKLOAD"

// NoopAdapter là stub CHỈ dùng để chạy thử state engine khi chưa cần
// một workload thật (vd test pipeline REQUESTED→READY end-to-end mà
// không phụ thuộc SampleAdapter). KHÔNG dùng trong production.
type NoopAdapter struct{}

// NewNoopAdapter tạo một NoopAdapter mới.
func NewNoopAdapter() *NoopAdapter { return &NoopAdapter{} }

// Name implement Adapter.
func (n *NoopAdapter) Name() string { return "noop" }

// Validate implement Adapter — luôn hợp lệ, không chạm guest.
func (n *NoopAdapter) Validate(_ context.Context, _ proxmox.VMRef) (ValidationReport, error) {
	return ValidationReport{Valid: true, Reasons: []string{simulatedMarker}}, nil
}

// Install implement Adapter — no-op, luôn thành công.
func (n *NoopAdapter) Install(_ context.Context, _ proxmox.VMRef, _ json.RawMessage) error {
	return nil
}

// Health implement Adapter — luôn healthy, đánh dấu SIMULATED.
func (n *NoopAdapter) Health(_ context.Context, _ proxmox.VMRef) (HealthReport, error) {
	return HealthReport{Healthy: true, ServiceState: simulatedMarker, Detail: simulatedMarker}, nil
}

// Remove implement Adapter — no-op, luôn thành công.
func (n *NoopAdapter) Remove(_ context.Context, _ proxmox.VMRef) error { return nil }
