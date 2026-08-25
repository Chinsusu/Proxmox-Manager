package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	db, err := Open(dsn, 10, 5)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ready(); err != nil {
		t.Fatalf("db not ready: %v", err)
	}
	return db
}

func uniqueKey(t *testing.T) string {
	t.Helper()
	return t.Name() + "-" + time.Now().Format("150405.000000000")
}

func TestIdempotencyRepository_StoreAndGet(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewIdempotencyRepository(db)

	key := uniqueKey(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM idempotency_keys WHERE scope = 'test' AND key = $1`, key)
	})

	rec := domain.IdempotencyRecord{
		Scope:       "test",
		Key:         key,
		RequestHash: "hash-a",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := repo.Store(ctx, db, rec); err != nil {
		t.Fatalf("Store() error: %v", err)
	}

	got, err := repo.Get(ctx, "test", key)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.RequestHash != "hash-a" {
		t.Fatalf("request_hash = %q, want hash-a", got.RequestHash)
	}
}

func TestIdempotencyRepository_SameHashIsNoop(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewIdempotencyRepository(db)

	key := uniqueKey(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM idempotency_keys WHERE scope = 'test' AND key = $1`, key)
	})

	rec := domain.IdempotencyRecord{Scope: "test", Key: key, RequestHash: "hash-a", ExpiresAt: time.Now().Add(time.Hour)}
	if err := repo.Store(ctx, db, rec); err != nil {
		t.Fatalf("first Store() error: %v", err)
	}
	// retry voi cung request hash phai la no-op thanh cong (idempotent retry).
	if err := repo.Store(ctx, db, rec); err != nil {
		t.Fatalf("second Store() with same hash should succeed, got: %v", err)
	}
}

func TestIdempotencyRepository_DifferentHashConflicts(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewIdempotencyRepository(db)

	key := uniqueKey(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM idempotency_keys WHERE scope = 'test' AND key = $1`, key)
	})

	first := domain.IdempotencyRecord{Scope: "test", Key: key, RequestHash: "hash-a", ExpiresAt: time.Now().Add(time.Hour)}
	if err := repo.Store(ctx, db, first); err != nil {
		t.Fatalf("first Store() error: %v", err)
	}

	second := domain.IdempotencyRecord{Scope: "test", Key: key, RequestHash: "hash-b", ExpiresAt: time.Now().Add(time.Hour)}
	if err := repo.Store(ctx, db, second); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("Store() with different hash error = %v, want domain.ErrIdempotencyConflict", err)
	}
}

func TestIdempotencyRepository_GetNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewIdempotencyRepository(db)

	_, err := repo.Get(ctx, "test", "does-not-exist-"+uniqueKey(t))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() error = %v, want domain.ErrNotFound", err)
	}
}
