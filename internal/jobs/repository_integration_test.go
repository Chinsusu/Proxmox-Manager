package jobs

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// openTestDB skip test nếu không có DATABASE_URL — cho phép unit test
// khác chạy bình thường trên máy không có Postgres (vd máy scaffold
// Windows không có Docker), trong khi CI (có postgres service, xem
// .github/workflows/ci.yml) chạy integration test thật.
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

// seedInstance tạo đủ fixture (cluster/template/network segment/instance)
// để insert được một provisioning_jobs row hợp lệ, trả về instanceID.
// Đăng ký cleanup xoá theo đúng thứ tự ngược FK.
func seedInstance(ctx context.Context, t *testing.T, db *storage.DB) string {
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

	var instanceID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO vm_instances (logical_name, hostname, template_id)
		VALUES ($1, $2, $3)
		RETURNING id
	`, uniqueName(t, "logical"), uniqueName(t, "host"), templateID).Scan(&instanceID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM provisioning_jobs WHERE instance_id = $1`, instanceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_instances WHERE id = $1`, instanceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_templates WHERE id = $1`, templateID)
		_, _ = db.ExecContext(ctx, `DELETE FROM pve_clusters WHERE id = $1`, clusterID)
	})

	return instanceID
}

func uniqueName(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + t.Name() + "-" + time.Now().Format("150405.000000000")
}

func seedJob(ctx context.Context, t *testing.T, db *storage.DB, instanceID string) string {
	t.Helper()
	var jobID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO provisioning_jobs (instance_id, operation, state, checkpoint)
		VALUES ($1, 'PROVISION', 'QUEUED', 'REQUESTED')
		RETURNING id
	`, instanceID).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	return jobID
}

func TestRepository_Claim_HappyPath(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	instanceID := seedInstance(ctx, t, db)
	jobID := seedJob(ctx, t, db, instanceID)

	repo := NewRepository(db)
	job, err := repo.Claim(ctx, "worker-1", 10*time.Minute)
	if err != nil {
		t.Fatalf("Claim() error: %v", err)
	}
	if job.ID != jobID {
		t.Fatalf("claimed job id = %s, want %s (có job khác lẫn vào từ test song song?)", job.ID, jobID)
	}
	if job.State != domain.JobRunning {
		t.Fatalf("state = %s, want RUNNING", job.State)
	}
	if job.LeaseOwner == nil || *job.LeaseOwner != "worker-1" {
		t.Fatalf("lease_owner = %v, want worker-1", job.LeaseOwner)
	}
	if job.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", job.Attempt)
	}
}

func TestRepository_Claim_NoJobAvailable(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewRepository(db)

	// next_attempt_at trong tuong lai -> khong claimable.
	instanceID := seedInstance(ctx, t, db)
	var jobID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO provisioning_jobs (instance_id, operation, state, checkpoint, next_attempt_at)
		VALUES ($1, 'PROVISION', 'QUEUED', 'REQUESTED', now() + interval '1 hour')
		RETURNING id
	`, instanceID).Scan(&jobID); err != nil {
		t.Fatalf("seed future job: %v", err)
	}

	_, err := repo.Claim(ctx, "worker-1", 10*time.Minute)
	if !errors.Is(err, domain.ErrNotClaimable) {
		t.Fatalf("Claim() error = %v, want domain.ErrNotClaimable", err)
	}
}

// TestRepository_Claim_ConcurrentWorkers_NoDuplicateOwnership la test
// truc tiep cho acceptance criteria P0-01: "race tests show no duplicate
// job ownership" (docs/10 muc 10, Epic P0-01).
func TestRepository_Claim_ConcurrentWorkers_NoDuplicateOwnership(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewRepository(db)

	const numJobs = 8
	const numWorkers = 20

	instanceID := seedInstance(ctx, t, db)
	jobIDs := make(map[string]bool, numJobs)
	for i := 0; i < numJobs; i++ {
		id := seedJob(ctx, t, db, instanceID)
		jobIDs[id] = true
	}

	var (
		mu       sync.Mutex
		claimed  = make(map[string]string) // jobID -> workerID
		wg       sync.WaitGroup
		errCount int64
	)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		workerID := uniqueName(t, "worker") + "-" + strconv.Itoa(w)
		go func(workerID string) {
			defer wg.Done()
			job, err := repo.Claim(ctx, workerID, 10*time.Minute)
			if err != nil {
				if errors.Is(err, domain.ErrNotClaimable) {
					atomic.AddInt64(&errCount, 1)
					return
				}
				t.Errorf("worker %s Claim() unexpected error: %v", workerID, err)
				return
			}
			mu.Lock()
			if prevOwner, exists := claimed[job.ID]; exists {
				t.Errorf("job %s claimed by both %s and %s — duplicate ownership", job.ID, prevOwner, workerID)
			}
			claimed[job.ID] = workerID
			mu.Unlock()
		}(workerID)
	}
	wg.Wait()

	if len(claimed) != numJobs {
		t.Fatalf("claimed %d distinct jobs, want exactly %d (numWorkers=%d, ErrNotClaimable count=%d)",
			len(claimed), numJobs, numWorkers, atomic.LoadInt64(&errCount))
	}
	for jobID := range claimed {
		if !jobIDs[jobID] {
			t.Errorf("claimed unexpected job id %s not part of this test's fixtures", jobID)
		}
	}
}

func TestRepository_Heartbeat_WrongOwnerFails(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	instanceID := seedInstance(ctx, t, db)
	jobID := seedJob(ctx, t, db, instanceID)
	repo := NewRepository(db)

	// Lease duration rong rai (khong phai 90s sat nut) de tranh flaky
	// khi CI chay nhieu integration test song song tren cung postgres
	// service duoi -race, co the lam cham thoi gian giua Claim va
	// Heartbeat du de lease_expires_at cu bi vuot qua now().
	const leaseDuration = 10 * time.Minute

	if _, err := repo.Claim(ctx, "worker-owner", leaseDuration); err != nil {
		t.Fatalf("Claim() error: %v", err)
	}

	if err := repo.Heartbeat(ctx, jobID, "worker-owner", leaseDuration); err != nil {
		t.Fatalf("Heartbeat() by owner should succeed, got: %v", err)
	}
	if err := repo.Heartbeat(ctx, jobID, "worker-imposter", leaseDuration); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("Heartbeat() by non-owner error = %v, want domain.ErrLeaseLost", err)
	}
}

func TestRepository_CompleteAndFail(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewRepository(db)

	t.Run("complete releases lease", func(t *testing.T) {
		instanceID := seedInstance(ctx, t, db)
		jobID := seedJob(ctx, t, db, instanceID)
		if _, err := repo.Claim(ctx, "worker-1", 10*time.Minute); err != nil {
			t.Fatalf("Claim() error: %v", err)
		}
		if err := repo.Complete(ctx, jobID, "worker-1"); err != nil {
			t.Fatalf("Complete() error: %v", err)
		}
		got, err := repo.Get(ctx, jobID)
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got.State != domain.JobSucceeded || got.LeaseOwner != nil || got.FinishedAt == nil {
			t.Fatalf("unexpected job after Complete: %+v", got)
		}
	})

	t.Run("fail with retryAt transitions to RETRY_WAIT", func(t *testing.T) {
		instanceID := seedInstance(ctx, t, db)
		jobID := seedJob(ctx, t, db, instanceID)
		if _, err := repo.Claim(ctx, "worker-1", 10*time.Minute); err != nil {
			t.Fatalf("Claim() error: %v", err)
		}
		retryAt := time.Now().Add(30 * time.Second)
		if err := repo.Fail(ctx, jobID, "worker-1", "PVE_TASK_UNKNOWN", "task timeout", &retryAt); err != nil {
			t.Fatalf("Fail() error: %v", err)
		}
		got, err := repo.Get(ctx, jobID)
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got.State != domain.JobRetryWait || got.LeaseOwner != nil || got.ErrorCode == nil || *got.ErrorCode != "PVE_TASK_UNKNOWN" {
			t.Fatalf("unexpected job after Fail(retry): %+v", got)
		}
	})

	t.Run("fail without retryAt transitions to FAILED", func(t *testing.T) {
		instanceID := seedInstance(ctx, t, db)
		jobID := seedJob(ctx, t, db, instanceID)
		if _, err := repo.Claim(ctx, "worker-1", 10*time.Minute); err != nil {
			t.Fatalf("Claim() error: %v", err)
		}
		if err := repo.Fail(ctx, jobID, "worker-1", "PVE_VMID_CONFLICT", "vmid taken", nil); err != nil {
			t.Fatalf("Fail() error: %v", err)
		}
		got, err := repo.Get(ctx, jobID)
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got.State != domain.JobFailed || got.FinishedAt == nil {
			t.Fatalf("unexpected job after Fail(terminal): %+v", got)
		}
	})
}

func TestRepository_ReclaimExpiredLeases(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	instanceID := seedInstance(ctx, t, db)
	jobID := seedJob(ctx, t, db, instanceID)
	repo := NewRepository(db)

	if _, err := repo.Claim(ctx, "worker-crashed", 10*time.Minute); err != nil {
		t.Fatalf("Claim() error: %v", err)
	}
	// gia lap lease da het han (worker chet giua chung, khong Complete/Fail kip).
	if _, err := db.ExecContext(ctx, `
		UPDATE provisioning_jobs SET lease_expires_at = now() - interval '1 minute' WHERE id = $1
	`, jobID); err != nil {
		t.Fatalf("force-expire lease: %v", err)
	}

	n, err := repo.ReclaimExpiredLeases(ctx)
	if err != nil {
		t.Fatalf("ReclaimExpiredLeases() error: %v", err)
	}
	if n < 1 {
		t.Fatalf("reclaimed count = %d, want >= 1", n)
	}

	got, err := repo.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.State != domain.JobQueued || got.LeaseOwner != nil {
		t.Fatalf("job not reclaimed correctly: %+v", got)
	}

	// job phai claim lai duoc boi worker khac sau khi reclaim.
	reclaimed, err := repo.Claim(ctx, "worker-2", 10*time.Minute)
	if err != nil {
		t.Fatalf("Claim() after reclaim error: %v", err)
	}
	if reclaimed.ID != jobID {
		t.Fatalf("claimed job id = %s, want %s", reclaimed.ID, jobID)
	}
}
