package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

// IdentityRepository đọc/ghi bảng identity_observations (Phần VI mục
// 2.8) — evidence guest facts sau mỗi lần thu thập, và tra cứu trùng
// machine_id_digest cho ID-002/duplicate detection (Phần VIII mục 10).
//
// mac_addresses/ip_addresses là cột MACADDR[]/INET[] — build/scan qua
// string_to_array/array_to_string dạng CSV thay vì scan mảng Go trực
// tiếp qua driver, theo quy ước đã dùng cho các cột array khác trong
// dự án (tránh phụ thuộc hành vi scan mảng riêng của driver pgx).
type IdentityRepository struct {
	db *DB
}

// NewIdentityRepository tạo IdentityRepository gắn với một *DB.
func NewIdentityRepository(db *DB) *IdentityRepository {
	return &IdentityRepository{db: db}
}

// Create ghi một lần quan sát guest facts mới. Bảng này lưu LỊCH SỬ,
// không upsert/dedupe theo instance (Phần VI mục 2.8: "không đặt
// UNIQUE trên digest... bảng này lưu cả lịch sử").
//
// Nhận storage.QueryRower để caller (ValidatingIdentityHandler) ghép
// cùng transaction với instance.UpdateState và audit event, khớp
// nguyên tắc "mọi transition có checkpoint + audit event" (Phần V mục 1).
func (r *IdentityRepository) Create(ctx context.Context, q QueryRower, obs domain.IdentityObservation) (*domain.IdentityObservation, error) {
	facts := obs.Facts
	if len(facts) == 0 {
		facts = json.RawMessage("{}")
	}
	macCSV := strings.Join(obs.MACAddresses, ",")
	ipCSV := strings.Join(obs.IPAddresses, ",")

	row := q.QueryRowContext(ctx, `
		INSERT INTO identity_observations
			(instance_id, generation, machine_id_digest, ssh_host_fingerprint,
			 cloud_init_instance_id, hostname, mac_addresses, ip_addresses, boot_id, facts)
		VALUES ($1, $2, $3, $4, $5, $6,
		        COALESCE(string_to_array(NULLIF($7, ''), ','), '{}')::macaddr[],
		        COALESCE(string_to_array(NULLIF($8, ''), ','), '{}')::inet[],
		        $9, $10)
		RETURNING `+identityColumns,
		obs.InstanceID, obs.Generation, obs.MachineIDDigest, obs.SSHHostFingerprint,
		obs.CloudInitInstanceID, obs.Hostname, macCSV, ipCSV, obs.BootID, facts,
	)
	return scanIdentity(row)
}

// LatestByInstance trả quan sát mới nhất của CHÍNH instance này (khác
// FindDuplicate* vốn tra cứu instance KHÁC) — dùng bởi drift scanner để
// so sánh digest ổn định theo thời gian (Phần VIII mục 12: "identity
// digest stability"). Trả domain.ErrNotFound nếu instance chưa từng
// được quan sát lần nào.
func (r *IdentityRepository) LatestByInstance(ctx context.Context, instanceID string) (*domain.IdentityObservation, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+identityColumns+`
		FROM identity_observations
		WHERE instance_id = $1
		ORDER BY observed_at DESC
		LIMIT 1
	`, instanceID)
	return scanIdentity(row)
}

// DuplicateMatch là một instance KHÁC đã từng quan sát cùng
// machine_id_digest — dùng để quyết định BLOCK/WARN theo Phần VIII
// mục 10 (active fleet luôn hard block; lịch sử retired configurable
// qua config.Identity.DuplicatePolicy.RetiredHistory).
type DuplicateMatch struct {
	InstanceID string
	Generation int
	ObservedAt time.Time
	// Retired = true nếu instance đó đã RetiredAt != nil (Phần II mục
	// 5.1) tại thời điểm tra cứu — khớp domain.VMInstance.IsRetired().
	Retired bool
}

// FindDuplicateMachineDigest tra cứu instance KHÁC excludeInstanceID
// đã từng quan sát cùng machine_id_digest (ID-002), một dòng mới nhất
// cho mỗi instance.
func (r *IdentityRepository) FindDuplicateMachineDigest(ctx context.Context, digest, excludeInstanceID string) ([]DuplicateMatch, error) {
	return r.findDuplicateBy(ctx, "machine_id_digest", digest, excludeInstanceID)
}

// FindDuplicateSSHFingerprint tra cứu instance KHÁC excludeInstanceID
// đã từng quan sát cùng ssh_host_fingerprint (ID-003), một dòng mới
// nhất cho mỗi instance — cùng cơ chế duplicate detection với ID-002
// (Phần VIII mục 10 áp dụng chung cho mọi loại identity fact cần
// unique, không riêng machine-id).
func (r *IdentityRepository) FindDuplicateSSHFingerprint(ctx context.Context, fingerprint, excludeInstanceID string) ([]DuplicateMatch, error) {
	return r.findDuplicateBy(ctx, "ssh_host_fingerprint", fingerprint, excludeInstanceID)
}

// findDuplicateBy tra cứu duplicate theo một cột bất kỳ trong
// identity_observations (machine_id_digest hoặc ssh_host_fingerprint —
// hai cột duy nhất có index riêng, idx_identity_machine_digest và
// idx_identity_ssh_fingerprint). column không đến từ input người dùng
// (hằng số nội bộ ở hai method public trên), không phải rủi ro SQL
// injection.
func (r *IdentityRepository) findDuplicateBy(ctx context.Context, column, value, excludeInstanceID string) ([]DuplicateMatch, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ON (io.instance_id) io.instance_id, io.generation, io.observed_at, vi.retired_at
		FROM identity_observations io
		JOIN vm_instances vi ON vi.id = io.instance_id
		WHERE io.`+column+` = $1 AND io.instance_id != $2
		ORDER BY io.instance_id, io.observed_at DESC
	`, value, excludeInstanceID)
	if err != nil {
		return nil, fmt.Errorf("storage: find duplicate by %s: %w", column, err)
	}
	defer func() { _ = rows.Close() }()

	var matches []DuplicateMatch
	for rows.Next() {
		var m DuplicateMatch
		var retiredAt *time.Time
		if err := rows.Scan(&m.InstanceID, &m.Generation, &m.ObservedAt, &retiredAt); err != nil {
			return nil, fmt.Errorf("storage: scan duplicate match: %w", err)
		}
		m.Retired = retiredAt != nil
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate duplicate matches: %w", err)
	}
	return matches, nil
}

const identityColumns = `id, instance_id, generation, machine_id_digest, ssh_host_fingerprint,
	cloud_init_instance_id, hostname, array_to_string(mac_addresses, ','), array_to_string(ip_addresses, ','),
	boot_id, facts, observed_at`

type identityRowScanner interface {
	Scan(dest ...any) error
}

func scanIdentity(row identityRowScanner) (*domain.IdentityObservation, error) {
	var o domain.IdentityObservation
	var facts []byte
	var macCSV, ipCSV string
	if err := row.Scan(
		&o.ID, &o.InstanceID, &o.Generation, &o.MachineIDDigest, &o.SSHHostFingerprint,
		&o.CloudInitInstanceID, &o.Hostname, &macCSV, &ipCSV, &o.BootID, &facts, &o.ObservedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("storage: scan identity observation: %w", err)
	}
	o.Facts = facts
	if macCSV != "" {
		o.MACAddresses = strings.Split(macCSV, ",")
	}
	if ipCSV != "" {
		o.IPAddresses = strings.Split(ipCSV, ",")
	}
	return &o, nil
}
