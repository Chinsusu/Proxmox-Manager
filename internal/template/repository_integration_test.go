package template

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

// seedCluster tạo một pve_clusters row tối thiểu để thoả FK của
// vm_templates, đăng ký cleanup xoá theo đúng thứ tự ngược.
func seedCluster(ctx context.Context, t *testing.T, db *storage.DB) string {
	t.Helper()
	var clusterID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO pve_clusters (name, base_url, secret_ref) VALUES ($1, $2, $3) RETURNING id
	`, uniqueName(t, "cluster"), "https://pve.test:8006/api2/json", "secret_ref_test").Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_templates WHERE pve_cluster_id = $1`, clusterID)
		_, _ = db.ExecContext(ctx, `DELETE FROM pve_clusters WHERE id = $1`, clusterID)
	})
	return clusterID
}

func newDraftTemplate(family, clusterID string, vmid int) domain.Template {
	return domain.Template{
		Name:            family + "-tpl",
		Family:          family,
		Version:         "2026.08.1",
		OSFamily:        "ubuntu",
		OSVersion:       "22.04",
		Architecture:    "amd64",
		PVEClusterID:    clusterID,
		PVENode:         "pve01",
		PVETemplateVMID: vmid,
		SourceChecksum:  "deadbeef",
	}
}

func TestRepository_CreateDefaultsToDraft(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	clusterID := seedCluster(ctx, t, db)
	repo := NewRepository(db)

	family := uniqueName(t, "family")
	created, err := repo.Create(ctx, newDraftTemplate(family, clusterID, 9001))
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.State != domain.TemplateDraft {
		t.Errorf("State = %s, want DRAFT", created.State)
	}
	if created.ValidationStatus != domain.ValidationUnknown {
		t.Errorf("ValidationStatus = %s, want UNKNOWN", created.ValidationStatus)
	}
	if len(created.CloneModeAllowed) != 1 || created.CloneModeAllowed[0] != "full" {
		t.Errorf("CloneModeAllowed = %v, want [full]", created.CloneModeAllowed)
	}
}

func TestRepository_PromoteValidPath(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	clusterID := seedCluster(ctx, t, db)
	repo := NewRepository(db)

	family := uniqueName(t, "family")
	created, err := repo.Create(ctx, newDraftTemplate(family, clusterID, 9002))
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	candidate, err := repo.Promote(ctx, created.ID, domain.TemplateCandidate)
	if err != nil {
		t.Fatalf("Promote(CANDIDATE) error: %v", err)
	}
	if candidate.State != domain.TemplateCandidate {
		t.Fatalf("State = %s, want CANDIDATE", candidate.State)
	}

	active, err := repo.Promote(ctx, created.ID, domain.TemplateActive)
	if err != nil {
		t.Fatalf("Promote(ACTIVE) error: %v", err)
	}
	if active.State != domain.TemplateActive {
		t.Fatalf("State = %s, want ACTIVE", active.State)
	}

	got, err := repo.GetActiveByFamily(ctx, family)
	if err != nil {
		t.Fatalf("GetActiveByFamily() error: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("GetActiveByFamily() id = %s, want %s", got.ID, created.ID)
	}
}

func TestRepository_PromoteInvalidTransitionRejected(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	clusterID := seedCluster(ctx, t, db)
	repo := NewRepository(db)

	family := uniqueName(t, "family")
	created, err := repo.Create(ctx, newDraftTemplate(family, clusterID, 9003))
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// DRAFT -> ACTIVE truc tiep khong hop le, phai qua CANDIDATE
	// (Phan IV muc 9).
	if _, err := repo.Promote(ctx, created.ID, domain.TemplateActive); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Promote(DRAFT->ACTIVE) error = %v, want domain.ErrInvalidTransition", err)
	}
}

// TestRepository_PromoteActive_DemotesPreviousActiveInSameFamily la
// test truc tiep cho invariant "mot template family co mot ACTIVE
// default" (Phan IV muc 9).
func TestRepository_PromoteActive_DemotesPreviousActiveInSameFamily(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	clusterID := seedCluster(ctx, t, db)
	repo := NewRepository(db)

	family := uniqueName(t, "family")

	v1, err := repo.Create(ctx, newDraftTemplate(family, clusterID, 9004))
	if err != nil {
		t.Fatalf("Create(v1) error: %v", err)
	}
	if _, err := repo.Promote(ctx, v1.ID, domain.TemplateCandidate); err != nil {
		t.Fatalf("Promote(v1, CANDIDATE) error: %v", err)
	}
	if _, err := repo.Promote(ctx, v1.ID, domain.TemplateActive); err != nil {
		t.Fatalf("Promote(v1, ACTIVE) error: %v", err)
	}

	v2 := newDraftTemplate(family, clusterID, 9005)
	v2.Version = "2026.08.2"
	created2, err := repo.Create(ctx, v2)
	if err != nil {
		t.Fatalf("Create(v2) error: %v", err)
	}
	if _, err := repo.Promote(ctx, created2.ID, domain.TemplateCandidate); err != nil {
		t.Fatalf("Promote(v2, CANDIDATE) error: %v", err)
	}
	if _, err := repo.Promote(ctx, created2.ID, domain.TemplateActive); err != nil {
		t.Fatalf("Promote(v2, ACTIVE) error: %v", err)
	}

	gotV1, err := repo.Get(ctx, v1.ID)
	if err != nil {
		t.Fatalf("Get(v1) error: %v", err)
	}
	if gotV1.State != domain.TemplateDeprecated {
		t.Fatalf("v1 State = %s, want DEPRECATED (phai bi demote khi v2 len ACTIVE)", gotV1.State)
	}

	active, err := repo.GetActiveByFamily(ctx, family)
	if err != nil {
		t.Fatalf("GetActiveByFamily() error: %v", err)
	}
	if active.ID != created2.ID {
		t.Fatalf("active template = %s, want v2 (%s)", active.ID, created2.ID)
	}
}

func TestRepository_RollbackDeprecatedToActive(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	clusterID := seedCluster(ctx, t, db)
	repo := NewRepository(db)

	family := uniqueName(t, "family")
	v1, err := repo.Create(ctx, newDraftTemplate(family, clusterID, 9006))
	if err != nil {
		t.Fatalf("Create(v1) error: %v", err)
	}
	if _, err := repo.Promote(ctx, v1.ID, domain.TemplateCandidate); err != nil {
		t.Fatalf("Promote(CANDIDATE) error: %v", err)
	}
	if _, err := repo.Promote(ctx, v1.ID, domain.TemplateActive); err != nil {
		t.Fatalf("Promote(ACTIVE) error: %v", err)
	}
	if _, err := repo.Promote(ctx, v1.ID, domain.TemplateDeprecated); err != nil {
		t.Fatalf("Promote(DEPRECATED) error: %v", err)
	}

	// Rollback: promote lai ban DEPRECATED thanh ACTIVE, khong mutate
	// noi dung ban loi (Phan IV muc 9).
	rolledBack, err := repo.Promote(ctx, v1.ID, domain.TemplateActive)
	if err != nil {
		t.Fatalf("Promote(DEPRECATED->ACTIVE, rollback) error: %v", err)
	}
	if rolledBack.State != domain.TemplateActive {
		t.Fatalf("State = %s, want ACTIVE after rollback", rolledBack.State)
	}
}

func TestRepository_SetValidationStatus(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	clusterID := seedCluster(ctx, t, db)
	repo := NewRepository(db)

	family := uniqueName(t, "family")
	created, err := repo.Create(ctx, newDraftTemplate(family, clusterID, 9007))
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if err := repo.SetValidationStatus(ctx, created.ID, domain.ValidationPass); err != nil {
		t.Fatalf("SetValidationStatus() error: %v", err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.ValidationStatus != domain.ValidationPass {
		t.Fatalf("ValidationStatus = %s, want PASS", got.ValidationStatus)
	}
}
