// Command vmf-worker là entrypoint của worker service — lease job từ
// provisioning_jobs, chạy qua stateengine.Engine tới READY/QUARANTINED,
// rollback khi thất bại hẳn (thuộc epic P0-05 State Engine, wiring thật
// vào một binary chạy được).
//
// CHỈ chạy được PROVISION/REBUILD — cả hai đi qua cùng transition chain
// REQUESTED→READY. DECOMMISSION/QUARANTINE/RETRY/RECONCILE chưa có
// transition handler chain nào đăng ký trong stateengine (gap đã biết,
// để lại follow-up riêng) — job các operation này sẽ FAILED ngay với
// error_code=UNSUPPORTED_OPERATION thay vì treo vô hạn.
//
// Dùng pgw.NoopAdapter (P0-04 PGW Adapter thật chưa triển khai) — mọi
// giá trị PGW trả về đều SIMULATED, NETWORK_BINDING/VALIDATING_EGRESS
// chạy được nhưng VALIDATING_EGRESS sẽ luôn FAIL thật (không rubber-stamp
// evidence giả) và chuyển instance QUARANTINED — đúng hành vi kỳ vọng
// cho tới khi có PGW thật.
//
// Proxmox: chỉ dùng proxmox.Clusters[0] — stateengine handlers hiện
// nhận đúng MỘT *proxmox.Adapter (gap đã biết, chưa route theo
// template.PVEClusterID khi có nhiều cluster).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/config"
	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/guest"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/observability"
	"github.com/Chinsusu/vm-factory/internal/pgw"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
	"github.com/Chinsusu/vm-factory/internal/stateengine"
	"github.com/Chinsusu/vm-factory/internal/storage"
	"github.com/Chinsusu/vm-factory/internal/template"
	"github.com/Chinsusu/vm-factory/internal/validation"
	"github.com/Chinsusu/vm-factory/internal/workload"
)

var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

const pollInterval = 2 * time.Second

func main() {
	configPath := flag.String("config", envOr("VMF_CONFIG", "configs/local.yaml"), "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Default().Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger := observability.New("vmf-worker", parseLevel(cfg.Observability.LogLevel))
	logger.Info("starting vmf-worker", "version", version, "commit", commit, "build_time", buildTime,
		"worker_concurrency", cfg.Provisioning.WorkerConcurrency)

	if err := validateWorkerConfig(cfg); err != nil {
		logger.Error("invalid worker config for provisioning pipeline", "error", err)
		os.Exit(1)
	}

	dsn, err := loadSecretFile(cfg.Database.DSNFile)
	if err != nil {
		logger.Error("failed to load database DSN", "error", err, "path", cfg.Database.DSNFile)
		os.Exit(1)
	}
	db, err := storage.Open(dsn, cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	cluster := cfg.Proxmox.Clusters[0]
	tokenSecret, err := loadSecretFile(cluster.TokenSecretFile)
	if err != nil {
		logger.Error("failed to load proxmox token secret", "error", err, "path", cluster.TokenSecretFile)
		os.Exit(1)
	}
	proxmoxAdapter := proxmox.NewAdapter(proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:            cluster.BaseURL,
		TokenID:            cluster.TokenID,
		Secret:             tokenSecret,
		InsecureSkipVerify: cluster.InsecureSkipVerify,
		RequestTimeout:     cluster.RequestTimeout.AsTimeDuration(),
	}))

	hmacKey, err := validation.LoadHMACKeyFromFile(cfg.Identity.HMACKeyFile)
	if err != nil {
		logger.Error("failed to load identity hmac key", "error", err, "path", cfg.Identity.HMACKeyFile)
		os.Exit(1)
	}

	engine, rollback := wireEngine(db, cfg, proxmoxAdapter, hmacKey)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	maintenanceInterval := cfg.Provisioning.JobLease.AsTimeDuration() / 2
	if maintenanceInterval <= 0 {
		maintenanceInterval = 30 * time.Second
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		runMaintenanceLoop(ctx, logger, jobs.NewRepository(db), ipam.NewReaper(db, audit.NewWriter()), maintenanceInterval)
	}()

	concurrency := cfg.Provisioning.WorkerConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	hostname, _ := os.Hostname()
	instancesRepo := instance.NewRepository(db)
	jobsRepo := jobs.NewRepository(db)
	for i := 0; i < concurrency; i++ {
		wk := &worker{
			id:        fmt.Sprintf("%s-%d-slot%d", hostname, os.Getpid(), i),
			engine:    engine,
			jobsRepo:  jobsRepo,
			instances: instancesRepo,
			rollback:  rollback,
			cfg:       cfg.Provisioning,
			logger:    logger,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			wk.run(ctx)
		}()
	}

	logger.Info("vmf-worker ready", "concurrency", concurrency)
	<-ctx.Done()
	logger.Info("shutdown signal received, waiting for in-flight jobs to reach a safe checkpoint")
	wg.Wait()
	logger.Info("vmf-worker stopped")
}

// wireEngine đăng ký toàn bộ transition handler cho REQUESTED→READY
// (Phần V) dùng adapter/repository thật, cộng Rollback dùng khi job
// thất bại hẳn (het max_attempts).
func wireEngine(db *storage.DB, cfg *config.Config, proxmoxAdapter *proxmox.Adapter, hmacKey []byte) (*stateengine.Engine, *stateengine.Rollback) {
	instancesRepo := instance.NewRepository(db)
	jobsRepo := jobs.NewRepository(db)
	templatesRepo := template.NewRepository(db)
	ipamRepo := ipam.NewRepository(db)
	segmentsRepo := ipam.NewSegmentRepository(db)
	identityRepo := storage.NewIdentityRepository(db)
	runsRepo := storage.NewValidationRunRepository(db)
	auditWriter := audit.NewWriter()

	pgwAdapter := pgw.NewNoopAdapter()
	factsCollector := guest.NewFactsCollector(proxmoxAdapter)
	digester := validation.NewIdentityDigester(hmacKey)
	d := cfg.Provisioning.Defaults

	engine := stateengine.NewEngine(db, instancesRepo, jobsRepo, auditWriter)
	engine.Register(domain.InstanceRequested, &stateengine.RequestedHandler{Templates: templatesRepo})
	engine.Register(domain.InstanceReserving, &stateengine.ReservingHandler{
		IPAM: ipamRepo, Proxmox: proxmoxAdapter, Templates: templatesRepo,
		SegmentID: d.NetworkSegmentID, ReservationTTL: d.ReservationTTL.AsTimeDuration(),
	})
	engine.Register(domain.InstanceCloning, &stateengine.CloningHandler{
		Proxmox: proxmoxAdapter, Templates: templatesRepo, Pool: d.Pool,
	})
	engine.Register(domain.InstanceConfiguring, &stateengine.ConfiguringHandler{
		Proxmox: proxmoxAdapter, Cores: d.Cores, MemoryMB: d.MemoryMB, Bridge: d.Bridge, IPConfig0: d.IPConfig0,
	})
	engine.Register(domain.InstanceNetworkBinding, &stateengine.NetworkBindingHandler{PGW: pgwAdapter, PolicyID: d.EgressPolicyID})
	engine.Register(domain.InstanceBooting, &stateengine.BootingHandler{Proxmox: proxmoxAdapter})
	engine.Register(domain.InstanceWaitingGuest, &stateengine.ValidatingIdentityHandler{
		PGW: pgwAdapter, Facts: factsCollector, Digester: digester,
		Identity: identityRepo, Runs: runsRepo, IPAM: ipamRepo, Segments: segmentsRepo,
		FactsTimeout:              cfg.Guest.QGATimeout.AsTimeDuration(),
		BlockRetiredDuplicate:     cfg.Identity.DuplicatePolicy.RetiredHistory == "block",
		RequireSingleNIC:          cfg.Network.RequireSingleNIC,
		RequireSingleDefaultRoute: cfg.Network.RequireSingleDefaultRoute,
	})
	engine.Register(domain.InstanceValidatingEgress, &stateengine.ValidatingEgressHandler{
		PGW: pgwAdapter, IPAM: ipamRepo, Runs: runsRepo, DenyIPv6: cfg.Network.IPv6Policy == "deny",
	})
	engine.Register(domain.InstanceApplyingWorkload, &stateengine.ApplyingWorkloadHandler{
		Proxmox: proxmoxAdapter, PGW: pgwAdapter, IPAM: ipamRepo, Runs: runsRepo,
		DefaultAdapter: "noop",
		Adapters: map[string]stateengine.WorkloadAdapterFactory{
			"noop":           func(_ *proxmox.Adapter) workload.Adapter { return workload.NewNoopAdapter() },
			"sample-systemd": func(a *proxmox.Adapter) workload.Adapter { return workload.NewSampleAdapter(a) },
		},
	})

	rollback := &stateengine.Rollback{
		Proxmox: proxmoxAdapter, PGW: pgwAdapter, IPAM: ipamRepo,
		Instances: instancesRepo, JobsRepo: jobsRepo, DB: db, Audit: auditWriter,
	}
	return engine, rollback
}

// worker lease job và drive qua Engine.Step tới trạng thái cuối, một
// slot xử lý MỘT job tại một thời điểm (Claim → chạy hết job đó → Claim
// tiếp), concurrency tổng do số slot (provisioning.worker_concurrency)
// quyết định.
type worker struct {
	id        string
	engine    *stateengine.Engine
	jobsRepo  *jobs.Repository
	instances *instance.Repository
	rollback  *stateengine.Rollback
	cfg       config.ProvisioningConfig
	logger    *slog.Logger
}

func (w *worker) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		job, err := w.jobsRepo.Claim(ctx, w.id, w.cfg.JobLease.AsTimeDuration())
		if err != nil {
			if !errors.Is(err, domain.ErrNotClaimable) {
				w.logger.Error("claim job failed", "worker_id", w.id, "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
			continue
		}
		w.processJob(ctx, job)
	}
}

func (w *worker) processJob(ctx context.Context, job *domain.ProvisioningJob) {
	log := observability.WithCorrelation(w.logger, "", job.ID, job.InstanceID)
	log.Info("job claimed", "worker_id", w.id, "operation", job.Operation, "attempt", job.Attempt)

	hbCtx, hbCancel := context.WithCancel(ctx)
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		w.heartbeatLoop(hbCtx, job.ID)
	}()
	defer func() {
		hbCancel()
		hbWG.Wait()
	}()

	if job.Operation != domain.JobOpProvision && job.Operation != domain.JobOpRebuild {
		msg := fmt.Sprintf("operation %s chua co transition handler chain trong stateengine (P0-05 gap) - chi PROVISION/REBUILD chay duoc", job.Operation)
		log.Error("unsupported job operation, failing without retry", "operation", job.Operation)
		if err := w.jobsRepo.Fail(ctx, job.ID, w.id, "UNSUPPORTED_OPERATION", msg, nil); err != nil {
			log.Error("mark unsupported-operation job failed", "error", err)
		}
		return
	}

	for {
		inst, err := w.instances.Get(ctx, job.InstanceID)
		if err != nil {
			log.Error("load instance failed", "error", err)
			w.failOrRetry(ctx, job, "LOAD_INSTANCE_FAILED", err.Error())
			return
		}
		if terminalState(inst.State) {
			if err := w.jobsRepo.Complete(ctx, job.ID, w.id); err != nil {
				log.Error("mark job complete failed", "final_state", inst.State, "error", err)
				return
			}
			log.Info("job completed", "final_state", inst.State)
			return
		}

		newState, err := w.engine.Step(ctx, job)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidTransition) {
				log.Error("no transition handler registered for state, failing without retry", "state", inst.State, "error", err)
				w.terminalFail(ctx, job, inst, "NO_HANDLER", err.Error())
				return
			}
			log.Warn("step failed", "state", inst.State, "attempt", job.Attempt, "error", err)
			w.failOrRetry(ctx, job, "STEP_FAILED", err.Error())
			return
		}
		log.Info("step advanced", "to", newState)

		refreshed, err := w.jobsRepo.Get(ctx, job.ID)
		if err != nil {
			log.Error("refresh job after step failed", "error", err)
			return
		}
		job = refreshed
	}
}

// failOrRetry đưa job về RETRY_WAIT với backoff nếu còn attempt, hoặc
// coi là thất bại hẳn (Fail terminal + Rollback) khi đã hết
// max_attempts. Backoff áp dụng ĐỒNG NHẤT cho mọi lỗi transient — chưa
// phân loại TRANSIENT/CONFLICT/CAPACITY/AUTH/VALIDATION/PERMANENT theo
// Phần V mục 5 (proxmox adapter chưa trả lỗi có kiểu để phân loại được,
// gap đã biết).
func (w *worker) failOrRetry(ctx context.Context, job *domain.ProvisioningJob, code, msg string) {
	if job.Attempt < job.MaxAttempts {
		retryAt := time.Now().Add(backoffDelay(job.Attempt))
		if err := w.jobsRepo.Fail(ctx, job.ID, w.id, code, msg, &retryAt); err != nil {
			w.logger.Error("mark job retry-wait failed", "job_id", job.ID, "error", err)
		}
		return
	}

	inst, err := w.instances.Get(ctx, job.InstanceID)
	if err != nil {
		w.logger.Error("load instance before terminal fail failed", "job_id", job.ID, "error", err)
		if failErr := w.jobsRepo.Fail(ctx, job.ID, w.id, code, msg, nil); failErr != nil {
			w.logger.Error("mark job terminal fail failed", "job_id", job.ID, "error", failErr)
		}
		return
	}
	w.terminalFail(ctx, job, inst, code, msg)
}

// terminalFail đánh dấu job FAILED (giải phóng lease) rồi chạy
// Rollback compensating action ngay bằng checkpoint_data đã có sẵn
// trong job (Fail không đụng tới checkpoint_data nên không cần đọc lại).
func (w *worker) terminalFail(ctx context.Context, job *domain.ProvisioningJob, inst *domain.VMInstance, code, msg string) {
	if err := w.jobsRepo.Fail(ctx, job.ID, w.id, code, msg, nil); err != nil {
		w.logger.Error("mark job terminal fail failed", "job_id", job.ID, "error", err)
		return
	}
	finalState, err := w.rollback.Execute(ctx, inst, job, msg)
	if err != nil {
		w.logger.Error("rollback failed", "job_id", job.ID, "instance_id", inst.ID, "error", err)
		return
	}
	w.logger.Info("rollback complete", "job_id", job.ID, "instance_id", inst.ID, "final_state", finalState)
}

func (w *worker) heartbeatLoop(ctx context.Context, jobID string) {
	interval := w.cfg.JobHeartbeat.AsTimeDuration()
	if interval <= 0 {
		interval = 20 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.jobsRepo.Heartbeat(ctx, jobID, w.id, w.cfg.JobLease.AsTimeDuration()); err != nil {
				if !errors.Is(err, domain.ErrLeaseLost) {
					w.logger.Warn("heartbeat failed", "job_id", jobID, "error", err)
				}
				return
			}
		}
	}
}

func terminalState(s domain.InstanceState) bool {
	return s == domain.InstanceReady || s == domain.InstanceQuarantined
}

// backoffDelay: exponential + jitter (Phần V mục 5), base 15s, trần 5
// phút để không giữ job RETRY_WAIT quá lâu trong lúc dev/lab.
func backoffDelay(attempt int) time.Duration {
	const base = 15 * time.Second
	const maxDelay = 5 * time.Minute
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(math.Pow(2, float64(attempt-1))) * base
	if d <= 0 || d > maxDelay {
		d = maxDelay
	}
	// jitter chỉ để giãn thời điểm retry giữa nhiều job, khong nhay cam
	// bao mat.
	jitter := time.Duration(rand.Int63n(int64(d)/4 + 1)) //nolint:gosec // G404: chi dung de giai retry timing, khong phai bao mat
	return d + jitter
}

func runMaintenanceLoop(ctx context.Context, logger *slog.Logger, jobsRepo *jobs.Repository, reaper *ipam.Reaper, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := jobsRepo.ReclaimExpiredLeases(ctx); err != nil {
				logger.Error("reclaim expired job leases failed", "error", err)
			} else if n > 0 {
				logger.Warn("reclaimed expired job leases", "count", n)
			}
			if n, err := reaper.ReleaseExpiredReservations(ctx); err != nil {
				logger.Error("release expired ip reservations failed", "error", err)
			} else if n > 0 {
				logger.Info("released expired ip reservations", "count", n)
			}
		}
	}
}

// validateWorkerConfig chặn khởi động sớm với thông báo rõ ràng thay vì
// panic/lỗi mơ hồ giữa chừng khi thiếu cấu hình transition handler cần.
func validateWorkerConfig(cfg *config.Config) error {
	if len(cfg.Proxmox.Clusters) == 0 {
		return errors.New("proxmox.clusters phai co it nhat 1 cluster")
	}
	if cfg.Proxmox.Clusters[0].TokenSecretFile == "" {
		return errors.New("proxmox.clusters[0].token_secret_file la bat buoc")
	}
	if cfg.Identity.HMACKeyFile == "" {
		return errors.New("identity.hmac_key_file la bat buoc")
	}
	if cfg.Provisioning.Defaults.NetworkSegmentID == "" {
		return errors.New("provisioning.defaults.network_segment_id la bat buoc (UUID cua mot network_segments da dang ky)")
	}
	return nil
}

func loadSecretFile(path string) (string, error) {
	// path đến từ config field do operator chỉ định lúc deploy, không
	// phải request input.
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path là config field do operator kiểm soát, không phải request input
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(raw))
	if v == "" {
		return "", fmt.Errorf("secret file %s is empty", path)
	}
	return v, nil
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
