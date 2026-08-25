package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

// AlertRepository đọc/ghi bảng alerts (UI integration,
// API_UI_Gap_Register mục 3.5).
type AlertRepository struct {
	db *DB
}

// NewAlertRepository tạo AlertRepository gắn với một *DB.
func NewAlertRepository(db *DB) *AlertRepository {
	return &AlertRepository{db: db}
}

const alertColumns = `id, fingerprint, status, severity, resource_type, resource_id, title, description,
	created_at, updated_at, acknowledged_at, acknowledged_by, acknowledged_note, version`

// Upsert tạo alert mới FIRING theo fingerprint, hoặc nếu fingerprint đã
// tồn tại: mở lại thành FIRING nếu trước đó RESOLVED (điều kiện tái
// diễn), giữ nguyên status nếu đang FIRING/ACKNOWLEDGED (không "quên"
// operator đã ack) — chỉ refresh description/updated_at. Dùng bởi
// alert deriver (cmd/worker) chạy định kỳ/theo sự kiện, gọi lại nhiều
// lần cho CÙNG điều kiện là bình thường, không phải lỗi.
func (r *AlertRepository) Upsert(ctx context.Context, a domain.Alert) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO alerts (fingerprint, status, severity, resource_type, resource_id, title, description)
		VALUES ($1, 'firing', $2, $3, $4, $5, $6)
		ON CONFLICT (fingerprint) DO UPDATE SET
			updated_at = now(),
			description = EXCLUDED.description,
			status = CASE WHEN alerts.status = 'resolved' THEN 'firing' ELSE alerts.status END,
			version = alerts.version + 1
	`, a.Fingerprint, a.Severity, a.ResourceType, a.ResourceID, a.Title, a.Description)
	if err != nil {
		return fmt.Errorf("storage: upsert alert: %w", err)
	}
	return nil
}

// AlertListFilter lọc GET /v1/alerts.
type AlertListFilter struct {
	Status       string
	Severity     string
	ResourceType string
}

// List trả alert mới nhất trước (created_at DESC, id DESC), keyset pagination.
func (r *AlertRepository) List(ctx context.Context, filter AlertListFilter, afterCreatedAt time.Time, afterID string, limit int) ([]domain.Alert, error) {
	query := `SELECT ` + alertColumns + ` FROM alerts WHERE 1=1`
	var args []any
	if filter.Status != "" {
		args = append(args, filter.Status)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if filter.Severity != "" {
		args = append(args, filter.Severity)
		query += fmt.Sprintf(" AND severity = $%d", len(args))
	}
	if filter.ResourceType != "" {
		args = append(args, filter.ResourceType)
		query += fmt.Sprintf(" AND resource_type = $%d", len(args))
	}
	args = append(args, nullableTime(afterCreatedAt), nullableString(afterID))
	query += fmt.Sprintf(" AND ($%d::timestamptz IS NULL OR (created_at, id) < ($%d::timestamptz, $%d::uuid))", len(args)-1, len(args)-1, len(args))
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate alerts: %w", err)
	}
	return out, nil
}

// Acknowledge chuyển một alert sang ACKNOWLEDGED — expectedVersion <= 0
// nghĩa là bỏ qua optimistic concurrency check (client không gửi
// version). Trả domain.ErrNotFound nếu id không tồn tại,
// domain.ErrVersionConflict nếu expectedVersion > 0 và không khớp
// version hiện tại (API_UI_Gap_Register mục 3.5: "structured conflict
// when state changes between render and action").
func (r *AlertRepository) Acknowledge(ctx context.Context, id, actor string, note *string, expectedVersion int) (*domain.Alert, error) {
	query := `
		UPDATE alerts
		SET status = 'acknowledged', acknowledged_at = now(), acknowledged_by = $1, acknowledged_note = $2,
		    updated_at = now(), version = version + 1
		WHERE id = $3
	`
	args := []any{actor, note, id}
	if expectedVersion > 0 {
		args = append(args, expectedVersion)
		query += fmt.Sprintf(" AND version = $%d", len(args))
	}
	query += " RETURNING " + alertColumns

	updated, err := scanAlert(r.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) && expectedVersion > 0 {
			if _, getErr := r.Get(ctx, id); getErr == nil {
				return nil, domain.ErrVersionConflict
			}
		}
		return nil, err
	}
	return updated, nil
}

// Get đọc một alert theo ID, trả domain.ErrNotFound nếu không tồn tại.
func (r *AlertRepository) Get(ctx context.Context, id string) (*domain.Alert, error) {
	return scanAlert(r.db.QueryRowContext(ctx, `SELECT `+alertColumns+` FROM alerts WHERE id = $1`, id))
}

type alertRowScanner interface {
	Scan(dest ...any) error
}

func scanAlert(row alertRowScanner) (*domain.Alert, error) {
	var a domain.Alert
	if err := row.Scan(
		&a.ID, &a.Fingerprint, &a.Status, &a.Severity, &a.ResourceType, &a.ResourceID, &a.Title, &a.Description,
		&a.CreatedAt, &a.UpdatedAt, &a.AcknowledgedAt, &a.AcknowledgedBy, &a.AcknowledgedNote, &a.Version,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("storage: scan alert: %w", err)
	}
	return &a, nil
}
