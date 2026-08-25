// Package ipam implement reservation IPv4 theo Phần VI mục 3:
// FREE → RESERVED → ASSIGNED → QUARANTINED → RELEASED.
package ipam

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// Repository đọc/ghi bảng ip_allocations.
type Repository struct {
	db *storage.DB
}

// NewRepository tạo Repository gắn với một *storage.DB.
func NewRepository(db *storage.DB) *Repository {
	return &Repository{db: db}
}

// ReserveNextFree chọn địa chỉ FREE nhỏ nhất trong segment và chuyển
// RESERVED cho instanceID, dùng FOR UPDATE SKIP LOCKED để nhiều worker
// cạnh tranh an toàn — đúng nguyên văn truy vấn ở Phần VI mục 3.2. Trả
// domain.ErrCapacityExhausted nếu segment hết địa chỉ FREE.
func (r *Repository) ReserveNextFree(ctx context.Context, segmentID, instanceID string, ttl time.Duration) (*domain.IPAllocation, error) {
	var alloc *domain.IPAllocation
	err := storage.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT id, address
			FROM ip_allocations
			WHERE segment_id = $1 AND state = 'FREE'
			ORDER BY address
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		`, segmentID)
		var id, address string
		if err := row.Scan(&id, &address); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrCapacityExhausted
			}
			return fmt.Errorf("ipam: select free address: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE ip_allocations
			SET state = 'RESERVED', instance_id = $1, reserved_until = now() + make_interval(secs => $2)
			WHERE id = $3
		`, instanceID, ttl.Seconds(), id); err != nil {
			return fmt.Errorf("ipam: reserve update: %w", err)
		}

		reserved, err := scanAllocation(tx.QueryRowContext(ctx, selectAllocationByID, id))
		if err != nil {
			return err
		}
		alloc = reserved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return alloc, nil
}

// MarkAssigned chuyển một allocation RESERVED sang ASSIGNED (network
// binding đã active, Phần V mục 4.5) — chỉ áp dụng cho đúng instanceID
// đang giữ reservation, trả domain.ErrNotFound nếu không khớp.
func (r *Repository) MarkAssigned(ctx context.Context, allocationID, instanceID string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE ip_allocations
		SET state = 'ASSIGNED', assigned_at = now()
		WHERE id = $1 AND instance_id = $2 AND state = 'RESERVED'
	`, allocationID, instanceID)
	if err != nil {
		return fmt.Errorf("ipam: mark assigned: %w", err)
	}
	return requireRowsAffected(res)
}

// MarkQuarantined chuyển một allocation RESERVED/ASSIGNED sang
// QUARANTINED (Phần V mục 7: "block automatic reuse of IP/proxy
// binding") — không tự Release, chặn tái sử dụng tới khi operator xử lý.
func (r *Repository) MarkQuarantined(ctx context.Context, allocationID string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE ip_allocations SET state = 'QUARANTINED' WHERE id = $1 AND state IN ('RESERVED', 'ASSIGNED')
	`, allocationID)
	if err != nil {
		return fmt.Errorf("ipam: mark quarantined: %w", err)
	}
	return requireRowsAffected(res)
}

// Release trả một allocation về FREE (rollback hoặc decommission, Phần
// VI mục 3.3) — reaper/state engine chỉ được gọi sau khi đã xác nhận
// không còn external VM/mapping tham chiếu tới nó.
func (r *Repository) Release(ctx context.Context, allocationID string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE ip_allocations
		SET state = 'FREE', instance_id = NULL, reserved_until = NULL,
		    assigned_at = NULL, released_at = now()
		WHERE id = $1 AND state IN ('RESERVED', 'ASSIGNED', 'QUARANTINED')
	`, allocationID)
	if err != nil {
		return fmt.Errorf("ipam: release: %w", err)
	}
	return requireRowsAffected(res)
}

// Get đọc một allocation theo ID, trả domain.ErrNotFound nếu không tồn tại.
func (r *Repository) Get(ctx context.Context, allocationID string) (*domain.IPAllocation, error) {
	return scanAllocation(r.db.QueryRowContext(ctx, selectAllocationByID, allocationID))
}

// FindByInstance đọc allocation RESERVED/ASSIGNED hiện tại của một
// instance (mới nhất nếu có nhiều) — dùng cho Instance.ip_address ở API
// layer (P0-09), vốn không lưu IP trực tiếp trên vm_instances (nguồn sự
// thật là ip_allocations). Trả domain.ErrNotFound nếu instance chưa có
// allocation nào đang hoạt động (vd còn ở REQUESTED, chưa qua RESERVING).
func (r *Repository) FindByInstance(ctx context.Context, instanceID string) (*domain.IPAllocation, error) {
	return scanAllocation(r.db.QueryRowContext(ctx, `
		SELECT id, segment_id, address, instance_id, state, reserved_until, assigned_at, released_at, created_at
		FROM ip_allocations
		WHERE instance_id = $1 AND state IN ('RESERVED', 'ASSIGNED')
		ORDER BY created_at DESC LIMIT 1
	`, instanceID))
}

const selectAllocationByID = `
	SELECT id, segment_id, address, instance_id, state, reserved_until, assigned_at, released_at, created_at
	FROM ip_allocations
	WHERE id = $1
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAllocation(row rowScanner) (*domain.IPAllocation, error) {
	var a domain.IPAllocation
	if err := row.Scan(
		&a.ID, &a.SegmentID, &a.Address, &a.InstanceID, &a.State,
		&a.ReservedUntil, &a.AssignedAt, &a.ReleasedAt, &a.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("ipam: scan allocation: %w", err)
	}
	return &a, nil
}

func requireRowsAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("ipam: rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
