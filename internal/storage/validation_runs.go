package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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
