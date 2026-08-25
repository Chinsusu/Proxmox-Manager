package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

func uniqueFingerprint(t *testing.T, suffix string) string {
	t.Helper()
	return "test-" + t.Name() + "-" + suffix + "-" + time.Now().Format("150405.000000000")
}

func TestAlertRepository_UpsertThenList(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewAlertRepository(db)
	fp := uniqueFingerprint(t, "a")
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM alerts WHERE fingerprint = $1`, fp) })

	if err := repo.Upsert(ctx, domain.Alert{
		Fingerprint: fp, Severity: "warning", ResourceType: "system", ResourceID: "backlog",
		Title: "test alert", Description: "first",
	}); err != nil {
		t.Fatalf("Upsert() error: %v", err)
	}

	got, err := repo.List(ctx, AlertListFilter{ResourceType: "system"}, time.Time{}, "", 100)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	var found *domain.Alert
	for i := range got {
		if got[i].Fingerprint == fp {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatalf("List() did not include fingerprint %s among %+v", fp, got)
	}
	if found.Status != domain.AlertFiring {
		t.Errorf("Status = %s, want firing", found.Status)
	}
	if found.Version != 1 {
		t.Errorf("Version = %d, want 1 (lan dau)", found.Version)
	}
}

func TestAlertRepository_UpsertTwice_KeepsAcknowledgedStatus(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewAlertRepository(db)
	fp := uniqueFingerprint(t, "b")
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM alerts WHERE fingerprint = $1`, fp) })

	if err := repo.Upsert(ctx, domain.Alert{Fingerprint: fp, Severity: "critical", ResourceType: "vm_instance", ResourceID: "ins-1", Title: "t"}); err != nil {
		t.Fatalf("Upsert() #1 error: %v", err)
	}
	got, err := repo.List(ctx, AlertListFilter{ResourceType: "vm_instance"}, time.Time{}, "", 100)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	var id string
	for _, a := range got {
		if a.Fingerprint == fp {
			id = a.ID
		}
	}
	if id == "" {
		t.Fatalf("could not find seeded alert %s", fp)
	}

	if _, err := repo.Acknowledge(ctx, id, "operator-1", nil, 0); err != nil {
		t.Fatalf("Acknowledge() error: %v", err)
	}

	// Dieu kien tai dien (upsert lai voi cung fingerprint) - KHONG duoc
	// "quen" operator da ack, phai giu status acknowledged.
	if err := repo.Upsert(ctx, domain.Alert{Fingerprint: fp, Severity: "critical", ResourceType: "vm_instance", ResourceID: "ins-1", Title: "t", Description: "recurred"}); err != nil {
		t.Fatalf("Upsert() #2 error: %v", err)
	}
	after, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if after.Status != domain.AlertAcknowledged {
		t.Errorf("Status after re-upsert = %s, want acknowledged (khong duoc reset ve firing)", after.Status)
	}
}

func TestAlertRepository_Acknowledge_VersionConflict(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewAlertRepository(db)
	fp := uniqueFingerprint(t, "c")
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM alerts WHERE fingerprint = $1`, fp) })

	if err := repo.Upsert(ctx, domain.Alert{Fingerprint: fp, Severity: "warning", ResourceType: "system", ResourceID: "x", Title: "t"}); err != nil {
		t.Fatalf("Upsert() error: %v", err)
	}
	got, err := repo.List(ctx, AlertListFilter{ResourceType: "system"}, time.Time{}, "", 100)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	var id string
	for _, a := range got {
		if a.Fingerprint == fp {
			id = a.ID
		}
	}

	if _, err := repo.Acknowledge(ctx, id, "operator-1", nil, 999); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("Acknowledge() with stale version error = %v, want domain.ErrVersionConflict", err)
	}
}

func TestAlertRepository_Acknowledge_NotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewAlertRepository(db)

	if _, err := repo.Acknowledge(ctx, "00000000-0000-0000-0000-000000000000", "operator-1", nil, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Acknowledge() error = %v, want domain.ErrNotFound", err)
	}
}
