package ipam

import (
	"context"
	"errors"
	"testing"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

func TestSegmentRepository_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewSegmentRepository(db)

	name := uniqueName(t, "segment")
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM network_segments WHERE name = $1`, name)
	})

	created, err := repo.Create(ctx, domain.NetworkSegment{
		Name:       name,
		CIDR:       "10.55.0.0/24",
		Gateway:    "10.55.0.1",
		Bridge:     "vmbr1",
		DNSServers: []string{"8.8.8.8", "1.1.1.1"},
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.IPv6Policy != "deny" {
		t.Errorf("IPv6Policy default = %q, want deny", created.IPv6Policy)
	}
	if created.AllocationStrategy != "sequential-lowest-free" {
		t.Errorf("AllocationStrategy default = %q", created.AllocationStrategy)
	}
	if len(created.DNSServers) != 2 || created.DNSServers[0] != "8.8.8.8" {
		t.Errorf("DNSServers = %v", created.DNSServers)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Name != name || got.Bridge != "vmbr1" {
		t.Errorf("Get() = %+v", got)
	}

	byName, err := repo.GetByName(ctx, name)
	if err != nil {
		t.Fatalf("GetByName() error: %v", err)
	}
	if byName.ID != created.ID {
		t.Errorf("GetByName() id = %s, want %s", byName.ID, created.ID)
	}
}

func TestSegmentRepository_GetNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewSegmentRepository(db)

	if _, err := repo.GetByName(ctx, "does-not-exist-"+uniqueName(t, "x")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByName() error = %v, want domain.ErrNotFound", err)
	}
}

func TestSegmentRepository_List(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewSegmentRepository(db)

	name := uniqueName(t, "segment-list")
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM network_segments WHERE name = $1`, name)
	})
	if _, err := repo.Create(ctx, domain.NetworkSegment{
		Name: name, CIDR: "10.56.0.0/24", Gateway: "10.56.0.1", Bridge: "vmbr1",
	}); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	segments, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	found := false
	for _, s := range segments {
		if s.Name == name {
			found = true
		}
	}
	if !found {
		t.Errorf("List() did not include created segment %q", name)
	}
}
