package audit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

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

// resource_id không có FK trong audit_events (Phần VI mục 2.10 —
// append-only, độc lập với vòng đời resource) nên test này tự sinh một
// resource_id giả, không cần seed instance/job thật.
func uniqueResourceID(t *testing.T) string {
	t.Helper()
	return "test-resource-" + t.Name() + "-" + time.Now().Format("150405.000000000")
}

func TestReader_ListByResource_OrderAndPagination(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	writer := NewWriter()
	reader := NewReader(db)

	resourceID := uniqueResourceID(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM audit_events WHERE resource_id = $1`, resourceID)
	})

	for i := 0; i < 3; i++ {
		if err := writer.Append(ctx, db, domain.AuditEvent{
			ActorType: "system", ActorID: "test", Action: "state_transition",
			ResourceType: "vm_instance", ResourceID: resourceID,
		}); err != nil {
			t.Fatalf("Append() #%d error: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	all, err := reader.ListByResource(ctx, "vm_instance", resourceID, time.Time{}, "", 1000)
	if err != nil {
		t.Fatalf("ListByResource() error: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3", len(all))
	}
	// occurred_at DESC - moi nhat truoc.
	if !all[0].OccurredAt.After(all[1].OccurredAt) || !all[1].OccurredAt.After(all[2].OccurredAt) {
		t.Fatalf("thu tu khong phai moi nhat truoc: %v, %v, %v", all[0].OccurredAt, all[1].OccurredAt, all[2].OccurredAt)
	}

	newest := all[0]
	rest, err := reader.ListByResource(ctx, "vm_instance", resourceID, newest.OccurredAt, newest.ID, 1000)
	if err != nil {
		t.Fatalf("ListByResource() after cursor error: %v", err)
	}
	if len(rest) != 2 || rest[0].ID != all[1].ID || rest[1].ID != all[2].ID {
		t.Fatalf("ListByResource() sau cursor = %+v, want [%s,%s]", rest, all[1].ID, all[2].ID)
	}
}

func TestReader_ListByResource_DifferentResourceIsolated(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	writer := NewWriter()
	reader := NewReader(db)

	resourceA := uniqueResourceID(t) + "-a"
	resourceB := uniqueResourceID(t) + "-b"
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM audit_events WHERE resource_id IN ($1, $2)`, resourceA, resourceB)
	})

	if err := writer.Append(ctx, db, domain.AuditEvent{
		ActorType: "system", ActorID: "test", Action: "state_transition", ResourceType: "vm_instance", ResourceID: resourceA,
	}); err != nil {
		t.Fatalf("Append() resourceA error: %v", err)
	}
	if err := writer.Append(ctx, db, domain.AuditEvent{
		ActorType: "system", ActorID: "test", Action: "state_transition", ResourceType: "vm_instance", ResourceID: resourceB,
	}); err != nil {
		t.Fatalf("Append() resourceB error: %v", err)
	}

	got, err := reader.ListByResource(ctx, "vm_instance", resourceA, time.Time{}, "", 1000)
	if err != nil {
		t.Fatalf("ListByResource() error: %v", err)
	}
	if len(got) != 1 || got[0].ResourceID != resourceA {
		t.Fatalf("ListByResource(resourceA) = %+v, want exactly 1 event cho resourceA", got)
	}
}
