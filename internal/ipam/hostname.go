package ipam

import (
	"context"
	"fmt"

	"github.com/Chinsusu/vm-factory/internal/storage"
)

// HostnameRepository sinh hostname theo policy {prefix}-{sequence:04d}
// (Phần II mục 8.3), không derive từ IP để tránh coupling.
type HostnameRepository struct {
	db *storage.DB
}

// NewHostnameRepository tạo HostnameRepository gắn với một *storage.DB.
func NewHostnameRepository(db *storage.DB) *HostnameRepository {
	return &HostnameRepository{db: db}
}

// Next sinh hostname tiếp theo cho một prefix, atomic qua upsert trên
// hostname_sequences — an toàn dưới concurrency (không cần transaction
// riêng, INSERT ... ON CONFLICT DO UPDATE là atomic ở PostgreSQL).
func (r *HostnameRepository) Next(ctx context.Context, prefix string) (string, error) {
	var seq int
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO hostname_sequences (prefix, next_value) VALUES ($1, 1)
		ON CONFLICT (prefix) DO UPDATE
			SET next_value = hostname_sequences.next_value + 1, updated_at = now()
		RETURNING next_value
	`, prefix).Scan(&seq)
	if err != nil {
		return "", fmt.Errorf("ipam: next hostname for prefix %q: %w", prefix, err)
	}
	return fmt.Sprintf("%s-%04d", prefix, seq), nil
}
