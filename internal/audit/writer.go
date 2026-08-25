// Package audit ghi audit_events append-only và outbox_events, theo
// Phần V mục 10 và Phần VI mục 2.10. Application không expose
// update/delete cho hai bảng này.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

// Execer là tập method tối thiểu để INSERT, thoả mãn cả *sql.DB và
// *sql.Tx. Định nghĩa riêng trong package này (không import
// internal/storage) để caller ghi audit event ngay trong transaction
// state-transition của chính họ mà không tạo dependency vòng.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Writer ghi audit_events và outbox_events.
type Writer struct{}

// NewWriter tạo một Writer. Writer không giữ state, an toàn dùng chung
// giữa nhiều goroutine.
func NewWriter() *Writer { return &Writer{} }

// Append ghi một audit_events record. Truyền execer là *sql.Tx của
// caller để đảm bảo audit nằm cùng transaction với state transition
// tạo ra nó (Phần V mục 10); truyền *sql.DB nếu action không thuộc một
// transaction lớn hơn (vd audit action đọc/không mutation).
func (w *Writer) Append(ctx context.Context, execer Execer, event domain.AuditEvent) error {
	if event.Metadata == nil {
		event.Metadata = json.RawMessage("{}")
	}
	_, err := execer.ExecContext(ctx, `
		INSERT INTO audit_events
			(actor_type, actor_id, action, resource_type, resource_id,
			 request_id, correlation_id, before_state, after_state, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		event.ActorType, event.ActorID, event.Action, event.ResourceType, event.ResourceID,
		event.RequestID, event.CorrelationID, nullableJSON(event.Before), nullableJSON(event.After), event.Metadata,
	)
	if err != nil {
		return fmt.Errorf("audit: append event: %w", err)
	}
	return nil
}

// Enqueue ghi một outbox_events record cùng transaction với caller, theo
// transactional outbox pattern (Phần II mục 6.3, Phần V mục 10).
func (w *Writer) Enqueue(ctx context.Context, execer Execer, event domain.OutboxEvent) error {
	_, err := execer.ExecContext(ctx, `
		INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload)
		VALUES ($1, $2, $3, $4)
	`,
		event.EventType, event.AggregateType, event.AggregateID, event.Payload,
	)
	if err != nil {
		return fmt.Errorf("audit: enqueue outbox event: %w", err)
	}
	return nil
}

// nullableJSON trả nil (SQL NULL) cho RawMessage rỗng thay vì chuỗi
// rỗng không hợp lệ JSONB, giữ before/after đúng nghĩa "không có dữ liệu".
func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
