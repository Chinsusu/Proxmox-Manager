package stateengine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
	"github.com/Chinsusu/vm-factory/internal/storage"
	"github.com/Chinsusu/vm-factory/internal/template"
	"github.com/Chinsusu/vm-factory/testlab/proxmoxmock"
)

// chaos_integration_test.go — bao phu cac hang PVE-xxx cua
// docs/appendices/acceptance_test_matrix.csv KHONG can cluster Proxmox
// that (dung testlab/proxmoxmock), chi can DATABASE_URL nhu cac
// integration test khac trong package nay — CHAY DUOC trong CI cong
// khai (khac voi TestEngine_FullPipeline_RealCluster can PVE_BASE_URL,
// chi chay thu cong). Epic P0-11 (Test Lab & Chaos), muc "chaos scripts".
//
// PVE-002 (clone timeout nhung VM da ton tai -> reconcile by tag) KHONG
// co test o day - CloningHandler chua co reconciliation qua external
// tag (gap da biet, xem comment CloningHandler o handlers_provision.go).

func seedInstanceForChaos(ctx context.Context, t *testing.T, db *storage.DB, mock *proxmoxmock.Server) (*instance.Repository, *jobs.Repository, *template.Repository, *domain.VMInstance, *domain.ProvisioningJob, *proxmox.Adapter) {
	t.Helper()
	clusterID := seedCluster(ctx, t, db)
	templateID := seedActiveTemplate(ctx, t, db, clusterID, 102)

	instancesRepo := instance.NewRepository(db)
	jobsRepo := jobs.NewRepository(db)
	templatesRepo := template.NewRepository(db)

	inst, err := instancesRepo.Create(ctx, db, domain.VMInstance{
		LogicalName: uniqueName(t, "logical"),
		Hostname:    uniqueName(t, "host"),
		TemplateID:  templateID,
	})
	if err != nil {
		t.Fatalf("instances.Create() error: %v", err)
	}
	job, err := jobsRepo.Create(ctx, db, inst.ID, domain.JobOpProvision, domain.InstanceReserving)
	if err != nil {
		t.Fatalf("jobs.Create() error: %v", err)
	}

	client := proxmox.NewClient(proxmox.ClientConfig{BaseURL: mock.URL, TokenID: "test@pve!mock", Secret: "x", RequestTimeout: 10 * time.Second})
	adapter := proxmox.NewAdapter(client)
	return instancesRepo, jobsRepo, templatesRepo, inst, job, adapter
}

func mustCheckpointData(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}
	return data
}

// TestChaos_PVE001_VMIDConflict_FailsControlledNoDuplicate: reserve da
// chon mot VMID nhung VM do da ton tai that su tren Proxmox (race voi
// mot provisioning khac ngoai tam kiem soat cua vm-factory, hoac VMID
// bi tai su dung) - CloningHandler.Execute PHAI tra loi ro rang (khong
// tao VM thu hai, khong panic), de worker (cmd/worker) retry/rollback
// theo co che chung.
func TestChaos_PVE001_VMIDConflict_FailsControlledNoDuplicate(t *testing.T) {
	ctx := context.Background()
	mock := proxmoxmock.NewServer()
	defer mock.Close()
	mock.RegisterVM(950, "pve01", false) // VMID 950 da bi chiem that ngoai tam kiem soat

	db := openTestDB(t)
	_, _, templatesRepo, inst, job, adapter := seedInstanceForChaos(ctx, t, db, mock)

	h := &CloningHandler{Proxmox: adapter, Templates: templatesRepo, Pool: "vmfactory"}
	tctx := &TransitionContext{
		Instance: inst, Job: job,
		CheckpointData: mustCheckpointData(t, reservingCheckpoint{IPAllocationID: "alloc-1", VMID: 950, Node: "pve01"}),
		SaveCheckpoint: func(context.Context, json.RawMessage) error { return nil },
	}

	_, err := h.Execute(ctx, tctx)
	if err == nil {
		t.Fatal("CloningHandler.Execute() into a VMID already in use succeeded, want a controlled error")
	}
	var pveErr *proxmox.Error
	if !errors.As(err, &pveErr) || pveErr.Code != proxmox.CodeVMIDConflict {
		t.Fatalf("error = %v, want classified as %s", err, proxmox.CodeVMIDConflict)
	}
	if mock.CloneCalls != 1 {
		t.Errorf("CloneCalls = %d, want exactly 1 (no retry-inside-handler double-clone)", mock.CloneCalls)
	}
}

// TestChaos_PVE003_WorkerDiesAfterClone_ResumeAttachesExistingTask:
// checkpoint da co CloneTaskUPID (handler truoc do da Clone() thanh
// cong roi SaveCheckpoint TRUOC khi worker "chet" giua luc poll) - lan
// Execute tiep theo (worker moi/lan goi lai) PHAI khong goi lai Clone(),
// chi tiep tuc poll task da co.
func TestChaos_PVE003_WorkerDiesAfterClone_ResumeAttachesExistingTask(t *testing.T) {
	ctx := context.Background()
	mock := proxmoxmock.NewServer()
	defer mock.Close()
	mock.RegisterVM(950, "pve01", false)
	priorUPID := "UPID:pve01:PRIOR:00000001::qmclone:mock:root@pam:"
	mock.RegisterCompletedTask(priorUPID, "pve01", "OK")

	db := openTestDB(t)
	_, _, templatesRepo, inst, job, adapter := seedInstanceForChaos(ctx, t, db, mock)

	h := &CloningHandler{Proxmox: adapter, Templates: templatesRepo, Pool: "vmfactory"}
	cp := cloningCheckpoint{
		reservingCheckpoint: reservingCheckpoint{IPAllocationID: "alloc-1", VMID: 950, Node: "pve01"},
		CloneTaskUPID:       priorUPID,
	}
	tctx := &TransitionContext{
		Instance: inst, Job: job,
		CheckpointData: mustCheckpointData(t, cp),
		SaveCheckpoint: func(context.Context, json.RawMessage) error {
			t.Fatal("SaveCheckpoint called during resume - CloneTaskUPID da co san, khong duoc goi lai Clone()/save moi")
			return nil
		},
	}

	result, err := h.Execute(ctx, tctx)
	if err != nil {
		t.Fatalf("Execute() on resume error: %v", err)
	}
	if result.NextState != domain.InstanceConfiguring {
		t.Errorf("NextState = %s, want %s", result.NextState, domain.InstanceConfiguring)
	}
	if mock.CloneCalls != 0 {
		t.Errorf("CloneCalls = %d, want 0 (resume phai khong tao clone moi - PVE-003)", mock.CloneCalls)
	}
	if result.PVEPlacement == nil || result.PVEPlacement.VMID != 950 {
		t.Errorf("PVEPlacement = %+v, want VMID 950", result.PVEPlacement)
	}
}

// TestChaos_PVE004_BridgeMissing_FailsControlled.
func TestChaos_PVE004_BridgeMissing_FailsControlled(t *testing.T) {
	ctx := context.Background()
	mock := proxmoxmock.NewServer()
	defer mock.Close()
	mock.RegisterVM(950, "pve01", false)
	mock.ConfigureErr = &proxmoxmock.InjectedError{Status: 500, Body: "bridge 'vmbr9' does not exist"}

	db := openTestDB(t)
	_, _, _, inst, job, adapter := seedInstanceForChaos(ctx, t, db, mock)

	h := &ConfiguringHandler{Proxmox: adapter, Bridge: "vmbr9", Cores: 1, MemoryMB: 512}
	tctx := &TransitionContext{
		Instance: inst, Job: job,
		CheckpointData: mustCheckpointData(t, cloningCheckpoint{reservingCheckpoint: reservingCheckpoint{VMID: 950, Node: "pve01"}}),
		SaveCheckpoint: func(context.Context, json.RawMessage) error { return nil },
	}

	_, err := h.Execute(ctx, tctx)
	if err == nil {
		t.Fatal("ConfiguringHandler.Execute() with a missing bridge succeeded, want a controlled error")
	}
	var pveErr *proxmox.Error
	if !errors.As(err, &pveErr) || pveErr.Code != proxmox.CodeBridgeNotFound {
		t.Fatalf("error = %v, want classified as %s", err, proxmox.CodeBridgeNotFound)
	}
}

// TestChaos_PVE005_StorageFull_FailsControlled.
func TestChaos_PVE005_StorageFull_FailsControlled(t *testing.T) {
	ctx := context.Background()
	mock := proxmoxmock.NewServer()
	defer mock.Close()
	mock.CloneErr = &proxmoxmock.InjectedError{Status: 500, Body: "no space left on device"}

	db := openTestDB(t)
	_, _, templatesRepo, inst, job, adapter := seedInstanceForChaos(ctx, t, db, mock)

	h := &CloningHandler{Proxmox: adapter, Templates: templatesRepo, Pool: "vmfactory"}
	tctx := &TransitionContext{
		Instance: inst, Job: job,
		CheckpointData: mustCheckpointData(t, reservingCheckpoint{IPAllocationID: "alloc-1", VMID: 951, Node: "pve01"}),
		SaveCheckpoint: func(context.Context, json.RawMessage) error { return nil },
	}

	_, err := h.Execute(ctx, tctx)
	if err == nil {
		t.Fatal("CloningHandler.Execute() with storage full succeeded, want a controlled error")
	}
	var pveErr *proxmox.Error
	if !errors.As(err, &pveErr) || pveErr.Code != proxmox.CodeStorageCapacity {
		t.Fatalf("error = %v, want classified as %s", err, proxmox.CodeStorageCapacity)
	}
}

// TestChaos_PVE006_VMAlreadyRunning_NoDuplicateStart: BootingHandler
// phai doc truoc khi ghi (read-before-write, Phan V muc 4.6) - neu VM
// da running thi KHONG duoc goi lai Start().
func TestChaos_PVE006_VMAlreadyRunning_NoDuplicateStart(t *testing.T) {
	ctx := context.Background()
	mock := proxmoxmock.NewServer()
	defer mock.Close()
	mock.RegisterVM(950, "pve01", true) // VM da running tu truoc

	db := openTestDB(t)
	_, _, _, inst, job, adapter := seedInstanceForChaos(ctx, t, db, mock)

	h := &BootingHandler{Proxmox: adapter}
	tctx := &TransitionContext{
		Instance: inst, Job: job,
		CheckpointData: mustCheckpointData(t, networkBindingCheckpoint{configuringCheckpoint: configuringCheckpoint{cloningCheckpoint: cloningCheckpoint{reservingCheckpoint: reservingCheckpoint{VMID: 950, Node: "pve01"}}}}),
		SaveCheckpoint: func(context.Context, json.RawMessage) error { return nil },
	}

	result, err := h.Execute(ctx, tctx)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if result.NextState != domain.InstanceWaitingGuest {
		t.Errorf("NextState = %s, want %s", result.NextState, domain.InstanceWaitingGuest)
	}
	if mock.StartCalls != 0 {
		t.Errorf("StartCalls = %d, want 0 (VM da running - PVE-006 cam start trung lap)", mock.StartCalls)
	}
}
