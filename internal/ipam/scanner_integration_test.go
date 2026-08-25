package ipam

import (
	"context"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

func TestScanner_ReadyInstanceWithoutAssignedIP(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	instanceID := seedInstanceForIPAM(ctx, t, db)
	findingRepo := storage.NewFindingRepository(db)
	scanner := NewScanner(db, findingRepo)

	if _, err := db.ExecContext(ctx, `UPDATE vm_instances SET state = 'READY' WHERE id = $1`, instanceID); err != nil {
		t.Fatalf("set instance READY: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM findings WHERE resource_id = $1`, instanceID)
	})

	created, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if created < 1 {
		t.Fatalf("Scan() created = %d, want >= 1 new finding", created)
	}

	findings, err := findingRepo.List(ctx, domain.FindingOpen)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Category == "ready_instance_without_assigned_ip" && f.ResourceID != nil && *f.ResourceID == instanceID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an OPEN finding for instance %s, none found in %+v", instanceID, findings)
	}

	// Chay lai lan hai: khong tao them finding trung (unique index
	// uq_findings_open + Create tra alreadyExists=true).
	createdAgain, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatalf("second Scan() error: %v", err)
	}
	if createdAgain != 0 {
		t.Fatalf("second Scan() created = %d new findings for the same instance, want 0 (should be deduped)", createdAgain)
	}
}

func TestScanner_OrphanedIPAllocation(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	segmentID := seedSegmentWithFreeIPs(ctx, t, db, 1)
	instanceID := seedInstanceForIPAM(ctx, t, db)
	ipamRepo := NewRepository(db)
	findingRepo := storage.NewFindingRepository(db)
	scanner := NewScanner(db, findingRepo)

	alloc, err := ipamRepo.ReserveNextFree(ctx, segmentID, instanceID, 5*time.Minute)
	if err != nil {
		t.Fatalf("ReserveNextFree() error: %v", err)
	}
	// Allocation nay con gan instance_id (ASSIGNED) den cuoi test theo
	// dung kich ban orphan dang test - don truoc de tranh FK violation
	// khi cleanup cua seedInstanceForIPAM xoa vm_instances (t.Cleanup
	// chay LIFO, dang ky sau se chay truoc).
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM findings WHERE resource_id = $1`, alloc.ID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM ip_allocations WHERE id = $1`, alloc.ID)
	})
	if err := ipamRepo.MarkAssigned(ctx, alloc.ID, instanceID); err != nil {
		t.Fatalf("MarkAssigned() error: %v", err)
	}
	// gia lap instance da retired ma IP chua duoc release (loi vi phai
	// giai phong IP truoc khi retire - scanner phai bat duoc).
	if _, err := db.ExecContext(ctx, `UPDATE vm_instances SET retired_at = now() WHERE id = $1`, instanceID); err != nil {
		t.Fatalf("mark instance retired: %v", err)
	}

	created, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if created < 1 {
		t.Fatalf("Scan() created = %d, want >= 1 new finding", created)
	}

	findings, err := findingRepo.List(ctx, domain.FindingOpen)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Category == "orphaned_ip_allocation" && f.ResourceID != nil && *f.ResourceID == alloc.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an OPEN finding for allocation %s, none found", alloc.ID)
	}
}
