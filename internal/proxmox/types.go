// Package proxmox implement ProxmoxAdapter (Phần II mục 3.5) theo
// Proxmox Provisioning Contract (Phần III). HTTP method và response
// shape trong package này đã verify trực tiếp qua schema thật
// (/pve-docs/api-viewer/apidoc.js) của một cluster Proxmox VE 9.1.6,
// không suy đoán từ tài liệu chung chung.
package proxmox

// VMRef định danh một VM trên một cluster/node cụ thể.
type VMRef struct {
	Node string
	VMID int
}

// TaskRef tham chiếu một task async Proxmox (UPID) — phải persist
// trước khi poll, theo External Side-effect Pattern (Phần II mục 7).
type TaskRef struct {
	Node string
	UPID string
}

// TaskStatus là kết quả đọc GET /nodes/{node}/tasks/{upid}/status —
// field khớp đúng response schema thật (status: running|stopped,
// exitstatus rỗng nếu chưa xong).
type TaskStatus struct {
	Status     string // "running" hoặc "stopped"
	ExitStatus string // rỗng nếu đang running; "OK" hoặc thông báo lỗi khi stopped
}

// Done báo hiệu task đã ở trạng thái terminal.
func (s TaskStatus) Done() bool { return s.Status == "stopped" }

// Success báo hiệu task terminal và thành công (exitstatus == "OK").
// Theo Phần III mục 5.3, task terminal success không tự động nghĩa là
// side effect đã đúng — caller vẫn phải verify VM object riêng.
func (s TaskStatus) Success() bool { return s.Done() && s.ExitStatus == "OK" }

// CloneRequest khớp request contract ở Phần III mục 5.1, tham số thật
// đã verify qua schema (newid/name/storage/target/pool/full).
type CloneRequest struct {
	SourceNode string
	SourceVMID int
	TargetNode string
	TargetVMID int
	Name       string
	Storage    string
	Pool       string
	FullClone  bool // Phần II ADR-008: full clone mặc định production
	// Description nên chứa external reference ổn định
	// (vmf.instance_id=..., vmf.job_id=..., vmf.template_version=...)
	// theo Phần III mục 5.2, để reconciler tìm lại VM nếu worker crash.
	Description string
}

// NetConfig là cấu hình một NIC — khớp Phần III mục 6 (P0 chỉ cho một
// NIC workload, NET-001 ở Phần VIII mục 5).
type NetConfig struct {
	Bridge   string
	Firewall bool
}

// DiskConfig là cấu hình đĩa chính — khớp Phần III mục 6.
type DiskConfig struct {
	SizeGB  int
	Discard bool
	SSD     bool
}

// ConfigureRequest khớp desired config JSON ở Phần III mục 6.
type ConfigureRequest struct {
	VMRef
	Cores    int
	Sockets  int
	MemoryMB int
	CPUType  string
	Agent    bool
	OnBoot   bool
	Net0     NetConfig
	// IPConfig0 là chuỗi cloud-init ipconfig0 thô (vd "ip=dhcp" hoặc
	// "ip=10.20.0.11/24,gw=10.20.0.1"). IPAM (P0-03) chịu trách nhiệm
	// quyết định giá trị; adapter chỉ truyền nguyên văn.
	IPConfig0 string
}

// VMObservedState là kết quả đọc GET /nodes/{node}/qemu/{vmid}/status/current,
// dùng để so sánh desired vs observed (Phần II mục 5.2).
type VMObservedState struct {
	VMRef
	Name   string
	Status string // "running" | "stopped" | ...
	Locked string // rỗng nếu VM không bị lock (Phần III mục 11: PVE_VM_LOCKED)
}

// IsRunning báo hiệu VM đang chạy.
func (s VMObservedState) IsRunning() bool { return s.Status == "running" }
