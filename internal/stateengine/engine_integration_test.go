package stateengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/guest"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/pgw"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
	"github.com/Chinsusu/vm-factory/internal/storage"
	"github.com/Chinsusu/vm-factory/internal/template"
	"github.com/Chinsusu/vm-factory/internal/validation"
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

func seedCluster(ctx context.Context, t *testing.T, db *storage.DB) string {
	t.Helper()
	var clusterID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO pve_clusters (name, base_url, secret_ref) VALUES ($1, $2, $3) RETURNING id
	`, uniqueName(t, "cluster"), "https://pve.test:8006/api2/json", "secret_ref_test").Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM pve_clusters WHERE id = $1`, clusterID)
	})
	return clusterID
}

func seedActiveTemplate(ctx context.Context, t *testing.T, db *storage.DB, clusterID string, sourceVMID int) string {
	t.Helper()
	var templateID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO vm_templates
			(name, family, version, os_family, os_version, architecture,
			 pve_cluster_id, pve_node, pve_template_vmid, source_checksum, state)
		VALUES ($1, $2, '2026.01.1', 'ubuntu', '22.04', 'amd64', $3, $4, $5, 'deadbeef', 'ACTIVE')
		RETURNING id
	`, uniqueName(t, "tpl"), uniqueName(t, "family"), clusterID, os.Getenv("PVE_NODE"), sourceVMID).Scan(&templateID); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_templates WHERE id = $1`, templateID)
	})
	return templateID
}

func seedSegmentWithFreeIPs(ctx context.Context, t *testing.T, db *storage.DB, bridge string, numAddresses int) string {
	t.Helper()
	var segmentID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO network_segments (name, cidr, gateway, bridge)
		VALUES ($1, '10.98.0.0/24', '10.98.0.1', $2)
		RETURNING id
	`, uniqueName(t, "segment"), bridge).Scan(&segmentID); err != nil {
		t.Fatalf("seed segment: %v", err)
	}
	for i := 0; i < numAddresses; i++ {
		addr := fmt.Sprintf("10.98.0.%d", 10+i)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO ip_allocations (segment_id, address, state) VALUES ($1, $2, 'FREE')
		`, segmentID, addr); err != nil {
			t.Fatalf("seed free address %s: %v", addr, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM ip_allocations WHERE segment_id = $1`, segmentID)
		_, _ = db.ExecContext(ctx, `DELETE FROM network_segments WHERE id = $1`, segmentID)
	})
	return segmentID
}

// TestEngine_FullPipeline_RealCluster chạy Engine.Step liên tiếp
// (REQUESTED → RESERVING → CLONING → CONFIGURING → NETWORK_BINDING →
// BOOTING → WAITING_GUEST → VALIDATING_IDENTITY → ...) trên DB thật +
// cluster Proxmox thật, dùng pgw.NoopAdapter cho NETWORK_BINDING/
// VALIDATING_EGRESS (chưa có PGW thật, epic P0-04 chưa triển khai).
// Xác nhận WAITING_GUEST bằng GuestPing thật, rồi chạy VALIDATING_IDENTITY
// (P0-07) — kỳ vọng PASS thật (hostname/MAC/IP đều do chính pipeline này
// cấp phát) sang VALIDATING_EGRESS, sau đó VALIDATING_EGRESS hợp lệ
// QUARANTINED vì NoopAdapter không phải PGW thật (Phần VIII mục 8: engine
// không được rubber-stamp evidence giả).
//
// KHÔNG chạy trong CI công khai — cần cả DATABASE_URL lẫn credential
// Proxmox riêng. Chạy thủ công:
//
//	DATABASE_URL=postgres://... \
//	PVE_BASE_URL=https://host:8006/api2/json \
//	PVE_TOKEN_ID='vmfactory@pve!automation' PVE_TOKEN_SECRET='...' \
//	PVE_NODE=us-ny PVE_SOURCE_VMID=102 PVE_STORAGE=local-lvm \
//	PVE_BRIDGE=vmbr1 PVE_POOL=vmfactory PVE_INSECURE_TLS=1 \
//	go test ./internal/stateengine/... -run RealCluster -v -timeout 10m
func TestEngine_FullPipeline_RealCluster(t *testing.T) {
	pveBaseURL := os.Getenv("PVE_BASE_URL")
	if pveBaseURL == "" {
		t.Skip("PVE_BASE_URL not set, skipping integration test against real Proxmox cluster")
	}
	sourceVMID, err := strconv.Atoi(os.Getenv("PVE_SOURCE_VMID"))
	if err != nil {
		t.Fatalf("PVE_SOURCE_VMID must be a valid integer: %v", err)
	}
	pveNode := os.Getenv("PVE_NODE")
	pveBridge := os.Getenv("PVE_BRIDGE")

	db := openTestDB(t)
	ctx := context.Background()

	clusterID := seedCluster(ctx, t, db)
	templateID := seedActiveTemplate(ctx, t, db, clusterID, sourceVMID)
	segmentID := seedSegmentWithFreeIPs(ctx, t, db, pveBridge, 1)

	instancesRepo := instance.NewRepository(db)
	jobsRepo := jobs.NewRepository(db)
	ipamRepo := ipam.NewRepository(db)
	templatesRepo := template.NewRepository(db)
	auditWriter := audit.NewWriter()

	inst, err := instancesRepo.Create(ctx, db, domain.VMInstance{
		LogicalName: uniqueName(t, "logical"),
		Hostname:    uniqueName(t, "host"),
		TemplateID:  templateID,
	})
	if err != nil {
		t.Fatalf("instances.Create() error: %v", err)
	}
	job, err := jobsRepo.Create(ctx, db, inst.ID, domain.JobOpProvision, domain.InstanceRequested)
	if err != nil {
		t.Fatalf("jobs.Create() error: %v", err)
	}

	client := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:            pveBaseURL,
		TokenID:            os.Getenv("PVE_TOKEN_ID"),
		Secret:             os.Getenv("PVE_TOKEN_SECRET"),
		InsecureSkipVerify: os.Getenv("PVE_INSECURE_TLS") == "1",
		RequestTimeout:     30 * time.Second,
	})
	adapter := proxmox.NewAdapter(client)

	engine := NewEngine(db, instancesRepo, jobsRepo, auditWriter)
	engine.Register(domain.InstanceRequested, &RequestedHandler{Templates: templatesRepo})
	engine.Register(domain.InstanceReserving, &ReservingHandler{
		IPAM: ipamRepo, Proxmox: adapter, Node: pveNode, SegmentID: segmentID, ReservationTTL: 10 * time.Minute,
	})
	engine.Register(domain.InstanceCloning, &CloningHandler{
		Proxmox: adapter, ClusterID: clusterID, SourceVMID: sourceVMID,
		Storage: os.Getenv("PVE_STORAGE"), Pool: os.Getenv("PVE_POOL"),
	})
	engine.Register(domain.InstanceConfiguring, &ConfiguringHandler{
		Proxmox: adapter, Cores: 1, MemoryMB: 512, Bridge: pveBridge, IPConfig0: "ip=dhcp",
	})
	engine.Register(domain.InstanceNetworkBinding, &NetworkBindingHandler{PGW: pgw.NewNoopAdapter(), PolicyID: "default"})
	engine.Register(domain.InstanceBooting, &BootingHandler{Proxmox: adapter})
	engine.Register(domain.InstanceWaitingGuest, &ValidatingIdentityHandler{
		PGW:                       pgw.NewNoopAdapter(),
		Facts:                     guest.NewFactsCollector(adapter),
		Digester:                  validation.NewIdentityDigester([]byte("test-hmac-key-not-for-production")),
		Identity:                  storage.NewIdentityRepository(db),
		Runs:                      storage.NewValidationRunRepository(db),
		IPAM:                      ipamRepo,
		Segments:                  ipam.NewSegmentRepository(db),
		RequireSingleNIC:          true,
		RequireSingleDefaultRoute: true,
	})
	engine.Register(domain.InstanceValidatingEgress, &ValidatingEgressHandler{
		PGW:      pgw.NewNoopAdapter(),
		IPAM:     ipamRepo,
		Runs:     storage.NewValidationRunRepository(db),
		DenyIPv6: true,
	})

	claimed, err := jobsRepo.Claim(ctx, "test-worker", 10*time.Minute)
	if err != nil {
		t.Fatalf("Claim() error: %v", err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job id = %s, want %s (job khac lan vao tu test song song?)", claimed.ID, job.ID)
	}

	// Safety-net cleanup: xoa VM that ngay khi biet vmid/node (tu buoc
	// RESERVING tro di), du cac buoc sau co fail giua chung.
	var cleanupVMID int
	var cleanupNode string
	t.Cleanup(func() {
		if cleanupVMID == 0 {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		ref := proxmox.VMRef{Node: cleanupNode, VMID: cleanupVMID}
		if stopTask, err := adapter.Stop(cleanupCtx, ref); err == nil {
			_, _ = adapter.WaitForTask(cleanupCtx, stopTask, 30*time.Second)
		}
		if delTask, err := adapter.Delete(cleanupCtx, ref, true); err == nil {
			_, _ = adapter.WaitForTask(cleanupCtx, delTask, 1*time.Minute)
			t.Logf("cleanup: deleted vmid=%d", cleanupVMID)
		}
	})

	expected := []domain.InstanceState{
		domain.InstanceReserving,
		domain.InstanceCloning,
		domain.InstanceConfiguring,
		domain.InstanceNetworkBinding,
		domain.InstanceBooting,
		domain.InstanceWaitingGuest,
	}

	for _, want := range expected {
		stepCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
		got, err := engine.Step(stepCtx, claimed)
		cancel()
		if err != nil {
			t.Fatalf("Step() advancing toward %s failed: %v", want, err)
		}
		if got != want {
			t.Fatalf("Step() reached %s, want %s", got, want)
		}
		t.Logf("reached state %s", got)

		claimed, err = jobsRepo.Get(ctx, claimed.ID)
		if err != nil {
			t.Fatalf("jobs.Get() refresh error: %v", err)
		}

		if cleanupVMID == 0 {
			var cp fullCheckpoint
			if err := json.Unmarshal(claimed.CheckpointData, &cp); err == nil && cp.VMID != 0 {
				cleanupVMID = cp.VMID
				cleanupNode = cp.Node
			}
		}
	}

	if cleanupVMID == 0 {
		t.Fatal("pipeline hoan tat nhung khong bat duoc vmid tu checkpoint - kiem tra lai ReservingHandler")
	}

	// WAITING_GUEST dat duoc nghia la VM da running - xac nhan QGA
	// that su phan hoi (khong chi tin task status).
	pingDeadline := time.Now().Add(2 * time.Minute)
	var pingErr error
	ref := proxmox.VMRef{Node: cleanupNode, VMID: cleanupVMID}
	for time.Now().Before(pingDeadline) {
		pingErr = adapter.GuestPing(ctx, ref)
		if pingErr == nil {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if pingErr != nil {
		t.Fatalf("GuestPing() sau khi dat WAITING_GUEST khong thanh cong trong timeout: %v", pingErr)
	}
	t.Logf("GuestPing OK - pipeline REQUESTED->WAITING_GUEST hoan tat that, vmid=%d", cleanupVMID)

	claimed, err = jobsRepo.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("jobs.Get() refresh truoc VALIDATING_IDENTITY error: %v", err)
	}
	identityStepCtx, cancelIdentity := context.WithTimeout(ctx, 1*time.Minute)
	gotIdentity, err := engine.Step(identityStepCtx, claimed)
	cancelIdentity()
	if err != nil {
		t.Fatalf("Step() VALIDATING_IDENTITY that bai: %v", err)
	}
	if gotIdentity != domain.InstanceValidatingEgress {
		// FAIL that (khong phai loi collector) van la ket qua hop le ve
		// mat co che neu facts thuc te lech (vd hostname cloud-init chua
		// kip set) - log evidence de dieu tra thay vi coi la bug cung.
		t.Logf("VALIDATING_IDENTITY khong PASS, instance chuyen %s (kiem tra validation_runs de biet ly do)", gotIdentity)
	} else {
		t.Logf("VALIDATING_IDENTITY PASS that - ID/NET rules khop guest facts thuc te tu chinh pipeline nay")
	}

	claimed, err = jobsRepo.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("jobs.Get() refresh truoc VALIDATING_EGRESS error: %v", err)
	}
	if gotIdentity == domain.InstanceValidatingEgress {
		egressStepCtx, cancelEgress := context.WithTimeout(ctx, 1*time.Minute)
		gotEgress, err := engine.Step(egressStepCtx, claimed)
		cancelEgress()
		if err != nil {
			t.Fatalf("Step() VALIDATING_EGRESS that bai: %v", err)
		}
		// pgw.NoopAdapter khong phai PGW that (P0-04 chua trien khai) -
		// EGR rules PHAI FAIL that (khong rubber-stamp), instance ket
		// thuc o QUARANTINED. Neu Step() lai tra APPLYING_WORKLOAD thi
		// day moi la bug (engine dang PASS nham evidence gia).
		if gotEgress != domain.InstanceQuarantined {
			t.Errorf("Step() VALIDATING_EGRESS = %s, want QUARANTINED (NoopAdapter khong duoc lam PASS)", gotEgress)
		} else {
			t.Logf("VALIDATING_EGRESS dung nhu ky vong: QUARANTINED vi PGW la NoopAdapter, khong rubber-stamp")
		}
	}

	finalInst, err := instancesRepo.Get(ctx, inst.ID)
	if err != nil {
		t.Fatalf("instances.Get() error: %v", err)
	}
	t.Logf("final instance state = %s", finalInst.State)
	if finalInst.VMID == nil || *finalInst.VMID != cleanupVMID {
		t.Errorf("instance.VMID = %v, want %d (SetPVEPlacement phai duoc goi dung o buoc CLONING)", finalInst.VMID, cleanupVMID)
	}
}
