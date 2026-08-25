package ipam

import (
	"context"
	"sync"
	"testing"
)

func TestHostnameRepository_Sequential(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewHostnameRepository(db)

	prefix := uniqueName(t, "node")
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM hostname_sequences WHERE prefix = $1`, prefix)
	})

	first, err := repo.Next(ctx, prefix)
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if first != prefix+"-0001" {
		t.Fatalf("first hostname = %q, want %q", first, prefix+"-0001")
	}

	second, err := repo.Next(ctx, prefix)
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if second != prefix+"-0002" {
		t.Fatalf("second hostname = %q, want %q", second, prefix+"-0002")
	}
}

// TestHostnameRepository_ConcurrentNoDuplicate verify hostname allocator
// khong sinh trung duoi concurrency - cung tinh than voi race test cho
// job lease va IP reservation o P0-01.
func TestHostnameRepository_ConcurrentNoDuplicate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewHostnameRepository(db)

	prefix := uniqueName(t, "race")
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM hostname_sequences WHERE prefix = $1`, prefix)
	})

	const numWorkers = 20
	var (
		mu      sync.Mutex
		seen    = make(map[string]bool, numWorkers)
		wg      sync.WaitGroup
		errored bool
	)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hostname, err := repo.Next(ctx, prefix)
			if err != nil {
				mu.Lock()
				errored = true
				mu.Unlock()
				t.Errorf("Next() error: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[hostname] {
				t.Errorf("duplicate hostname generated: %s", hostname)
			}
			seen[hostname] = true
		}()
	}
	wg.Wait()

	if errored {
		return
	}
	if len(seen) != numWorkers {
		t.Fatalf("got %d distinct hostnames, want %d", len(seen), numWorkers)
	}
}
