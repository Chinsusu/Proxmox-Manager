// Package workload implement Adapter (WorkloadAdapter theo Phần II mục
// 3.5) generic — noop/sample adapter, artifact verification,
// install/health/remove, bounded log evidence. VM Factory không hiểu
// business semantics của workload; adapter map về contract chung (Phần
// VIII mục 7). Mọi thao tác trong guest đi qua allowlisted operation cố
// định (Phần II mục 11.4: install_artifact, service_status,
// service_restart, collect_bounded_logs) — KHÔNG nhận/chạy shell tuỳ ý.
package workload

import (
	"context"
	"encoding/json"

	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// Artifact là nội dung đã fetch/xác định checksum kỳ vọng TRƯỚC khi
// đưa cho Adapter — VM Factory (worker, có network bình thường) chịu
// trách nhiệm fetch từ registry/URL bên ngoài; Adapter chỉ nhận bytes
// đã sẵn sàng, không tự tải qua internet. Guest chạy trên bridge cô lập
// (chưa có PGW/egress thật — P0-04 chưa triển khai) nên artifact phải
// tới guest qua QGA file-write (proxmox.Adapter.WriteGuestFile), không
// qua guest tự curl. Content marshal JSON tự động thành base64 (hành vi
// mặc định encoding/json cho []byte) — an toàn khi nhúng vào WorkloadSpec.
type Artifact struct {
	Content []byte `json:"content"`
	// SHA256 là checksum hex kỳ vọng của Content — Adapter PHẢI so
	// khớp trước khi cài đặt (Phần VIII mục 7 "artifact version/
	// checksum"; acceptance test WRK-001: "Artifact checksum mismatch
	// → Do not execute; rollback").
	SHA256 string `json:"sha256"`
}

// ValidationReport là kết quả Validate.
type ValidationReport struct {
	Valid   bool
	Reasons []string
}

// HealthReport là kết quả Health — map từ service_status allowlisted
// operation (Phần II mục 11.4).
type HealthReport struct {
	Healthy      bool
	ServiceState string
	Detail       string
}

// Adapter khớp nguyên văn interface WorkloadAdapter ở Phần II mục 3.5.
// spec ở Install là json.RawMessage (khớp domain.VMInstance.WorkloadSpec)
// — VM Factory lưu opaque JSON, mỗi adapter tự định nghĩa và tự
// unmarshal shape riêng ("VM Factory không hiểu business semantics của
// workload", Phần VIII mục 7), không ép mọi adapter dùng chung một Go
// struct cụ thể.
type Adapter interface {
	Name() string
	Validate(ctx context.Context, vm proxmox.VMRef) (ValidationReport, error)
	Install(ctx context.Context, vm proxmox.VMRef, spec json.RawMessage) error
	Health(ctx context.Context, vm proxmox.VMRef) (HealthReport, error)
	Remove(ctx context.Context, vm proxmox.VMRef) error
}
