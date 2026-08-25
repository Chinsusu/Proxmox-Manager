package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

func TestValidationRunRepository_CreateAndLatestByType(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	inst := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewValidationRunRepository(db)

	evidence, _ := json.Marshal(map[string]any{"ruleset_version": "identity-network-egress-1.0", "checks": []string{}})
	started := time.Now().Add(-time.Second)
	created, err := repo.Create(ctx, db, domain.ValidationRun{
		InstanceID:     inst.ID,
		Type:           "identity",
		Result:         domain.ValidationPass,
		RulesetVersion: "identity-network-egress-1.0",
		Evidence:       evidence,
		StartedAt:      started,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if created.Result != domain.ValidationPass {
		t.Errorf("Result = %s, want PASS", created.Result)
	}
	if created.FinishedAt == nil {
		t.Error("FinishedAt should default to StartedAt when caller doesn't set it")
	}

	got, err := repo.LatestByType(ctx, inst.ID, "identity")
	if err != nil {
		t.Fatalf("LatestByType() error: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("LatestByType() ID = %s, want %s", got.ID, created.ID)
	}
}

func TestValidationRunRepository_LatestByType_ReturnsMostRecent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	inst := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewValidationRunRepository(db)

	older, err := repo.Create(ctx, db, domain.ValidationRun{
		InstanceID: inst.ID, Type: "identity", Result: domain.ValidationFail,
		RulesetVersion: "v1", StartedAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("create older run: %v", err)
	}
	newer, err := repo.Create(ctx, db, domain.ValidationRun{
		InstanceID: inst.ID, Type: "identity", Result: domain.ValidationPass,
		RulesetVersion: "v1", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create newer run: %v", err)
	}
	if older.ID == newer.ID {
		t.Fatal("older and newer runs must have distinct IDs")
	}

	got, err := repo.LatestByType(ctx, inst.ID, "identity")
	if err != nil {
		t.Fatalf("LatestByType() error: %v", err)
	}
	if got.ID != newer.ID {
		t.Errorf("LatestByType() returned %s, want the newer run %s", got.ID, newer.ID)
	}
}

func TestValidationRunRepository_LatestByType_NotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	inst := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewValidationRunRepository(db)

	_, err := repo.LatestByType(ctx, inst.ID, "egress")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("LatestByType() error = %v, want domain.ErrNotFound", err)
	}
}

func TestValidationRunRepository_LatestPerType(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	inst := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewValidationRunRepository(db)

	// identity: hai lan chay, chi lan moi nhat duoc tra ve.
	olderIdentity, err := repo.Create(ctx, db, domain.ValidationRun{
		InstanceID: inst.ID, Type: "identity", Result: domain.ValidationFail,
		RulesetVersion: "v1", StartedAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("create older identity run: %v", err)
	}
	newerIdentity, err := repo.Create(ctx, db, domain.ValidationRun{
		InstanceID: inst.ID, Type: "identity", Result: domain.ValidationPass,
		RulesetVersion: "v1", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create newer identity run: %v", err)
	}
	egressRun, err := repo.Create(ctx, db, domain.ValidationRun{
		InstanceID: inst.ID, Type: "egress", Result: domain.ValidationWarn,
		RulesetVersion: "v1", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create egress run: %v", err)
	}

	runs, err := repo.LatestPerType(ctx, inst.ID)
	if err != nil {
		t.Fatalf("LatestPerType() error: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2 (identity + egress)", len(runs))
	}
	byType := map[string]domain.ValidationRun{}
	for _, r := range runs {
		byType[r.Type] = r
	}
	if byType["identity"].ID != newerIdentity.ID {
		t.Errorf("identity run = %s, want newer run %s (not older %s)", byType["identity"].ID, newerIdentity.ID, olderIdentity.ID)
	}
	if byType["egress"].ID != egressRun.ID {
		t.Errorf("egress run = %s, want %s", byType["egress"].ID, egressRun.ID)
	}
}

// TestValidationRunRepository_List bao phu GET /v1/validations (UI
// integration, API_UI_Gap_Register mục 3.4).
func TestValidationRunRepository_List(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	inst := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewValidationRunRepository(db)

	failRun, err := repo.Create(ctx, db, domain.ValidationRun{
		InstanceID: inst.ID, Type: "identity", Result: domain.ValidationFail,
		RulesetVersion: "v1", StartedAt: time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create fail run: %v", err)
	}
	passRun, err := repo.Create(ctx, db, domain.ValidationRun{
		InstanceID: inst.ID, Type: "egress", Result: domain.ValidationPass,
		RulesetVersion: "v1", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create pass run: %v", err)
	}

	t.Run("filter by result", func(t *testing.T) {
		got, err := repo.List(ctx, ValidationListFilter{InstanceID: inst.ID, Result: string(domain.ValidationFail)}, time.Time{}, "", 100)
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(got) != 1 || got[0].ID != failRun.ID {
			t.Fatalf("List(result=FAIL) = %+v, want exactly %s", got, failRun.ID)
		}
	})

	t.Run("filter by type", func(t *testing.T) {
		got, err := repo.List(ctx, ValidationListFilter{InstanceID: inst.ID, Type: "egress"}, time.Time{}, "", 100)
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(got) != 1 || got[0].ID != passRun.ID {
			t.Fatalf("List(type=egress) = %+v, want exactly %s", got, passRun.ID)
		}
	})

	t.Run("no filter returns both newest first", func(t *testing.T) {
		got, err := repo.List(ctx, ValidationListFilter{InstanceID: inst.ID}, time.Time{}, "", 100)
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(got) != 2 || got[0].ID != passRun.ID || got[1].ID != failRun.ID {
			t.Fatalf("List() = %+v, want [%s,%s] newest-first", got, passRun.ID, failRun.ID)
		}
	})
}
