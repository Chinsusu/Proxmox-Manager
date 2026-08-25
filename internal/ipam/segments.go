package ipam

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

// SegmentRepository đọc/ghi bảng network_segments (Phần VI mục 2.3).
type SegmentRepository struct {
	db *storage.DB
}

// NewSegmentRepository tạo SegmentRepository gắn với một *storage.DB.
func NewSegmentRepository(db *storage.DB) *SegmentRepository {
	return &SegmentRepository{db: db}
}

// Create đăng ký một network segment mới. dns_servers lưu dạng inet[]
// nhưng được truyền/đọc dưới dạng chuỗi phân tách bởi dấu phẩy để tránh
// phụ thuộc thêm thư viện chỉ để scan Postgres array.
func (r *SegmentRepository) Create(ctx context.Context, seg domain.NetworkSegment) (*domain.NetworkSegment, error) {
	ipv6Policy := seg.IPv6Policy
	if ipv6Policy == "" {
		ipv6Policy = "deny"
	}
	allocStrategy := seg.AllocationStrategy
	if allocStrategy == "" {
		allocStrategy = "sequential-lowest-free"
	}
	exclusions := seg.Exclusions
	if len(exclusions) == 0 {
		exclusions = json.RawMessage("[]")
	}

	row := r.db.QueryRowContext(ctx, `
		INSERT INTO network_segments (name, cidr, gateway, bridge, dns_servers, ipv6_policy, allocation_strategy, exclusions)
		VALUES ($1, $2::cidr, $3::inet, $4, string_to_array(NULLIF($5, ''), ',')::inet[], $6, $7, $8)
		RETURNING `+segmentColumns, seg.Name, seg.CIDR, seg.Gateway, seg.Bridge, strings.Join(seg.DNSServers, ","), ipv6Policy, allocStrategy, exclusions)
	return scanSegment(row)
}

// Get đọc một segment theo ID.
func (r *SegmentRepository) Get(ctx context.Context, id string) (*domain.NetworkSegment, error) {
	return scanSegment(r.db.QueryRowContext(ctx, selectSegmentByID, id))
}

// GetByName đọc một segment theo tên (unique).
func (r *SegmentRepository) GetByName(ctx context.Context, name string) (*domain.NetworkSegment, error) {
	return scanSegment(r.db.QueryRowContext(ctx, selectSegmentByName, name))
}

// List trả mọi segment, sắp theo tên.
func (r *SegmentRepository) List(ctx context.Context) ([]domain.NetworkSegment, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+segmentColumns+` FROM network_segments ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("ipam: list segments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var segments []domain.NetworkSegment
	for rows.Next() {
		seg, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		segments = append(segments, *seg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ipam: iterate segments: %w", err)
	}
	return segments, nil
}

const segmentColumns = `id, name, cidr::text, gateway::text, bridge,
	array_to_string(dns_servers, ','), ipv6_policy, allocation_strategy,
	exclusions, state, created_at, updated_at`

var selectSegmentByID = `SELECT ` + segmentColumns + ` FROM network_segments WHERE id = $1`
var selectSegmentByName = `SELECT ` + segmentColumns + ` FROM network_segments WHERE name = $1`

func scanSegment(row rowScanner) (*domain.NetworkSegment, error) {
	var seg domain.NetworkSegment
	var dnsCSV string
	var exclusions []byte
	if err := row.Scan(
		&seg.ID, &seg.Name, &seg.CIDR, &seg.Gateway, &seg.Bridge,
		&dnsCSV, &seg.IPv6Policy, &seg.AllocationStrategy,
		&exclusions, &seg.State, &seg.CreatedAt, &seg.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("ipam: scan segment: %w", err)
	}
	if dnsCSV != "" {
		seg.DNSServers = strings.Split(dnsCSV, ",")
	}
	seg.Exclusions = exclusions
	return &seg, nil
}
