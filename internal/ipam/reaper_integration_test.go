package ipam

import (
	"context"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
)

func TestReaper_ReleasesExpiredReservationWithoutActiveJob(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	segmentID := seedSegmentWithFreeIPs(ctx, t, db, 1)
	instanceID := seedInstanceForIPAM(ctx, t, db)
	ipamRepo := NewRepository(db)
	reaper := NewReaper(db, audit.NewWriter())

	alloc, err := ipamRepo.ReserveNextFree(ctx, segmentID, instanceID, time.Second)
	if err != nil {
		t.Fatalf("ReserveNextFree() error: %v", err)
	}
	// gia lap TTL da het han (khong the reserve voi ttl am truc tiep).
	if _, err := db.ExecContext(ctx, `UPDATE ip_allocations SET reserved_until = now() - interval '1 minute' WHERE id = $1`, alloc.ID); err != nil {
		t.Fatalf("force-expire reservation: %v", err)
	}

	n, err := reaper.ReleaseExpiredReservations(ctx)
	if err != nil {
		t.Fatalf("ReleaseExpiredReservations() error: %v", err)
	}
	if n < 1 {
		t.Fatalf("released count = %d, want >= 1", n)
	}

	got, err := ipamRepo.Get(ctx, alloc.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.State != domain.AllocationFree || got.InstanceID != nil {
		t.Fatalf("allocation not released correctly: %+v", got)
	}

	var auditCount int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM audit_events WHERE resource_type = 'ip_allocation' AND resource_id = $1 AND action = 'release_expired_reservation'
	`, alloc.ID).Scan(&auditCount); err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	if auditCount < 1 {
		t.Fatal("expected an audit_events row for the released reservation, found none")
	}
}

func TestReaper_DoesNotReleaseWhenJobStillActive(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	segmentID := seedSegmentWithFreeIPs(ctx, t, db, 1)
	instanceID := seedInstanceForIPAM(ctx, t, db)
	ipamRepo := NewRepository(db)
	reaper := NewReaper(db, audit.NewWriter())

	if _, err := db.ExecContext(ctx, `
		INSERT INTO provisioning_jobs (instance_id, operation, state, checkpoint)
		VALUES ($1, 'PROVISION', 'QUEUED', 'REQUESTED')
	`, instanceID); err != nil {
		t.Fatalf("seed active job: %v", err)
	}
	// QUAN TRONG: job nay o state QUEUED, se bi Claim() cua goi ipam
	// khac (vd internal/jobs) nhat nham neu khong don - Claim() lay
	// job kha dung BAT KY trong toan bang, khong loc theo instance.
	// Chay CI thuc te da bat duoc loi nay (Claim o package khac lay
	// nham job con sot lai o day khi 2 package chay song song tren
	// cung postgres). Don ngay, dang ky truoc cleanup cua
	// seedInstanceForIPAM de chay truoc no (t.Cleanup la LIFO), tranh
	// FK violation am tham khi xoa vm_instances.
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM provisioning_jobs WHERE instance_id = $1`, instanceID)
	})

	alloc, err := ipamRepo.ReserveNextFree(ctx, segmentID, instanceID, time.Second)
	if err != nil {
		t.Fatalf("ReserveNextFree() error: %v", err)
	}
	// Allocation nay se van con RESERVED (gan instance_id) den cuoi test
	// vi reaper duoc ky vong KHONG release no - don truoc de tranh FK
	// violation khi cleanup cua seedInstanceForIPAM xoa vm_instances
	// (t.Cleanup chay LIFO, dang ky sau se chay truoc).
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM ip_allocations WHERE id = $1`, alloc.ID)
	})
	if _, err := db.ExecContext(ctx, `UPDATE ip_allocations SET reserved_until = now() - interval '1 minute' WHERE id = $1`, alloc.ID); err != nil {
		t.Fatalf("force-expire reservation: %v", err)
	}

	if _, err := reaper.ReleaseExpiredReservations(ctx); err != nil {
		t.Fatalf("ReleaseExpiredReservations() error: %v", err)
	}

	got, err := ipamRepo.Get(ctx, alloc.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.State != domain.AllocationReserved {
		t.Fatalf("allocation state = %s, want still RESERVED (job active) — reaper released it incorrectly", got.State)
	}
}
