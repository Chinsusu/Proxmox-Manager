package workload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"time"

	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// sampleMarkerPath là nơi duy nhất lưu bằng chứng đã cài đặt trên
// guest — MỘT workload cho mỗi VM (khớp domain.VMInstance có đúng một
// cặp WorkloadAdapter/WorkloadSpec, không phải danh sách), nên không
// cần tham số hoá theo tên workload như một thiết kế ban đầu từng làm.
const sampleMarkerPath = "/etc/vmf-workload/install.json"

// SampleSpec là shape cụ thể của WorkloadSpec (JSONB) mà SampleAdapter
// hiểu — một workload systemd đơn giản. Adapter khác (ngoài phạm vi
// P0) tự định nghĩa spec JSON riêng; VM Factory chỉ lưu opaque JSON.
type SampleSpec struct {
	Name        string   `json:"name"`
	ServiceName string   `json:"service_name"`
	InstallPath string   `json:"install_path"`
	Artifact    Artifact `json:"artifact"`
}

// installMarker là bằng chứng đã cài đặt, ghi tại sampleMarkerPath —
// dùng cho idempotency (Phần II mục 6.3: "Workload install | Adapter
// version marker + checksum") và để Health/Remove/Validate tự đọc lại
// service_name/install_path mà KHÔNG cần spec truyền lại — SampleAdapter
// không giữ state giữa các lần gọi (constructible độc lập mỗi lần, vd
// health-check định kỳ ở một process worker khác lần Install).
type installMarker struct {
	Name        string `json:"name"`
	ServiceName string `json:"service_name"`
	InstallPath string `json:"install_path"`
	SHA256      string `json:"sha256"`
}

// SampleAdapter là workload adapter THẬT (không phải stub) cho một
// service systemd đơn giản — dùng làm ví dụ tham chiếu triển khai
// Adapter interface đúng theo Phần II mục 11.4 (chỉ allowlisted
// operation, không shell tuỳ ý): install_artifact (WriteGuestFile +
// chmod/systemctl cố định), service_status, service_restart,
// collect_bounded_logs.
type SampleAdapter struct {
	proxmox     *proxmox.Adapter
	execTimeout time.Duration
}

// NewSampleAdapter tạo SampleAdapter gắn với một *proxmox.Adapter —
// không cần spec (đọc lại từ marker trên guest khi cần, xem installMarker).
func NewSampleAdapter(adapter *proxmox.Adapter) *SampleAdapter {
	return &SampleAdapter{proxmox: adapter}
}

// Name implement Adapter.
func (s *SampleAdapter) Name() string { return "sample-systemd" }

func (s *SampleAdapter) timeout() time.Duration {
	if s.execTimeout > 0 {
		return s.execTimeout
	}
	return 30 * time.Second
}

// Install implement Adapter — theo transition 4.10 APPLYING_WORKLOAD
// (Phần V): verify checksum TRƯỚC khi chạm guest (WRK-001 "Artifact
// checksum mismatch → Do not execute"), rồi push + cài qua các bước
// allowlisted cố định. Idempotent: nếu marker guest đã ghi đúng
// checksum này, bỏ qua toàn bộ (không cài lại, không restart service).
func (s *SampleAdapter) Install(ctx context.Context, vm proxmox.VMRef, specJSON json.RawMessage) error {
	var spec SampleSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return fmt.Errorf("workload: unmarshal sample spec: %w", err)
	}

	actual := sha256.Sum256(spec.Artifact.Content)
	actualHex := hex.EncodeToString(actual[:])
	if actualHex != spec.Artifact.SHA256 {
		return fmt.Errorf("workload: artifact checksum mismatch: expected %s, got %s (WRK-001: khong thuc thi)", spec.Artifact.SHA256, actualHex)
	}

	if existing, err := s.readMarker(ctx, vm); err == nil &&
		existing.SHA256 == spec.Artifact.SHA256 && existing.ServiceName == spec.ServiceName {
		return nil // da cai dung phien ban nay, idempotent skip
	}

	unitPath := "/etc/systemd/system/" + spec.ServiceName + ".service"
	// path.Dir (KHÔNG phải path/filepath.Dir) — spec.InstallPath là
	// đường dẫn TRÊN GUEST Linux, không phải path hệ điều hành đang
	// chạy worker; filepath.Dir trên Windows đổi "/" thành "\", tạo
	// nhầm thư mục trên guest ("no such file or directory" khi write
	// artifact sau đó) — phát hiện khi verify thật trên cluster.
	dirs := []string{path.Dir(spec.InstallPath), path.Dir(sampleMarkerPath)}
	for _, d := range dirs {
		if err := s.mustExec(ctx, vm, []string{"mkdir", "-p", d}); err != nil {
			return fmt.Errorf("workload: mkdir %s: %w", d, err)
		}
	}

	if err := s.proxmox.WriteGuestFile(ctx, vm, spec.InstallPath, spec.Artifact.Content); err != nil {
		return fmt.Errorf("workload: write artifact: %w", err)
	}
	if err := s.mustExec(ctx, vm, []string{"chmod", "+x", spec.InstallPath}); err != nil {
		return fmt.Errorf("workload: chmod artifact: %w", err)
	}

	unit := buildSystemdUnit(spec)
	if err := s.proxmox.WriteGuestFile(ctx, vm, unitPath, []byte(unit)); err != nil {
		return fmt.Errorf("workload: write systemd unit: %w", err)
	}
	if err := s.mustExec(ctx, vm, []string{"systemctl", "daemon-reload"}); err != nil {
		return fmt.Errorf("workload: daemon-reload: %w", err)
	}
	if err := s.mustExec(ctx, vm, []string{"systemctl", "enable", "--now", spec.ServiceName}); err != nil {
		return fmt.Errorf("workload: enable service: %w", err)
	}

	marker, err := json.Marshal(installMarker{
		Name: spec.Name, ServiceName: spec.ServiceName, InstallPath: spec.InstallPath, SHA256: spec.Artifact.SHA256,
	})
	if err != nil {
		return fmt.Errorf("workload: marshal install marker: %w", err)
	}
	if err := s.proxmox.WriteGuestFile(ctx, vm, sampleMarkerPath, marker); err != nil {
		return fmt.Errorf("workload: write install marker: %w", err)
	}
	return nil
}

// buildSystemdUnit sinh nội dung unit cố định — không nội suy giá trị
// từ input người dùng ngoài spec.Name/InstallPath (đã qua SampleSpec do
// VM Factory kiểm soát, không phải raw command từ request).
func buildSystemdUnit(spec SampleSpec) string {
	return fmt.Sprintf(`[Unit]
Description=vm-factory sample workload: %s
After=network.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure

[Install]
WantedBy=multi-user.target
`, spec.Name, spec.InstallPath)
}

// Validate implement Adapter — xác nhận marker tồn tại và tự nhất quán
// trên guest (đã ghi bởi Install thành công) (Phần VIII mục 7 "artifact
// version/checksum"). Không nhận spec (đúng chữ ký gốc Phần II mục 3.5).
func (s *SampleAdapter) Validate(ctx context.Context, vm proxmox.VMRef) (ValidationReport, error) {
	marker, err := s.readMarker(ctx, vm)
	if err != nil {
		return ValidationReport{Valid: false, Reasons: []string{fmt.Sprintf("read install marker: %v", err)}}, nil
	}
	var reasons []string
	if marker.SHA256 == "" || marker.ServiceName == "" {
		reasons = append(reasons, "install marker thieu sha256/service_name")
	}
	return ValidationReport{Valid: len(reasons) == 0, Reasons: reasons}, nil
}

// Health implement Adapter — service_status allowlisted operation
// (Phần II mục 11.4): `systemctl is-active`, service_name đọc từ marker.
func (s *SampleAdapter) Health(ctx context.Context, vm proxmox.VMRef) (HealthReport, error) {
	marker, err := s.readMarker(ctx, vm)
	if err != nil {
		return HealthReport{}, fmt.Errorf("workload: read marker for health: %w", err)
	}
	result, err := s.proxmox.WaitExec(ctx, vm, []string{"systemctl", "is-active", marker.ServiceName}, s.timeout())
	if err != nil {
		return HealthReport{}, fmt.Errorf("workload: service_status: %w", err)
	}
	state := trimTrailingNewline(result.Stdout)
	if state == "" {
		state = trimTrailingNewline(result.Stderr)
	}
	return HealthReport{
		Healthy:      result.ExitCode == 0 && state == "active",
		ServiceState: state,
		Detail:       fmt.Sprintf("exit_code=%d", result.ExitCode),
	}, nil
}

// Restart chạy service_restart allowlisted operation — không thuộc
// Adapter interface gốc (Phần II mục 3.5 không có method này) nhưng
// nằm trong danh sách allowlist Phần II mục 11.4, cung cấp thêm cho
// caller cần recovery theo policy (acceptance test WRK-002: "Install
// succeeds, health fails → Retry/rollback by adapter policy").
func (s *SampleAdapter) Restart(ctx context.Context, vm proxmox.VMRef) error {
	marker, err := s.readMarker(ctx, vm)
	if err != nil {
		return fmt.Errorf("workload: read marker for restart: %w", err)
	}
	return s.mustExec(ctx, vm, []string{"systemctl", "restart", marker.ServiceName})
}

// CollectBoundedLogs chạy collect_bounded_logs allowlisted operation
// (Phần II mục 11.4, Phần VIII mục 7 "bounded log evidence") — giới
// hạn số dòng để không biến bằng chứng thành log dump không giới hạn
// (Phần II mục 3.4: "Không dùng database như log store dung lượng lớn").
func (s *SampleAdapter) CollectBoundedLogs(ctx context.Context, vm proxmox.VMRef, maxLines int) (string, error) {
	marker, err := s.readMarker(ctx, vm)
	if err != nil {
		return "", fmt.Errorf("workload: read marker for logs: %w", err)
	}
	if maxLines <= 0 {
		maxLines = 200
	}
	result, err := s.proxmox.WaitExec(ctx, vm,
		[]string{"journalctl", "-u", marker.ServiceName, "-n", strconv.Itoa(maxLines), "--no-pager"}, s.timeout())
	if err != nil {
		return "", fmt.Errorf("workload: collect_bounded_logs: %w", err)
	}
	return result.Stdout, nil
}

// Remove implement Adapter — dừng+disable service, xoá unit/artifact/
// marker. Chưa từng cài (marker không tồn tại) coi là thành công ngay
// (Phần II mục 6.3 "treat not-found as success" áp dụng cho Delete nói
// chung). daemon-reload cuối cùng LUÔN phải thành công (systemd luôn
// tồn tại trên golden template) — lỗi ở đây mới coi là lỗi thật.
func (s *SampleAdapter) Remove(ctx context.Context, vm proxmox.VMRef) error {
	marker, err := s.readMarker(ctx, vm)
	if err != nil {
		return nil
	}
	_ = s.mustExec(ctx, vm, []string{"systemctl", "disable", "--now", marker.ServiceName})
	_ = s.mustExec(ctx, vm, []string{"rm", "-f", "/etc/systemd/system/" + marker.ServiceName + ".service"})
	_ = s.mustExec(ctx, vm, []string{"rm", "-f", marker.InstallPath})
	_ = s.mustExec(ctx, vm, []string{"rm", "-f", sampleMarkerPath})
	if err := s.mustExec(ctx, vm, []string{"systemctl", "daemon-reload"}); err != nil {
		return fmt.Errorf("workload: daemon-reload after remove: %w", err)
	}
	return nil
}

// readMarker đọc + parse install marker từ guest — trả error nếu
// marker không tồn tại hoặc không parse được.
func (s *SampleAdapter) readMarker(ctx context.Context, vm proxmox.VMRef) (installMarker, error) {
	result, err := s.proxmox.WaitExec(ctx, vm, []string{"cat", sampleMarkerPath}, s.timeout())
	if err != nil {
		return installMarker{}, fmt.Errorf("read marker: %w", err)
	}
	if result.ExitCode != 0 {
		return installMarker{}, fmt.Errorf("read marker: exit code %d: %s", result.ExitCode, result.Stderr)
	}
	var m installMarker
	if err := json.Unmarshal([]byte(result.Stdout), &m); err != nil {
		return installMarker{}, fmt.Errorf("parse marker: %w", err)
	}
	return m, nil
}

// mustExec chạy một allowlisted command cố định, trả error nếu exec
// thất bại hoặc exit code khác 0.
func (s *SampleAdapter) mustExec(ctx context.Context, vm proxmox.VMRef, command []string) error {
	result, err := s.proxmox.WaitExec(ctx, vm, command, s.timeout())
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("command %v exited %d: %s", command, result.ExitCode, result.Stderr)
	}
	return nil
}

func trimTrailingNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
