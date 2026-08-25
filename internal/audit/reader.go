package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// Reader đọc lại audit_events — tách khỏi Writer (dùng Execer tối
// giản để tránh phụ thuộc storage) vì đọc theo trang cần *storage.DB
// thật, không cần tham gia transaction của caller như Append.
type Reader struct {
	db *storage.DB
}

// NewReader tạo Reader gắn với một *storage.DB.
func NewReader(db *storage.DB) *Reader {
	return &Reader{db: db}
}

// ListByResource trả audit event mới nhất trước (occurred_at DESC, id
// DESC làm tie-break) cho một (resourceType, resourceID) cụ thể — keyset
// pagination, afterOccurredAt zero-value + afterID rỗng nghĩa "từ đầu".
func (r *Reader) ListByResource(ctx context.Context, resourceType, resourceID string, afterOccurredAt time.Time, afterID string, limit int) ([]domain.AuditEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, occurred_at, actor_type, actor_id, action, resource_type, resource_id,
		       request_id, correlation_id, before_state, after_state, metadata
		FROM audit_events
		WHERE resource_type = $1 AND resource_id = $2
		  AND ($3::timestamptz IS NULL OR (occurred_at, id) < ($3::timestamptz, $4::uuid))
		ORDER BY occurred_at DESC, id DESC
		LIMIT $5
	`, resourceType, resourceID, nullableTime(afterOccurredAt), nullableString(afterID), limit)
	if err != nil {
		return nil, fmt.Errorf("audit: list by resource: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var events []domain.AuditEvent
	for rows.Next() {
		var e domain.AuditEvent
		var before, after, metadata []byte
		if err := rows.Scan(
			&e.ID, &e.OccurredAt, &e.ActorType, &e.ActorID, &e.Action, &e.ResourceType, &e.ResourceID,
			&e.RequestID, &e.CorrelationID, &before, &after, &metadata,
		); err != nil {
			return nil, fmt.Errorf("audit: scan event: %w", err)
		}
		e.Before, e.After, e.Metadata = before, after, metadata
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: iterate events: %w", err)
	}
	return events, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
