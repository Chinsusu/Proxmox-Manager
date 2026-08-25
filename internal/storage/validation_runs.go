package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

// ValidationRunRepository đọc/ghi bảng validation_runs — evidence có
// thể audit cho mỗi lần chạy ruleset identity/network/egress/workload/
// template (Phần VI mục 2.9, Phần VIII mục 1: "Validation phải tạo
// evidence có thể audit, không chỉ trả true/false").
type ValidationRunRepository struct {
	db *DB
}

// NewValidationRunRepository tạo ValidationRunRepository gắn với một *DB.
func NewValidationRunRepository(db *DB) *ValidationRunRepository {
	return &ValidationRunRepository{db: db}
}

// Create ghi một validation run đã hoàn tất (bao gồm evidence + result)
// trong một lần INSERT — rule engine chạy đồng bộ hết mọi rule trước
// khi persist, không có trạng thái "đang chạy" cần cập nhật sau
// (khác provisioning_jobs vốn có lease/heartbeat).
//
// Nhận storage.QueryRower để caller ghép cùng transaction với instance
// state transition (Phần V mục 1).
func (r *ValidationRunRepository) Create(ctx context.Context, q QueryRower, run domain.ValidationRun) (*domain.ValidationRun, error) {
	evidence := run.Evidence
	if len(evidence) == 0 {
		evidence = json.RawMessage("{}")
	}
	finishedAt := run.FinishedAt
	if finishedAt == nil {
		finishedAt = &run.StartedAt
	}

	row := q.QueryRowContext(ctx, `
		INSERT INTO validation_runs
			(instance_id, job_id, type, result, ruleset_version, evidence, started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+validationRunColumns,
		run.InstanceID, run.JobID, run.Type, run.Result, run.RulesetVersion, evidence, run.StartedAt, finishedAt,
	)
	return scanValidationRun(row)
}

// LatestByType trả validation run mới nhất cho một instance+type, dùng
// bởi drift scanner để so sánh digest ổn định qua thời gian (Phần VIII
// mục 12: "identity digest stability"). Trả domain.ErrNotFound nếu
// chưa từng có run nào.
func (r *ValidationRunRepository) LatestByType(ctx context.Context, instanceID, runType string) (*domain.ValidationRun, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+validationRunColumns+`
		FROM validation_runs
		WHERE instance_id = $1 AND type = $2
		ORDER BY started_at DESC
		LIMIT 1
	`, instanceID, runType)
	return scanValidationRun(row)
}

// LatestPerType trả run mới nhất cho MỖI type đã từng chạy trên một
// instance (identity/egress/workload/...) — dùng cho GET
// /v1/instances/{id}/evidence (P0-09): "Get latest validation evidence"
// nghĩa là bức tranh hiện tại của mọi loại validation, không phải toàn
// bộ lịch sử.
func (r *ValidationRunRepository) LatestPerType(ctx context.Context, instanceID string) ([]domain.ValidationRun, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ON (type) `+validationRunColumns+`
		FROM validation_runs
		WHERE instance_id = $1
		ORDER BY type, started_at DESC
	`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("storage: latest validation runs per type: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var runs []domain.ValidationRun
	for rows.Next() {
		run, err := scanValidationRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate validation runs: %w", err)
	}
	return runs, nil
}

// ValidationListFilter lọc GET /v1/validations (UI integration,
// API_UI_Gap_Register mục 3.4) — field rỗng nghĩa là không lọc theo field đó.
type ValidationListFilter struct {
	InstanceID string
	Result     string
	Type       string
}

// List trả validation run mới nhất trước (started_at DESC, id DESC),
// keyset pagination — afterStartedAt zero-value + afterID rỗng nghĩa "từ đầu".
func (r *ValidationRunRepository) List(ctx context.Context, filter ValidationListFilter, afterStartedAt time.Time, afterID string, limit int) ([]domain.ValidationRun, error) {
	query := `SELECT ` + validationRunColumns + ` FROM validation_runs WHERE 1=1`
	var args []any
	if filter.InstanceID != "" {
		args = append(args, filter.InstanceID)
		query += fmt.Sprintf(" AND instance_id = $%d", len(args))
	}
	if filter.Result != "" {
		args = append(args, filter.Result)
		query += fmt.Sprintf(" AND result = $%d", len(args))
	}
	if filter.Type != "" {
		args = append(args, filter.Type)
		query += fmt.Sprintf(" AND type = $%d", len(args))
	}
	args = append(args, nullableTime(afterStartedAt), nullableString(afterID))
	query += fmt.Sprintf(" AND ($%d::timestamptz IS NULL OR (started_at, id) < ($%d::timestamptz, $%d::uuid))", len(args)-1, len(args)-1, len(args))
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY started_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list validation runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var runs []domain.ValidationRun
	for rows.Next() {
		run, err := scanValidationRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate validation runs: %w", err)
	}
	return runs, nil
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

const validationRunColumns = `id, instance_id, job_id, type, result, ruleset_version, evidence, started_at, finished_at`

type validationRunRowScanner interface {
	Scan(dest ...any) error
}

func scanValidationRun(row validationRunRowScanner) (*domain.ValidationRun, error) {
	var v domain.ValidationRun
	var evidence []byte
	if err := row.Scan(
		&v.ID, &v.InstanceID, &v.JobID, &v.Type, &v.Result, &v.RulesetVersion,
		&evidence, &v.StartedAt, &v.FinishedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("storage: scan validation run: %w", err)
	}
	v.Evidence = evidence
	return &v, nil
}
