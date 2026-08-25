package ipam

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// openTestDB skip test nếu không có DATABASE_URL — xem lý do tương tự
// internal/jobs/repository_integration_test.go.
func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	db, err := storage.Open(dsn, 10, 5)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ready(); err != nil {
		t.Fatalf("db not ready: %v", err)
	}
	return db
}

func uniqueName(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + t.Name() + "-" + time.Now().Format("150405.000000000")
}

// seedSegmentWithFreeIPs tạo một network_segment với numAddresses địa
// chỉ FREE liên tiếp, trả về segmentID. Đăng ký cleanup xoá segment và
// mọi allocation của nó.
func seedSegmentWithFreeIPs(ctx context.Context, t *testing.T, db *storage.DB, numAddresses int) string {
	t.Helper()

	var segmentID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO network_segments (name, cidr, gateway, bridge)
		VALUES ($1, '10.99.0.0/24', '10.99.0.1', 'vmbr99')
		RETURNING id
	`, uniqueName(t, "segment")).Scan(&segmentID); err != nil {
		t.Fatalf("seed segment: %v", err)
	}

	for i := 0; i < numAddresses; i++ {
		addr := fmt.Sprintf("10.99.0.%d", 10+i)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO ip_allocations (segment_id, address, state) VALUES ($1, $2, 'FREE')
		`, segmentID, addr); err != nil {
			t.Fatalf("seed free address %s: %v", addr, err)
		}
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM ip_allocations WHERE segment_id = $1`, segmentID)
		_, _ = db.ExecContext(ctx, `DELETE FROM network_segments WHERE id = $1`, segmentID)
	})

	return segmentID
}

func seedInstanceForIPAM(ctx context.Context, t *testing.T, db *storage.DB) string {
	t.Helper()

	var clusterID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO pve_clusters (name, base_url, secret_ref) VALUES ($1, $2, $3) RETURNING id
	`, uniqueName(t, "cluster"), "https://pve.test:8006/api2/json", "secret_ref_test").Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	var templateID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO vm_templates
			(name, family, version, os_family, os_version, architecture,
			 pve_cluster_id, pve_node, pve_template_vmid, source_checksum, state)
		VALUES ($1, $2, '2026.01.1', 'ubuntu', '22.04', 'amd64', $3, 'pve01', 9000, 'deadbeef', 'ACTIVE')
		RETURNING id
	`, uniqueName(t, "tpl"), uniqueName(t, "family"), clusterID).Scan(&templateID); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	var instanceID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO vm_instances (logical_name, hostname, template_id) VALUES ($1, $2, $3) RETURNING id
	`, uniqueName(t, "logical"), uniqueName(t, "host"), templateID).Scan(&instanceID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_instances WHERE id = $1`, instanceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_templates WHERE id = $1`, templateID)
		_, _ = db.ExecContext(ctx, `DELETE FROM pve_clusters WHERE id = $1`, clusterID)
	})

	return instanceID
}

func TestRepository_ReserveNextFree_HappyPath(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	segmentID := seedSegmentWithFreeIPs(ctx, t, db, 1)
	instanceID := seedInstanceForIPAM(ctx, t, db)
	repo := NewRepository(db)

	alloc, err := repo.ReserveNextFree(ctx, segmentID, instanceID, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReserveNextFree() error: %v", err)
	}
	if alloc.State != domain.AllocationReserved {
		t.Fatalf("state = %s, want RESERVED", alloc.State)
	}
	if alloc.InstanceID == nil || *alloc.InstanceID != instanceID {
		t.Fatalf("instance_id = %v, want %s", alloc.InstanceID, instanceID)
	}
}

func TestRepository_ReserveNextFree_ExhaustedReturnsCapacityError(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	segmentID := seedSegmentWithFreeIPs(ctx, t, db, 0)
	instanceID := seedInstanceForIPAM(ctx, t, db)
	repo := NewRepository(db)

	_, err := repo.ReserveNextFree(ctx, segmentID, instanceID, 5*time.Minute)
	if !errors.Is(err, domain.ErrCapacityExhausted) {
		t.Fatalf("error = %v, want domain.ErrCapacityExhausted", err)
	}
}

// TestRepository_ReserveNextFree_ConcurrentWorkers_NoDuplicateAddress la
// test truc tiep cho acceptance criteria P0-01: "race tests show no
// duplicate IP" (docs/10 muc 10, Epic P0-01).
func TestRepository_ReserveNextFree_ConcurrentWorkers_NoDuplicateAddress(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewRepository(db)

	const numAddresses = 6
	const numWorkers = 15

	segmentID := seedSegmentWithFreeIPs(ctx, t, db, numAddresses)

	var (
		mu           sync.Mutex
		reservedAddr = make(map[string]string) // address -> instanceID
		wg           sync.WaitGroup
		exhaustedCnt int64
	)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			instanceID := seedInstanceForIPAM(ctx, t, db)
			alloc, err := repo.ReserveNextFree(ctx, segmentID, instanceID, 5*time.Minute)
			if err != nil {
				if errors.Is(err, domain.ErrCapacityExhausted) {
					atomic.AddInt64(&exhaustedCnt, 1)
					return
				}
				t.Errorf("worker %d ReserveNextFree() unexpected error: %v", idx, err)
				return
			}
			mu.Lock()
			if prevOwner, exists := reservedAddr[alloc.Address]; exists {
				t.Errorf("address %s reserved for both %s and %s — duplicate allocation", alloc.Address, prevOwner, instanceID)
			}
			reservedAddr[alloc.Address] = instanceID
			mu.Unlock()
		}(w)
	}
	wg.Wait()

	if len(reservedAddr) != numAddresses {
		t.Fatalf("reserved %d distinct addresses, want exactly %d (numWorkers=%d, exhausted=%d)",
			len(reservedAddr), numAddresses, numWorkers, atomic.LoadInt64(&exhaustedCnt))
	}
	if int64(numWorkers-numAddresses) != exhaustedCnt {
		t.Fatalf("exhausted count = %d, want %d", exhaustedCnt, numWorkers-numAddresses)
	}
}

func TestRepository_MarkAssignedAndRelease(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	segmentID := seedSegmentWithFreeIPs(ctx, t, db, 1)
	instanceID := seedInstanceForIPAM(ctx, t, db)
	repo := NewRepository(db)

	alloc, err := repo.ReserveNextFree(ctx, segmentID, instanceID, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReserveNextFree() error: %v", err)
	}

	if err := repo.MarkAssigned(ctx, alloc.ID, instanceID); err != nil {
		t.Fatalf("MarkAssigned() error: %v", err)
	}
	got, err := repo.Get(ctx, alloc.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.State != domain.AllocationAssigned || got.AssignedAt == nil {
		t.Fatalf("unexpected allocation after MarkAssigned: %+v", got)
	}

	if err := repo.Release(ctx, alloc.ID); err != nil {
		t.Fatalf("Release() error: %v", err)
	}
	got, err = repo.Get(ctx, alloc.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.State != domain.AllocationFree || got.InstanceID != nil || got.ReleasedAt == nil {
		t.Fatalf("unexpected allocation after Release: %+v", got)
	}
}
