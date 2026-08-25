// Package template implement template registry theo Phần IX (Ubuntu
// Golden Template Specification) mục 6, 9: manifest, versioning và
// promotion DRAFT → CANDIDATE → ACTIVE → DEPRECATED → REVOKED.
package template

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// Repository đọc/ghi bảng vm_templates.
type Repository struct {
	db *storage.DB
}

// NewRepository tạo Repository gắn với một *storage.DB.
func NewRepository(db *storage.DB) *Repository {
	return &Repository{db: db}
}

// validTransitions khớp Phần IV mục 9. DEPRECATED → ACTIVE là đường
// rollback ("promote version trước làm active" thay vì mutate version
// lỗi) — không phải luồng forward bình thường nhưng được phép tường minh.
var validTransitions = map[domain.TemplateState][]domain.TemplateState{
	domain.TemplateDraft:      {domain.TemplateCandidate},
	domain.TemplateCandidate:  {domain.TemplateActive, domain.TemplateRevoked},
	domain.TemplateActive:     {domain.TemplateDeprecated, domain.TemplateRevoked},
	domain.TemplateDeprecated: {domain.TemplateActive, domain.TemplateRevoked},
	domain.TemplateRevoked:    {},
}

func isValidTransition(from, to domain.TemplateState) bool {
	for _, allowed := range validTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Create đăng ký một template version mới ở state DRAFT (Phần IV mục
// 9: "DRAFT → CANDIDATE → validation suite → ACTIVE"), bất kể State
// truyền vào trong tham số — chỉ Promote mới được đổi state.
func (r *Repository) Create(ctx context.Context, t domain.Template) (*domain.Template, error) {
	cloneModes := t.CloneModeAllowed
	if len(cloneModes) == 0 {
		cloneModes = []string{"full"}
	}
	manifest := t.BuildManifest
	if len(manifest) == 0 {
		manifest = json.RawMessage("{}")
	}

	row := r.db.QueryRowContext(ctx, `
		INSERT INTO vm_templates
			(name, family, version, os_family, os_version, architecture,
			 pve_cluster_id, pve_node, pve_template_vmid, storage,
			 clone_mode_allowed, source_checksum, build_manifest, state, validation_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, string_to_array($11, ',')::text[], $12, $13, 'DRAFT', 'UNKNOWN')
		RETURNING `+templateColumns,
		t.Name, t.Family, t.Version, t.OSFamily, t.OSVersion, t.Architecture,
		t.PVEClusterID, t.PVENode, t.PVETemplateVMID, t.Storage,
		strings.Join(cloneModes, ","), t.SourceChecksum, manifest,
	)
	return scanTemplate(row)
}

// Get đọc một template theo ID.
func (r *Repository) Get(ctx context.Context, id string) (*domain.Template, error) {
	return scanTemplate(r.db.QueryRowContext(ctx, selectTemplateByID, id))
}

// GetActiveByFamily đọc template ACTIVE hiện tại của một family — theo
// invariant "một template family có một ACTIVE default" (Phần IV mục 9).
func (r *Repository) GetActiveByFamily(ctx context.Context, family string) (*domain.Template, error) {
	return scanTemplate(r.db.QueryRowContext(ctx, `
		SELECT `+templateColumns+` FROM vm_templates WHERE family = $1 AND state = 'ACTIVE'
	`, family))
}

// List trả template theo family (rỗng = mọi family), mới nhất trước.
func (r *Repository) List(ctx context.Context, family string) ([]domain.Template, error) {
	query := `SELECT ` + templateColumns + ` FROM vm_templates`
	args := []any{}
	if family != "" {
		query += ` WHERE family = $1`
		args = append(args, family)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("template: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var templates []domain.Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		templates = append(templates, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("template: iterate: %w", err)
	}
	return templates, nil
}

// Promote chuyển template sang target state, đúng theo validTransitions.
// Khi promote lên ACTIVE, mọi template ACTIVE khác cùng family bị
// chuyển DEPRECATED trong cùng transaction — đảm bảo invariant "một
// ACTIVE default cho mỗi family" (Phần IV mục 9), không tạo khoảng
// trống hai bản ACTIVE hay không bản nào.
func (r *Repository) Promote(ctx context.Context, id string, target domain.TemplateState) (*domain.Template, error) {
	var result *domain.Template
	err := storage.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		current, err := scanTemplate(tx.QueryRowContext(ctx, selectTemplateByID+` FOR UPDATE`, id))
		if err != nil {
			return err
		}

		if !isValidTransition(current.State, target) {
			return fmt.Errorf("%w: %s -> %s", domain.ErrInvalidTransition, current.State, target)
		}

		if target == domain.TemplateActive {
			if _, err := tx.ExecContext(ctx, `
				UPDATE vm_templates SET state = 'DEPRECATED', updated_at = now()
				WHERE family = $1 AND state = 'ACTIVE' AND id != $2
			`, current.Family, id); err != nil {
				return fmt.Errorf("template: demote previous active: %w", err)
			}
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE vm_templates SET state = $1, updated_at = now() WHERE id = $2
		`, target, id); err != nil {
			return fmt.Errorf("template: promote: %w", err)
		}

		updated, err := scanTemplate(tx.QueryRowContext(ctx, selectTemplateByID, id))
		if err != nil {
			return err
		}
		result = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// SetValidationStatus cập nhật validation_status sau khi chạy offline
// hoặc canary validator (Phần IV mục 8), không tự đổi state — Promote
// là hành động tường minh riêng, tránh side effect ẩn.
func (r *Repository) SetValidationStatus(ctx context.Context, id string, status domain.ValidationResult) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE vm_templates SET validation_status = $1, updated_at = now() WHERE id = $2
	`, status, id)
	if err != nil {
		return fmt.Errorf("template: set validation status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("template: rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

const templateColumns = `id, name, family, version, os_family, os_version, architecture,
	pve_cluster_id, pve_node, pve_template_vmid, storage,
	array_to_string(clone_mode_allowed, ','),
	source_checksum, build_manifest, state, validation_status, created_at, updated_at`

var selectTemplateByID = `SELECT ` + templateColumns + ` FROM vm_templates WHERE id = $1`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTemplate(row rowScanner) (*domain.Template, error) {
	var t domain.Template
	var storageVal sql.NullString
	var cloneModesCSV string
	var manifest []byte
	if err := row.Scan(
		&t.ID, &t.Name, &t.Family, &t.Version, &t.OSFamily, &t.OSVersion, &t.Architecture,
		&t.PVEClusterID, &t.PVENode, &t.PVETemplateVMID, &storageVal, &cloneModesCSV,
		&t.SourceChecksum, &manifest, &t.State, &t.ValidationStatus, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("template: scan: %w", err)
	}
	t.Storage = storageVal.String
	if cloneModesCSV != "" {
		t.CloneModeAllowed = strings.Split(cloneModesCSV, ",")
	}
	t.BuildManifest = manifest
	return &t, nil
}
