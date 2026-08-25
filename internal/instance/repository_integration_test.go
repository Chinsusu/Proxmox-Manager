package instance

import (
	"context"
	"errors"
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

func uniqueName(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + t.Name() + "-" + time.Now().Format("150405.000000000")
}

// seedTemplate tạo cluster + template ACTIVE tối thiểu để thoả FK của
// vm_instances.template_id, đăng ký cleanup theo đúng thứ tự.
func seedTemplate(ctx context.Context, t *testing.T, db *storage.DB) string {
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

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_instances WHERE template_id = $1`, templateID)
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_templates WHERE id = $1`, templateID)
		_, _ = db.ExecContext(ctx, `DELETE FROM pve_clusters WHERE id = $1`, clusterID)
	})

	return templateID
}

func TestRepository_CreateDefaultsToRequested(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplate(ctx, t, db)
	repo := NewRepository(db)

	created, err := repo.Create(ctx, db, domain.VMInstance{
		LogicalName: uniqueName(t, "logical"),
		Hostname:    uniqueName(t, "host"),
		TemplateID:  templateID,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.State != domain.InstanceRequested {
		t.Errorf("State = %s, want REQUESTED", created.State)
	}
	if created.Generation != 1 {
		t.Errorf("Generation = %d, want 1", created.Generation)
	}
	if created.Version != 1 {
		t.Errorf("Version = %d, want 1", created.Version)
	}
	if created.VMID != nil {
		t.Errorf("VMID = %v, want nil (chưa clone)", created.VMID)
	}
}

func TestRepository_GetNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewRepository(db)

	if _, err := repo.Get(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() error = %v, want domain.ErrNotFound", err)
	}
}

func TestRepository_UpdateStateIncrementsVersion(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplate(ctx, t, db)
	repo := NewRepository(db)

	created, err := repo.Create(ctx, db, domain.VMInstance{
		LogicalName: uniqueName(t, "logical"),
		Hostname:    uniqueName(t, "host"),
		TemplateID:  templateID,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if err := repo.UpdateState(ctx, db, created.ID, domain.InstanceReserving); err != nil {
		t.Fatalf("UpdateState() error: %v", err)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.State != domain.InstanceReserving {
		t.Errorf("State = %s, want RESERVING", got.State)
	}
	if got.Version != created.Version+1 {
		t.Errorf("Version = %d, want %d", got.Version, created.Version+1)
	}
}

func TestRepository_SetPVEPlacement(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplate(ctx, t, db)
	repo := NewRepository(db)

	var clusterID string
	if err := db.QueryRowContext(ctx, `SELECT pve_cluster_id FROM vm_templates WHERE id = $1`, templateID).Scan(&clusterID); err != nil {
		t.Fatalf("lookup cluster: %v", err)
	}

	created, err := repo.Create(ctx, db, domain.VMInstance{
		LogicalName: uniqueName(t, "logical"),
		Hostname:    uniqueName(t, "host"),
		TemplateID:  templateID,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if err := repo.SetPVEPlacement(ctx, db, created.ID, clusterID, "pve01", 9101); err != nil {
		t.Fatalf("SetPVEPlacement() error: %v", err)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.PVENode == nil || *got.PVENode != "pve01" {
		t.Errorf("PVENode = %v, want pve01", got.PVENode)
	}
	if got.VMID == nil || *got.VMID != 9101 {
		t.Errorf("VMID = %v, want 9101", got.VMID)
	}
}

func TestRepository_Retire(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplate(ctx, t, db)
	repo := NewRepository(db)

	created, err := repo.Create(ctx, db, domain.VMInstance{
		LogicalName: uniqueName(t, "logical"),
		Hostname:    uniqueName(t, "host"),
		TemplateID:  templateID,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if err := repo.Retire(ctx, db, created.ID); err != nil {
		t.Fatalf("Retire() error: %v", err)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.State != domain.InstanceRetired || got.RetiredAt == nil {
		t.Fatalf("unexpected instance after Retire: %+v", got)
	}

	// GetByLogicalName chi tra active (retired_at IS NULL) - phai
	// khong tim thay nua sau khi retire.
	if _, err := repo.GetByLogicalName(ctx, created.LogicalName); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByLogicalName() after retire error = %v, want domain.ErrNotFound", err)
	}
}
