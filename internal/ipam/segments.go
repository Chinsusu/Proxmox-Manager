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
// phụ thuộc thêm thư viện chỉ để scan Postgres array. Nhận
// storage.QueryRower để caller tham gia transaction của chính họ (vd
// RunIdempotent ở API layer P0-09) — cùng lý do với instance/jobs
// Repository.Create.
func (r *SegmentRepository) Create(ctx context.Context, q storage.QueryRower, seg domain.NetworkSegment) (*domain.NetworkSegment, error) {
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

	row := q.QueryRowContext(ctx, `
		INSERT INTO network_segments (name, cidr, gateway, bridge, dns_servers, ipv6_policy, allocation_strategy, exclusions)
		VALUES ($1, $2::cidr, $3::inet, $4, COALESCE(string_to_array(NULLIF($5, ''), ','), ARRAY[]::text[])::inet[], $6, $7, $8)
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

// List trả segment sắp theo tên, lọc theo state nếu khác rỗng (dùng cho
// GET /v1/network-segments và GET /v1/ip-pools, API_UI_Gap_Register mục 3.2).
func (r *SegmentRepository) List(ctx context.Context, state string) ([]domain.NetworkSegment, error) {
	query := `SELECT ` + segmentColumns + ` FROM network_segments WHERE 1=1`
	var args []any
	if state != "" {
		args = append(args, state)
		query += fmt.Sprintf(" AND state = $%d", len(args))
	}
	query += ` ORDER BY name`
	rows, err := r.db.QueryContext(ctx, query, args...)
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

// SegmentCapacity là số lượng ip_allocations theo state cho một segment
// — dùng cho NetworkSegment.capacity ở API layer (P0-09). Total là tổng
// bốn state đã track (free+reserved+assigned+quarantined), KHÔNG phải
// dung lượng lý thuyết của CIDR (đã trừ network/broadcast/gateway/
// reserved theo Phần VI mục 3.1 tại thời điểm tạo allocation rows).
type SegmentCapacity struct {
	Total       int
	Free        int
	Reserved    int
	Assigned    int
	Quarantined int
}

// Capacity đếm ip_allocations theo state cho một segment.
func (r *SegmentRepository) Capacity(ctx context.Context, segmentID string) (SegmentCapacity, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT state, count(*) FROM ip_allocations WHERE segment_id = $1 GROUP BY state
	`, segmentID)
	if err != nil {
		return SegmentCapacity{}, fmt.Errorf("ipam: segment capacity: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var capacity SegmentCapacity
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return SegmentCapacity{}, fmt.Errorf("ipam: scan capacity row: %w", err)
		}
		switch domain.AllocationState(state) {
		case domain.AllocationFree:
			capacity.Free = n
		case domain.AllocationReserved:
			capacity.Reserved = n
		case domain.AllocationAssigned:
			capacity.Assigned = n
		case domain.AllocationQuarantined:
			capacity.Quarantined = n
		}
		capacity.Total += n
	}
	if err := rows.Err(); err != nil {
		return SegmentCapacity{}, fmt.Errorf("ipam: iterate capacity: %w", err)
	}
	return capacity, nil
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
