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

// TestRepository_List_OrderAndPagination khong assert so luong tuyet
// doi (bang vm_instances dung chung giua nhieu test/package tren cung
// Postgres CI - bai hoc tu P0-03) - chi loc ket qua ve 3 instance minh
// tao, xac nhan thu tu (moi nhat truoc) va keyset pagination tiep tuc
// dung tu cursor.
func TestRepository_List_OrderAndPagination(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplate(ctx, t, db)
	repo := NewRepository(db)

	var created []*domain.VMInstance
	for i := 0; i < 3; i++ {
		inst, err := repo.Create(ctx, db, domain.VMInstance{
			LogicalName: uniqueName(t, "logical"), Hostname: uniqueName(t, "host"), TemplateID: templateID,
		})
		if err != nil {
			t.Fatalf("Create() #%d error: %v", i, err)
		}
		created = append(created, inst)
		// dam bao created_at tang don dieu ngay ca khi do phan giai
		// dong ho khong du de tach hai lan INSERT lien tiep.
		time.Sleep(5 * time.Millisecond)
	}
	knownIDs := map[string]int{created[0].ID: 0, created[1].ID: 1, created[2].ID: 2}

	filterKnown := func(items []domain.VMInstance) []domain.VMInstance {
		var out []domain.VMInstance
		for _, it := range items {
			if _, ok := knownIDs[it.ID]; ok {
				out = append(out, it)
			}
		}
		return out
	}

	all, err := repo.List(ctx, time.Time{}, "", 1000)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	mine := filterKnown(all)
	if len(mine) != 3 {
		t.Fatalf("filtered list = %d instances, want 3 (co the co instance tao ngoai test bi loc sai)", len(mine))
	}
	if mine[0].ID != created[2].ID || mine[1].ID != created[1].ID || mine[2].ID != created[0].ID {
		t.Fatalf("thu tu List() = [%s,%s,%s], want moi nhat truoc [%s,%s,%s]",
			mine[0].ID, mine[1].ID, mine[2].ID, created[2].ID, created[1].ID, created[0].ID)
	}

	// Keyset pagination: dung item moi nhat (mine[0] = created[2]) lam
	// cursor, List() tiep theo phai bo qua no va tra dung 2 item con
	// lai theo thu tu (created[1] roi created[0]).
	cursorAfter := mine[0]
	next, err := repo.List(ctx, cursorAfter.CreatedAt, cursorAfter.ID, 1000)
	if err != nil {
		t.Fatalf("List() after cursor error: %v", err)
	}
	nextMine := filterKnown(next)
	if len(nextMine) != 2 || nextMine[0].ID != created[1].ID || nextMine[1].ID != created[0].ID {
		t.Fatalf("List() sau cursor = %+v, want [%s,%s]", nextMine, created[1].ID, created[0].ID)
	}
}

func TestRepository_FindCurrentJobID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplate(ctx, t, db)
	repo := NewRepository(db)

	created, err := repo.Create(ctx, db, domain.VMInstance{
		LogicalName: uniqueName(t, "logical"), Hostname: uniqueName(t, "host"), TemplateID: templateID,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if _, err := repo.FindCurrentJobID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("FindCurrentJobID() chua co job, error = %v, want domain.ErrNotFound", err)
	}

	var jobID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO provisioning_jobs (instance_id, operation, state, checkpoint) VALUES ($1, 'PROVISION', 'QUEUED', 'REQUESTED') RETURNING id
	`, created.ID).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DELETE FROM provisioning_jobs WHERE id = $1`, jobID) })

	got, err := repo.FindCurrentJobID(ctx, created.ID)
	if err != nil {
		t.Fatalf("FindCurrentJobID() error: %v", err)
	}
	if got != jobID {
		t.Errorf("FindCurrentJobID() = %s, want %s", got, jobID)
	}
}
