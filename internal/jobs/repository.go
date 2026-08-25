// Package jobs implement job lease trên PostgreSQL (FOR UPDATE SKIP
// LOCKED), heartbeat và lease takeover theo Phần II mục 6.1.
package jobs

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

// Repository đọc/ghi bảng provisioning_jobs.
type Repository struct {
	db *storage.DB
}

// NewRepository tạo Repository gắn với một *storage.DB.
func NewRepository(db *storage.DB) *Repository {
	return &Repository{db: db}
}

// Claim lease một job đủ điều kiện (state QUEUED/RETRY_WAIT, next_attempt_at
// <= now), dùng FOR UPDATE SKIP LOCKED để nhiều worker cạnh tranh an toàn
// không lock chờ nhau — đúng nguyên văn truy vấn ở Phần II mục 6.1. Trả
// domain.ErrNotClaimable nếu không có job nào sẵn sàng.
func (r *Repository) Claim(ctx context.Context, workerID string, leaseDuration time.Duration) (*domain.ProvisioningJob, error) {
	var job *domain.ProvisioningJob
	err := storage.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT id
			FROM provisioning_jobs
			WHERE next_attempt_at <= now()
			  AND state IN ('QUEUED', 'RETRY_WAIT')
			ORDER BY priority DESC, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		`)
		var id string
		if err := row.Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrNotClaimable
			}
			return fmt.Errorf("jobs: select claimable: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE provisioning_jobs
			SET state = 'RUNNING',
			    lease_owner = $1,
			    lease_expires_at = now() + make_interval(secs => $2),
			    attempt = attempt + 1,
			    started_at = COALESCE(started_at, now())
			WHERE id = $3
		`, workerID, leaseDuration.Seconds(), id); err != nil {
			return fmt.Errorf("jobs: claim update: %w", err)
		}

		claimed, err := scanJob(tx.QueryRowContext(ctx, selectJobByID, id))
		if err != nil {
			return err
		}
		job = claimed
		return nil
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

// Heartbeat gia hạn lease cho job đang RUNNING, chỉ khi workerID vẫn là
// chủ sở hữu lease hiện tại. Trả domain.ErrLeaseLost nếu lease đã hết
// hạn/bị worker khác tiếp quản (Phần I mục 8.1).
func (r *Repository) Heartbeat(ctx context.Context, jobID, workerID string, extension time.Duration) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE provisioning_jobs
		SET lease_expires_at = now() + make_interval(secs => $1)
		WHERE id = $2 AND lease_owner = $3 AND state = 'RUNNING' AND lease_expires_at > now()
	`, extension.Seconds(), jobID, workerID)
	if err != nil {
		return fmt.Errorf("jobs: heartbeat: %w", err)
	}
	return requireRowsAffected(res, domain.ErrLeaseLost)
}

// Complete đánh dấu job thành công (terminal), giải phóng lease.
func (r *Repository) Complete(ctx context.Context, jobID, workerID string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE provisioning_jobs
		SET state = 'SUCCEEDED', finished_at = now(), lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1 AND lease_owner = $2 AND state = 'RUNNING'
	`, jobID, workerID)
	if err != nil {
		return fmt.Errorf("jobs: complete: %w", err)
	}
	return requireRowsAffected(res, domain.ErrLeaseLost)
}

// Fail đánh dấu job lỗi. retryAt != nil chuyển RETRY_WAIT với next_attempt_at
// tương ứng (theo backoff policy caller tự tính, Phần V mục 5); retryAt ==
// nil chuyển FAILED (terminal). Cả hai đều giải phóng lease.
func (r *Repository) Fail(ctx context.Context, jobID, workerID, errorCode, errorMessage string, retryAt *time.Time) error {
	var res sql.Result
	var err error
	if retryAt != nil {
		res, err = r.db.ExecContext(ctx, `
			UPDATE provisioning_jobs
			SET state = 'RETRY_WAIT', next_attempt_at = $1, error_code = $2, error_message = $3,
			    lease_owner = NULL, lease_expires_at = NULL
			WHERE id = $4 AND lease_owner = $5 AND state = 'RUNNING'
		`, *retryAt, errorCode, errorMessage, jobID, workerID)
	} else {
		res, err = r.db.ExecContext(ctx, `
			UPDATE provisioning_jobs
			SET state = 'FAILED', finished_at = now(), error_code = $1, error_message = $2,
			    lease_owner = NULL, lease_expires_at = NULL
			WHERE id = $3 AND lease_owner = $4 AND state = 'RUNNING'
		`, errorCode, errorMessage, jobID, workerID)
	}
	if err != nil {
		return fmt.Errorf("jobs: fail: %w", err)
	}
	return requireRowsAffected(res, domain.ErrLeaseLost)
}

// ReclaimExpiredLeases đưa các job RUNNING có lease đã hết hạn quay lại
// QUEUED để worker khác claim được — bù cho trường hợp worker chết giữa
// chừng mà không kịp Fail/Complete (Phần I mục 8.1, Phần II mục 6.1).
// Trả về số job đã reclaim.
func (r *Repository) ReclaimExpiredLeases(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE provisioning_jobs
		SET state = 'QUEUED', lease_owner = NULL, lease_expires_at = NULL
		WHERE state = 'RUNNING' AND lease_expires_at IS NOT NULL AND lease_expires_at < now()
	`)
	if err != nil {
		return 0, fmt.Errorf("jobs: reclaim expired leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("jobs: reclaim rows affected: %w", err)
	}
	return n, nil
}

// Get đọc một job theo ID, trả domain.ErrNotFound nếu không tồn tại.
func (r *Repository) Get(ctx context.Context, jobID string) (*domain.ProvisioningJob, error) {
	return scanJob(r.db.QueryRowContext(ctx, selectJobByID, jobID))
}

const selectJobByID = `
	SELECT id, instance_id, operation, state, checkpoint, checkpoint_data,
	       priority, attempt, max_attempts, next_attempt_at,
	       lease_owner, lease_expires_at, error_code, error_message,
	       created_at, started_at, finished_at
	FROM provisioning_jobs
	WHERE id = $1
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (*domain.ProvisioningJob, error) {
	var j domain.ProvisioningJob
	var checkpointData []byte
	if err := row.Scan(
		&j.ID, &j.InstanceID, &j.Operation, &j.State, &j.Checkpoint, &checkpointData,
		&j.Priority, &j.Attempt, &j.MaxAttempts, &j.NextAttemptAt,
		&j.LeaseOwner, &j.LeaseExpiresAt, &j.ErrorCode, &j.ErrorMessage,
		&j.CreatedAt, &j.StartedAt, &j.FinishedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("jobs: scan job: %w", err)
	}
	j.CheckpointData = json.RawMessage(checkpointData)
	return &j, nil
}

func requireRowsAffected(res sql.Result, ifZero error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("jobs: rows affected: %w", err)
	}
	if n == 0 {
		return ifZero
	}
	return nil
}
