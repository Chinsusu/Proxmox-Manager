package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
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

func uniqueKey(t *testing.T) string {
	t.Helper()
	return "test-key-" + t.Name() + "-" + time.Now().Format("150405.000000000")
}

func TestRunIdempotent_FirstCallRunsWork(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	idem := storage.NewIdempotencyRepository(db)
	key := uniqueKey(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM idempotency_keys WHERE scope = 'test' AND key = $1`, key)
	})

	var workCalls int
	status, body, replayed, err := RunIdempotent(ctx, db, idem, "test", key, []byte(`{"a":1}`),
		func(_ context.Context, _ *sql.Tx) (int, any, string, error) {
			workCalls++
			return http.StatusCreated, map[string]any{"id": "res-1"}, "00000000-0000-0000-0000-000000000001", nil
		})
	if err != nil {
		t.Fatalf("RunIdempotent() error: %v", err)
	}
	if replayed {
		t.Error("replayed = true, want false (lan dau tien)")
	}
	if workCalls != 1 {
		t.Errorf("workCalls = %d, want 1", workCalls)
	}
	if status != http.StatusCreated {
		t.Errorf("status = %d, want 201", status)
	}
	m, ok := body.(map[string]any)
	if !ok || m["id"] != "res-1" {
		t.Errorf("body = %+v", body)
	}
}

func TestRunIdempotent_SameKeyAndHashReplaysWithoutRerunningWork(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	idem := storage.NewIdempotencyRepository(db)
	key := uniqueKey(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM idempotency_keys WHERE scope = 'test' AND key = $1`, key)
	})

	var workCalls int
	work := func(_ context.Context, _ *sql.Tx) (int, any, string, error) {
		workCalls++
		return http.StatusAccepted, map[string]any{"job_id": "job-1"}, "00000000-0000-0000-0000-000000000002", nil
	}
	requestBody := []byte(`{"a":1}`)

	if _, _, replayed1, err := RunIdempotent(ctx, db, idem, "test", key, requestBody, work); err != nil || replayed1 {
		t.Fatalf("first call: replayed=%v err=%v, want replayed=false err=nil", replayed1, err)
	}

	status2, body2, replayed2, err := RunIdempotent(ctx, db, idem, "test", key, requestBody, work)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if !replayed2 {
		t.Error("replayed = false, want true (cung key+hash)")
	}
	if workCalls != 1 {
		t.Errorf("workCalls = %d, want 1 (khong duoc chay lai work)", workCalls)
	}
	if status2 != http.StatusAccepted {
		t.Errorf("status2 = %d, want 202 (tu response da luu)", status2)
	}
	m, ok := body2.(map[string]any)
	if !ok || m["job_id"] != "job-1" {
		t.Errorf("body2 = %+v", body2)
	}
}

func TestRunIdempotent_SameKeyDifferentHashConflicts(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	idem := storage.NewIdempotencyRepository(db)
	key := uniqueKey(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM idempotency_keys WHERE scope = 'test' AND key = $1`, key)
	})

	work := func(_ context.Context, _ *sql.Tx) (int, any, string, error) {
		return http.StatusOK, map[string]any{}, "", nil
	}
	if _, _, _, err := RunIdempotent(ctx, db, idem, "test", key, []byte(`{"a":1}`), work); err != nil {
		t.Fatalf("first call error: %v", err)
	}

	_, _, _, err := RunIdempotent(ctx, db, idem, "test", key, []byte(`{"a":2}`), work)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("second call (khac request body) error = %v, want domain.ErrIdempotencyConflict", err)
	}
}

func TestRunIdempotent_WorkErrorDoesNotPersistRecord(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	idem := storage.NewIdempotencyRepository(db)
	key := uniqueKey(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM idempotency_keys WHERE scope = 'test' AND key = $1`, key)
	})

	sentinelErr := errors.New("boom")
	_, _, _, err := RunIdempotent(ctx, db, idem, "test", key, []byte(`{}`),
		func(_ context.Context, _ *sql.Tx) (int, any, string, error) {
			return 0, nil, "", sentinelErr
		})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("error = %v, want sentinelErr", err)
	}

	if _, getErr := idem.Get(ctx, "test", key); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("idempotency record ton tai sau work loi (transaction phai rollback), Get() error = %v", getErr)
	}
}
