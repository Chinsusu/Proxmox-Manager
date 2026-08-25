package ipam

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// Reaper thu hồi các reservation IP đã hết TTL, theo Phần VI mục 3.3:
//
//	"Reservation reaper chỉ release khi: TTL expired; job không
//	active/leased; no external VM or mapping observed; audit event
//	written."
//
// Điều kiện "no external VM or mapping observed" cần truy vấn
// Proxmox/PGW adapter, ngoài phạm vi package này (epic P0-05 State
// Engine sẽ compose Reaper với adapter check trước khi gọi). Ở đây
// Reaper chỉ đảm bảo hai điều kiện kiểm tra được thuần từ DB: TTL hết
// hạn và không có job đang active/leased cho instance sở hữu.
type Reaper struct {
	db    *storage.DB
	audit *audit.Writer
}

// NewReaper tạo Reaper gắn với một *storage.DB và audit.Writer.
func NewReaper(db *storage.DB, auditWriter *audit.Writer) *Reaper {
	return &Reaper{db: db, audit: auditWriter}
}

// ReleaseExpiredReservations tìm và giải phóng allocation RESERVED đã
// quá reserved_until mà instance sở hữu không còn job QUEUED/RUNNING/
// RETRY_WAIT, ghi audit event cho mỗi lần release. Trả số allocation
// đã release.
func (rp *Reaper) ReleaseExpiredReservations(ctx context.Context) (int64, error) {
	var released int64
	err := storage.WithTx(ctx, rp.db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, instance_id
			FROM ip_allocations
			WHERE state = 'RESERVED'
			  AND reserved_until IS NOT NULL
			  AND reserved_until < now()
			  AND instance_id IS NOT NULL
			  AND NOT EXISTS (
			    SELECT 1 FROM provisioning_jobs pj
			    WHERE pj.instance_id = ip_allocations.instance_id
			      AND pj.state IN ('QUEUED', 'RUNNING', 'RETRY_WAIT')
			  )
			FOR UPDATE SKIP LOCKED
		`)
		if err != nil {
			return fmt.Errorf("ipam: reaper select expired: %w", err)
		}
		defer func() { _ = rows.Close() }()

		type candidate struct {
			id, instanceID string
		}
		var candidates []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.instanceID); err != nil {
				return fmt.Errorf("ipam: reaper scan candidate: %w", err)
			}
			candidates = append(candidates, c)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("ipam: reaper iterate candidates: %w", err)
		}

		for _, c := range candidates {
			if _, err := tx.ExecContext(ctx, `
				UPDATE ip_allocations
				SET state = 'FREE', instance_id = NULL, reserved_until = NULL, released_at = now()
				WHERE id = $1
			`, c.id); err != nil {
				return fmt.Errorf("ipam: reaper release %s: %w", c.id, err)
			}

			metadata, _ := json.Marshal(map[string]string{"reason": "reservation_ttl_expired", "instance_id": c.instanceID})
			if err := rp.audit.Append(ctx, tx, domain.AuditEvent{
				ActorType:    "system",
				ActorID:      "ipam-reaper",
				Action:       "release_expired_reservation",
				ResourceType: "ip_allocation",
				ResourceID:   c.id,
				Metadata:     metadata,
			}); err != nil {
				return fmt.Errorf("ipam: reaper audit %s: %w", c.id, err)
			}
			released++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return released, nil
}
