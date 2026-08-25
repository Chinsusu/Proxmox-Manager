package domain

import (
	"encoding/json"
	"time"
)

// AuditEvent là một bản ghi append-only — bản ghi audit_events, theo
// Phần VI mục 2.10. Application không expose update/delete cho bảng
// này (Phần II mục 11 và Phần VI mục 2.10).
type AuditEvent struct {
	ID            string
	OccurredAt    time.Time
	ActorType     string
	ActorID       string
	Action        string
	ResourceType  string
	ResourceID    string
	RequestID     *string
	CorrelationID *string
	Before        json.RawMessage
	After         json.RawMessage
	Metadata      json.RawMessage
}

// OutboxEvent là một domain event chờ publish — bản ghi outbox_events,
// theo Phần V mục 10. Phải ghi cùng transaction với state transition
// tạo ra nó (Phần II mục 7, mục 6.3 bảng idempotency).
type OutboxEvent struct {
	ID            string
	EventType     string
	AggregateType string
	AggregateID   string
	Payload       json.RawMessage
	CreatedAt     time.Time
	PublishedAt   *time.Time
	Attempt       int
}

// IdempotencyRecord là bản ghi idempotency_keys, theo Phần VI mục 2.11.
// Cùng key nhưng request hash khác phải trả IDEMPOTENCY_CONFLICT
// (Phần II mục 10, đã liệt kê trong error mapping).
type IdempotencyRecord struct {
	Scope          string
	Key            string
	RequestHash    string
	ResponseStatus *int
	ResponseBody   json.RawMessage
	ResourceID     *string
	ExpiresAt      time.Time
	CreatedAt      time.Time
}
