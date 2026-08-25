package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

// FindingRepository đọc/ghi bảng findings (Phần VI mục 11, OpenAPI
// GET /v1/findings).
type FindingRepository struct {
	db *DB
}

// NewFindingRepository tạo FindingRepository gắn với một *DB.
func NewFindingRepository(db *DB) *FindingRepository {
	return &FindingRepository{db: db}
}

// Create ghi một finding mới. Nếu đã tồn tại finding OPEN cùng
// (category, resource_type, resource_id) — theo unique index
// uq_findings_open — trả alreadyExists=true thay vì lỗi, để scanner
// chạy lặp lại không tạo trùng (Phần VI mục 11: "Kết quả không tự
// delete; tạo finding và remediation job").
func (r *FindingRepository) Create(ctx context.Context, f domain.Finding) (created *domain.Finding, alreadyExists bool, err error) {
	details := f.Details
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO findings (category, severity, resource_type, resource_id, summary, details)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (category, resource_type, resource_id) WHERE state = 'OPEN' DO NOTHING
		RETURNING `+findingColumns,
		f.Category, f.Severity, f.ResourceType, f.ResourceID, f.Summary, details,
	)
	finding, err := scanFinding(row)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return finding, false, nil
}

// List trả finding theo state, mới nhất trước. state rỗng trả tất cả.
func (r *FindingRepository) List(ctx context.Context, state domain.FindingState) ([]domain.Finding, error) {
	query := `SELECT ` + findingColumns + ` FROM findings`
	args := []any{}
	if state != "" {
		query += ` WHERE state = $1`
		args = append(args, state)
	}
	query += ` ORDER BY detected_at DESC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list findings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var findings []domain.Finding
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		findings = append(findings, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate findings: %w", err)
	}
	return findings, nil
}

const findingColumns = `id, category, severity, resource_type, resource_id, summary, details, state, detected_at, resolved_at`

type findingRowScanner interface {
	Scan(dest ...any) error
}

func scanFinding(row findingRowScanner) (*domain.Finding, error) {
	var f domain.Finding
	var details []byte
	if err := row.Scan(
		&f.ID, &f.Category, &f.Severity, &f.ResourceType, &f.ResourceID,
		&f.Summary, &details, &f.State, &f.DetectedAt, &f.ResolvedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("storage: scan finding: %w", err)
	}
	f.Details = details
	return &f, nil
}
