package ipam

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// Scanner chạy các invariant check liên quan IPAM/instance, theo Phần
// VI mục 11 ("Data Quality Checks"). Không tự sửa dữ liệu — chỉ tạo
// Finding, remediation là quyết định vận hành riêng (Phần VI mục 11:
// "Kết quả không tự delete; tạo finding và remediation job có approval
// policy").
//
// Chỉ implement các check khả thi thuần từ dữ liệu đã có tại P0-03
// (instance/IP/job). Check liên quan PGW/identity (Phần VI mục 11:
// "PGW mapping missing/orphaned", "duplicate VMID/IP/hostname/digest")
// cần dữ liệu từ P0-04/P0-07, chưa thêm ở đây để tránh false positive
// trên dữ liệu chưa tồn tại.
type Scanner struct {
	db       *storage.DB
	findings *storage.FindingRepository
}

// NewScanner tạo Scanner gắn với một *storage.DB và FindingRepository.
func NewScanner(db *storage.DB, findings *storage.FindingRepository) *Scanner {
	return &Scanner{db: db, findings: findings}
}

// Scan chạy toàn bộ check và ghi Finding cho bất thường mới phát hiện.
// Trả số finding MỚI được tạo (finding đã OPEN từ lần scan trước
// không tính lại, theo unique index uq_findings_open).
func (s *Scanner) Scan(ctx context.Context) (int, error) {
	created := 0

	n, err := s.checkReadyInstanceWithoutAssignedIP(ctx)
	if err != nil {
		return created, fmt.Errorf("ipam: scan ready_instance_without_assigned_ip: %w", err)
	}
	created += n

	n, err = s.checkOrphanedIPAllocation(ctx)
	if err != nil {
		return created, fmt.Errorf("ipam: scan orphaned_ip_allocation: %w", err)
	}
	created += n

	return created, nil
}

// checkReadyInstanceWithoutAssignedIP: instance ở state READY nhưng
// không có ip_allocations nào ASSIGNED cho nó — vi phạm invariant 4
// (Phần II mục 5.1: "Một instance READY phải có validation... network
// đúng"), dấu hiệu network binding bị mất mà state không phản ánh.
func (s *Scanner) checkReadyInstanceWithoutAssignedIP(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT vi.id, vi.hostname
		FROM vm_instances vi
		WHERE vi.state = 'READY'
		  AND NOT EXISTS (
		    SELECT 1 FROM ip_allocations ia
		    WHERE ia.instance_id = vi.id AND ia.state = 'ASSIGNED'
		  )
	`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	created := 0
	for rows.Next() {
		var id, hostname string
		if err := rows.Scan(&id, &hostname); err != nil {
			return created, err
		}
		details, _ := json.Marshal(map[string]string{"hostname": hostname})
		_, exists, err := s.findings.Create(ctx, domain.Finding{
			Category:     "ready_instance_without_assigned_ip",
			Severity:     domain.FindingWarning,
			ResourceType: strPtr("vm_instance"),
			ResourceID:   &id,
			Summary:      fmt.Sprintf("Instance %s ở state READY nhưng không có IP allocation ASSIGNED", hostname),
			Details:      details,
		})
		if err != nil {
			return created, err
		}
		if !exists {
			created++
		}
	}
	return created, rows.Err()
}

// checkOrphanedIPAllocation: allocation RESERVED/ASSIGNED trỏ tới
// instance không còn tồn tại hoặc đã retired — IP bị giữ chỗ oan,
// chặn tái sử dụng dù resource logic đã kết thúc vòng đời (Phần VI
// mục 11: "IP allocated without active instance").
func (s *Scanner) checkOrphanedIPAllocation(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ia.id, ia.address::text
		FROM ip_allocations ia
		LEFT JOIN vm_instances vi ON vi.id = ia.instance_id
		WHERE ia.state IN ('RESERVED', 'ASSIGNED')
		  AND (vi.id IS NULL OR vi.retired_at IS NOT NULL)
	`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	created := 0
	for rows.Next() {
		var id, address string
		if err := rows.Scan(&id, &address); err != nil {
			return created, err
		}
		details, _ := json.Marshal(map[string]string{"address": address})
		_, exists, err := s.findings.Create(ctx, domain.Finding{
			Category:     "orphaned_ip_allocation",
			Severity:     domain.FindingWarning,
			ResourceType: strPtr("ip_allocation"),
			ResourceID:   &id,
			Summary:      fmt.Sprintf("IP %s đang RESERVED/ASSIGNED nhưng instance sở hữu không tồn tại hoặc đã retired", address),
			Details:      details,
		})
		if err != nil {
			return created, err
		}
		if !exists {
			created++
		}
	}
	return created, rows.Err()
}

func strPtr(s string) *string { return &s }
