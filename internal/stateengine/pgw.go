// Package stateengine implement transition registry, checkpoint,
// retry/backoff, rollback, quarantine theo Phần V (VM Lifecycle State
// Machine) — thay script tuyến tính bằng reconciler có contract rõ
// (ADR-003).
package stateengine

import (
	"context"
)

// ClientRequest khớp request contract PGW ở Phần VII mục 3.
type ClientRequest struct {
	Name       string
	IPCIDR     string
	MACAddress string
	VLANID     int
	Enabled    bool
	Metadata   map[string]string
}

// ClientRef tham chiếu một PGW client đã tạo.
type ClientRef struct {
	ID string
}

// MappingRequest tạo một PGW mapping cho một client.
type MappingRequest struct {
	ClientID string
	PolicyID string
}

// MappingRef tham chiếu một PGW mapping đã tạo.
type MappingRef struct {
	ID string
}

// Generation là desired_generation của một mapping sau khi activate
// (Phần VII mục 4).
type Generation int64

// EgressEvidence khớp proof contract ở Phần VII mục 5.
type EgressEvidence struct {
	ClientID          string
	MappingID         string
	Result            string
	CheckedAt         string
	IPv4              string
	IPv6              string
	Policy            string
	DirectLeakPackets int
	ProxyHealth       string
	RulesGeneration   int64
}

// PGWAdapter khớp nguyên văn interface ở Phần II mục 3.5. PGW là
// external dependency (ADR-006) — implementation thật thuộc epic
// P0-04, chưa có cluster PGW để verify tại thời điểm P0-05 này (xem
// NoopPGWAdapter).
type PGWAdapter interface {
	CreateClient(ctx context.Context, req ClientRequest) (ClientRef, error)
	CreateMapping(ctx context.Context, req MappingRequest) (MappingRef, error)
	ActivateMapping(ctx context.Context, id string) (Generation, error)
	SuspendMapping(ctx context.Context, id string) error
	DeleteMapping(ctx context.Context, id string) error
	EgressProof(ctx context.Context, clientID string) (EgressEvidence, error)
}

// simulatedMarker xuất hiện trong mọi giá trị NoopPGWAdapter trả về —
// đảm bảo không ai nhầm evidence giả với egress proof thật nếu nó lọt
// vào log/report.
const simulatedMarker = "SIMULATED-NOOP-NOT-REAL-PGW"

// NoopPGWAdapter là stub CHỈ dùng để chạy thử cơ chế state engine khi
// chưa có cluster PGW thật (epic P0-04 chưa triển khai). KHÔNG dùng
// trong production — mọi giá trị trả về đều đánh dấu rõ "SIMULATED"
// để không thể nhầm với egress proof thật.
type NoopPGWAdapter struct{}

// NewNoopPGWAdapter tạo một NoopPGWAdapter mới.
func NewNoopPGWAdapter() *NoopPGWAdapter { return &NoopPGWAdapter{} }

// CreateClient implement PGWAdapter — trả ClientRef giả, không gọi hệ
// thống thật nào.
func (n *NoopPGWAdapter) CreateClient(_ context.Context, req ClientRequest) (ClientRef, error) {
	return ClientRef{ID: "noop-client-" + req.Name}, nil
}

// CreateMapping implement PGWAdapter — trả MappingRef giả.
func (n *NoopPGWAdapter) CreateMapping(_ context.Context, req MappingRequest) (MappingRef, error) {
	return MappingRef{ID: "noop-mapping-" + req.ClientID}, nil
}

// ActivateMapping implement PGWAdapter — luôn trả generation=1.
func (n *NoopPGWAdapter) ActivateMapping(_ context.Context, _ string) (Generation, error) {
	return Generation(1), nil
}

// SuspendMapping implement PGWAdapter — no-op, luôn thành công.
func (n *NoopPGWAdapter) SuspendMapping(_ context.Context, _ string) error { return nil }

// DeleteMapping implement PGWAdapter — no-op, luôn thành công.
func (n *NoopPGWAdapter) DeleteMapping(_ context.Context, _ string) error { return nil }

// EgressProof implement PGWAdapter — trả evidence đánh dấu SIMULATED,
// không phải proof thật.
func (n *NoopPGWAdapter) EgressProof(_ context.Context, clientID string) (EgressEvidence, error) {
	return EgressEvidence{
		ClientID: clientID,
		Result:   simulatedMarker,
		Policy:   simulatedMarker,
	}, nil
}
