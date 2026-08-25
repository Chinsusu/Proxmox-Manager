// Package instance implement repository cho vm_instances — aggregate
// root của VM lifecycle (Phần II mục 5.1), dùng bởi state engine (P0-05).
package instance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// Repository đọc/ghi bảng vm_instances.
type Repository struct {
	db *storage.DB
}

// NewRepository tạo Repository gắn với một *storage.DB.
func NewRepository(db *storage.DB) *Repository {
	return &Repository{db: db}
}

// Create đăng ký một instance mới ở state REQUESTED (Phần V mục 3),
// bất kể State truyền vào — chỉ UpdateState mới được đổi state. Nhận
// storage.QueryRower để caller ghép cùng transaction tạo job/reservation
// (Phần II mục 6: "instance + job + reservations created cùng transaction").
func (r *Repository) Create(ctx context.Context, q storage.QueryRower, inst domain.VMInstance) (*domain.VMInstance, error) {
	desiredConfig := inst.DesiredConfig
	if len(desiredConfig) == 0 {
		desiredConfig = json.RawMessage("{}")
	}
	workloadSpec := inst.WorkloadSpec
	if len(workloadSpec) == 0 {
		workloadSpec = json.RawMessage("{}")
	}
	generation := inst.Generation
	if generation == 0 {
		generation = 1
	}

	row := q.QueryRowContext(ctx, `
		INSERT INTO vm_instances
			(logical_name, hostname, state, generation, template_id,
			 desired_config, workload_adapter, workload_spec)
		VALUES ($1, $2, 'REQUESTED', $3, $4, $5, $6, $7)
		RETURNING `+instanceColumns,
		inst.LogicalName, inst.Hostname, generation, inst.TemplateID,
		desiredConfig, inst.WorkloadAdapter, workloadSpec,
	)
	return scanInstance(row)
}

// Get đọc một instance theo ID.
func (r *Repository) Get(ctx context.Context, id string) (*domain.VMInstance, error) {
	return scanInstance(r.db.QueryRowContext(ctx, selectInstanceByID, id))
}

// GetByLogicalName đọc instance mới nhất (generation cao nhất, còn
// active) theo logical_name — dùng khi rebuild cần tìm generation
// hiện tại (Phần V mục 8).
func (r *Repository) GetByLogicalName(ctx context.Context, logicalName string) (*domain.VMInstance, error) {
	return scanInstance(r.db.QueryRowContext(ctx, `
		SELECT `+instanceColumns+` FROM vm_instances
		WHERE logical_name = $1 AND retired_at IS NULL
		ORDER BY generation DESC LIMIT 1
	`, logicalName))
}

// UpdateState chuyển instance sang state mới, tăng version — dùng bởi
// state engine sau khi transition handler xác nhận evidence hợp lệ
// (Phần V mục 1). Không tự validate transition hợp lệ ở tầng repository
// — đó là trách nhiệm của transition registry (stateengine package),
// tách biệt "lưu kết quả" khỏi "quyết định được phép chuyển hay không".
func (r *Repository) UpdateState(ctx context.Context, execer storage.Execer, id string, newState domain.InstanceState) error {
	res, err := execer.ExecContext(ctx, `
		UPDATE vm_instances SET state = $1, version = version + 1, updated_at = now()
		WHERE id = $2
	`, newState, id)
	if err != nil {
		return fmt.Errorf("instance: update state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("instance: rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetPVEPlacement ghi placement Proxmox sau khi Clone thành công
// (Phần III mục 5.3: task success + VM object tồn tại + tag đúng).
func (r *Repository) SetPVEPlacement(ctx context.Context, execer storage.Execer, id, clusterID, node string, vmid int) error {
	res, err := execer.ExecContext(ctx, `
		UPDATE vm_instances SET pve_cluster_id = $1, pve_node = $2, vmid = $3, updated_at = now()
		WHERE id = $4
	`, clusterID, node, vmid, id)
	if err != nil {
		return fmt.Errorf("instance: set pve placement: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("instance: rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetDesiredConfigHash ghi lại hash config canonicalized, dùng cho
// idempotent Configure retry (Phần III mục 6.1).
func (r *Repository) SetDesiredConfigHash(ctx context.Context, execer storage.Execer, id, hash string) error {
	_, err := execer.ExecContext(ctx, `
		UPDATE vm_instances SET desired_config_hash = $1, updated_at = now() WHERE id = $2
	`, hash, id)
	if err != nil {
		return fmt.Errorf("instance: set desired config hash: %w", err)
	}
	return nil
}

// Retire đánh dấu instance đã kết thúc vòng đời (Phần V mục 9), giải
// phóng hostname/vmid unique constraint cho generation tiếp theo.
func (r *Repository) Retire(ctx context.Context, execer storage.Execer, id string) error {
	res, err := execer.ExecContext(ctx, `
		UPDATE vm_instances SET state = 'RETIRED', retired_at = now(), version = version + 1, updated_at = now()
		WHERE id = $1 AND retired_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("instance: retire: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("instance: rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// List trả instance mới nhất trước (created_at DESC, id DESC làm
// tie-break), keyset pagination — afterCreatedAt/afterID rỗng nghĩa là
// "từ đầu". Trả tối đa limit+1 bản ghi để caller (tầng HTTP) tự xác
// định có trang kế tiếp hay không mà không cần COUNT(*) riêng.
func (r *Repository) List(ctx context.Context, afterCreatedAt time.Time, afterID string, limit int) ([]domain.VMInstance, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+instanceColumns+` FROM vm_instances
		WHERE ($1::timestamptz IS NULL OR (created_at, id) < ($1::timestamptz, $2::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, nullableTime(afterCreatedAt), nullableString(afterID), limit)
	if err != nil {
		return nil, fmt.Errorf("instance: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var instances []domain.VMInstance
	for rows.Next() {
		inst, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		instances = append(instances, *inst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("instance: iterate list: %w", err)
	}
	return instances, nil
}

// FindCurrentJobID trả job gần nhất (created_at mới nhất) của một
// instance — dùng cho Instance.current_job_id ở API layer (P0-09).
// Trả domain.ErrNotFound nếu instance chưa từng có job nào.
func (r *Repository) FindCurrentJobID(ctx context.Context, instanceID string) (string, error) {
	var jobID string
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM provisioning_jobs WHERE instance_id = $1 ORDER BY created_at DESC LIMIT 1
	`, instanceID).Scan(&jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.ErrNotFound
		}
		return "", fmt.Errorf("instance: find current job id: %w", err)
	}
	return jobID, nil
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

const instanceColumns = `id, logical_name, hostname, state, generation, template_id,
	pve_cluster_id, pve_node, vmid, resource_pool,
	desired_config, desired_config_hash, workload_adapter, workload_spec,
	version, created_at, updated_at, retired_at`

var selectInstanceByID = `SELECT ` + instanceColumns + ` FROM vm_instances WHERE id = $1`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInstance(row rowScanner) (*domain.VMInstance, error) {
	var inst domain.VMInstance
	var pveClusterID, pveNode, resourcePool, desiredConfigHash, workloadAdapter sql.NullString
	var vmid sql.NullInt32
	var desiredConfig, workloadSpec []byte
	if err := row.Scan(
		&inst.ID, &inst.LogicalName, &inst.Hostname, &inst.State, &inst.Generation, &inst.TemplateID,
		&pveClusterID, &pveNode, &vmid, &resourcePool,
		&desiredConfig, &desiredConfigHash, &workloadAdapter, &workloadSpec,
		&inst.Version, &inst.CreatedAt, &inst.UpdatedAt, &inst.RetiredAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("instance: scan: %w", err)
	}
	if pveClusterID.Valid {
		inst.PVEClusterID = &pveClusterID.String
	}
	if pveNode.Valid {
		inst.PVENode = &pveNode.String
	}
	if vmid.Valid {
		v := int(vmid.Int32)
		inst.VMID = &v
	}
	if resourcePool.Valid {
		inst.ResourcePool = &resourcePool.String
	}
	if desiredConfigHash.Valid {
		inst.DesiredConfigHash = &desiredConfigHash.String
	}
	if workloadAdapter.Valid {
		inst.WorkloadAdapter = &workloadAdapter.String
	}
	inst.DesiredConfig = desiredConfig
	inst.WorkloadSpec = workloadSpec
	return &inst, nil
}
