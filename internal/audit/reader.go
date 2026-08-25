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

// ListFilter lọc GET /v1/audit-events (UI integration,
// API_UI_Gap_Register mục 3.6) — field rỗng nghĩa là không lọc theo field đó.
type ListFilter struct {
	Actor        string
	Action       string
	ResourceType string
	ResourceID   string
	From         time.Time
	To           time.Time
	// Q tìm theo action/resource_type/resource_id (ILIKE) — audit_events
	// không có free-text field riêng, ghép các cột định danh là hợp lý
	// nhất cho tìm kiếm nhanh trên UI.
	Q string
}

// List trả audit event mới nhất trước (occurred_at DESC, id DESC), lọc
// chung (không giới hạn một resource cụ thể như ListByResource) — keyset
// pagination, afterOccurredAt zero-value + afterID rỗng nghĩa "từ đầu".
func (r *Reader) List(ctx context.Context, filter ListFilter, afterOccurredAt time.Time, afterID string, limit int) ([]domain.AuditEvent, error) {
	query := `SELECT id, occurred_at, actor_type, actor_id, action, resource_type, resource_id,
		request_id, correlation_id, before_state, after_state, metadata
		FROM audit_events WHERE 1=1`
	var args []any
	if filter.Actor != "" {
		args = append(args, filter.Actor)
		query += fmt.Sprintf(" AND actor_id = $%d", len(args))
	}
	if filter.Action != "" {
		args = append(args, filter.Action)
		query += fmt.Sprintf(" AND action = $%d", len(args))
	}
	if filter.ResourceType != "" {
		args = append(args, filter.ResourceType)
		query += fmt.Sprintf(" AND resource_type = $%d", len(args))
	}
	if filter.ResourceID != "" {
		args = append(args, filter.ResourceID)
		query += fmt.Sprintf(" AND resource_id = $%d", len(args))
	}
	if !filter.From.IsZero() {
		args = append(args, filter.From)
		query += fmt.Sprintf(" AND occurred_at >= $%d", len(args))
	}
	if !filter.To.IsZero() {
		args = append(args, filter.To)
		query += fmt.Sprintf(" AND occurred_at <= $%d", len(args))
	}
	if filter.Q != "" {
		args = append(args, "%"+filter.Q+"%")
		query += fmt.Sprintf(" AND (action ILIKE $%d OR resource_type ILIKE $%d OR resource_id ILIKE $%d)", len(args), len(args), len(args))
	}
	args = append(args, nullableTime(afterOccurredAt), nullableString(afterID))
	query += fmt.Sprintf(" AND ($%d::timestamptz IS NULL OR (occurred_at, id) < ($%d::timestamptz, $%d::uuid))", len(args)-1, len(args)-1, len(args))
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY occurred_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
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
