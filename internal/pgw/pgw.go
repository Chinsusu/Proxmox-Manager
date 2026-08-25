package pgw

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

// Adapter khớp nguyên văn interface ở Phần II mục 3.5. PGW là external
// dependency (ADR-006) — implementation thật (gọi API PGW staging/prod)
// thuộc epic P0-04, chưa có cluster PGW để verify tại thời điểm này
// (xem NoopAdapter). Interface đặt ở đây (thay vì trong stateengine hay
// validation) để cả hai package dùng chung một port, tránh trùng type
// (chuyển từ internal/stateengine/pgw.go sang đây ở epic P0-07 khi
// validation engine cũng cần EgressEvidence cho EGR rules).
type Adapter interface {
	CreateClient(ctx context.Context, req ClientRequest) (ClientRef, error)
	CreateMapping(ctx context.Context, req MappingRequest) (MappingRef, error)
	ActivateMapping(ctx context.Context, id string) (Generation, error)
	SuspendMapping(ctx context.Context, id string) error
	DeleteMapping(ctx context.Context, id string) error
	EgressProof(ctx context.Context, clientID string) (EgressEvidence, error)
}

// simulatedMarker xuất hiện trong mọi giá trị NoopAdapter trả về — đảm
// bảo không ai nhầm evidence giả với egress proof thật nếu nó lọt vào
// log/report.
const simulatedMarker = "SIMULATED-NOOP-NOT-REAL-PGW"

// NoopAdapter là stub CHỈ dùng để chạy thử state engine/validation engine
// khi chưa có cluster PGW thật (epic P0-04 chưa triển khai). KHÔNG dùng
// trong production — mọi giá trị trả về đều đánh dấu rõ "SIMULATED" để
// không thể nhầm với egress proof thật.
type NoopAdapter struct{}

// NewNoopAdapter tạo một NoopAdapter mới.
func NewNoopAdapter() *NoopAdapter { return &NoopAdapter{} }

// CreateClient implement Adapter — trả ClientRef giả, không gọi hệ
// thống thật nào.
func (n *NoopAdapter) CreateClient(_ context.Context, req ClientRequest) (ClientRef, error) {
	return ClientRef{ID: "noop-client-" + req.Name}, nil
}

// CreateMapping implement Adapter — trả MappingRef giả.
func (n *NoopAdapter) CreateMapping(_ context.Context, req MappingRequest) (MappingRef, error) {
	return MappingRef{ID: "noop-mapping-" + req.ClientID}, nil
}

// ActivateMapping implement Adapter — luôn trả generation=1.
func (n *NoopAdapter) ActivateMapping(_ context.Context, _ string) (Generation, error) {
	return Generation(1), nil
}

// SuspendMapping implement Adapter — no-op, luôn thành công.
func (n *NoopAdapter) SuspendMapping(_ context.Context, _ string) error { return nil }

// DeleteMapping implement Adapter — no-op, luôn thành công.
func (n *NoopAdapter) DeleteMapping(_ context.Context, _ string) error { return nil }

// EgressProof implement Adapter — trả evidence đánh dấu SIMULATED,
// không phải proof thật.
func (n *NoopAdapter) EgressProof(_ context.Context, clientID string) (EgressEvidence, error) {
	return EgressEvidence{
		ClientID: clientID,
		Result:   simulatedMarker,
		Policy:   simulatedMarker,
	}, nil
}
