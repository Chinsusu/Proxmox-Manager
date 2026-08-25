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
