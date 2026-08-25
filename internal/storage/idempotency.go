package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

// IdempotencyRepository đọc/ghi bảng idempotency_keys (Phần VI mục 2.11).
type IdempotencyRepository struct {
	db *DB
}

// NewIdempotencyRepository tạo repository gắn với một *DB.
func NewIdempotencyRepository(db *DB) *IdempotencyRepository {
	return &IdempotencyRepository{db: db}
}

// Get đọc bản ghi idempotency theo (scope, key). Trả domain.ErrNotFound
// nếu chưa tồn tại hoặc đã hết hạn (expires_at <= now()).
func (r *IdempotencyRepository) Get(ctx context.Context, scope, key string) (*domain.IdempotencyRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT scope, key, request_hash, response_status, response_body, resource_id, expires_at, created_at
		FROM idempotency_keys
		WHERE scope = $1 AND key = $2 AND expires_at > now()
	`, scope, key)
	return scanIdempotencyRecord(row)
}

// Store ghi bản ghi idempotency mới trong scope transaction của caller.
// Nếu (scope, key) đã tồn tại với request_hash khác, trả
// domain.ErrIdempotencyConflict (Phần II mục 10). Nếu request_hash
// giống hệt, coi là no-op thành công (retry idempotent đúng nghĩa).
func (r *IdempotencyRepository) Store(ctx context.Context, execer Execer, rec domain.IdempotencyRecord) error {
	_, err := execer.ExecContext(ctx, `
		INSERT INTO idempotency_keys
			(scope, key, request_hash, response_status, response_body, resource_id, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (scope, key) DO UPDATE
			SET request_hash = EXCLUDED.request_hash
			WHERE idempotency_keys.request_hash = EXCLUDED.request_hash
	`,
		rec.Scope, rec.Key, rec.RequestHash, rec.ResponseStatus, nullableJSONStorage(rec.ResponseBody), rec.ResourceID, rec.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("storage: store idempotency record: %w", err)
	}

	// ON CONFLICT ... WHERE khong khop se khong update va cung khong bao
	// loi - phai doc lai de phan biet "da ton tai giong het" voi "conflict
	// that su" (Postgres khong co cach tra ve "0 rows affected vi WHERE
	// khong khop" ma phan biet duoc voi "0 rows vi khong co conflict").
	existing, getErr := r.Get(ctx, rec.Scope, rec.Key)
	if getErr != nil {
		return fmt.Errorf("storage: verify idempotency record: %w", getErr)
	}
	if existing.RequestHash != rec.RequestHash {
		return domain.ErrIdempotencyConflict
	}
	return nil
}

func scanIdempotencyRecord(row *sql.Row) (*domain.IdempotencyRecord, error) {
	var rec domain.IdempotencyRecord
	var responseBody []byte
	if err := row.Scan(
		&rec.Scope, &rec.Key, &rec.RequestHash, &rec.ResponseStatus, &responseBody,
		&rec.ResourceID, &rec.ExpiresAt, &rec.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("storage: scan idempotency record: %w", err)
	}
	if responseBody != nil {
		rec.ResponseBody = json.RawMessage(responseBody)
	}
	return &rec, nil
}

func nullableJSONStorage(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}
