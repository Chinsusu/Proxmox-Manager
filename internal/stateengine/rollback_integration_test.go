package stateengine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/pgw"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// rollback_integration_test.go — Rollback.Execute truoc gio chua co
// test nao (checkpoint_test.go chi test JSON embedding cua checkpoint,
// khong test hanh vi Rollback/Quarantine that). Bao phu RBK-002 cua
// docs/appendices/acceptance_test_matrix.csv.
//
// RBK-001 (VM delete not found -> Treat as success after verify) KHONG
// co test o day: doc chi noi 9.2 yeu cau hanh vi nay, nhung
// Rollback.Execute HIEN TAI coi MOI loi Delete() (ke ca not-found) la
// failure -> QUARANTINED, khong phan biet duoc "not found" (should be
// idempotent success) voi loi that su, vi internal/proxmox/errors.go
// chua co ma loi rieng cho "VM not found" (classifyError chi co
// CodeAuthFailed/VMIDConflict/BridgeNotFound/StorageCapacity/VMLocked/
// TemplateInvalid/Unknown - khong co "not found"). Phan loai dung can
// biet CHINH XAC chuoi loi that Proxmox tra ve cho VM khong ton tai,
// chua verify duoc tren cluster that trong phien lam viec nay - viet
// test gia dinh chuoi loi se la doan mo, khong phai xac minh that. Ghi
// nhan la gap con lai, khong tu sua.

// fakeFailingPGW implement pgw.Adapter, chi DeleteMapping tra loi (gia
// lap RBK-002 "PGW delete fails") - moi method khac khong duoc goi
// trong kich ban nay nen panic neu goi nham, giup phat hien test sai
// gia dinh sam thay vi im lang.
type fakeFailingPGW struct {
	deleteMappingErr error
}

func (f *fakeFailingPGW) CreateClient(context.Context, pgw.ClientRequest) (pgw.ClientRef, error) {
	panic("not used in this test")
}
func (f *fakeFailingPGW) CreateMapping(context.Context, pgw.MappingRequest) (pgw.MappingRef, error) {
	panic("not used in this test")
}
func (f *fakeFailingPGW) ActivateMapping(context.Context, string) (pgw.Generation, error) {
	panic("not used in this test")
}
func (f *fakeFailingPGW) SuspendMapping(context.Context, string) error { return nil }
func (f *fakeFailingPGW) DeleteMapping(_ context.Context, _ string) error {
	return f.deleteMappingErr
}
func (f *fakeFailingPGW) EgressProof(context.Context, string) (pgw.EgressEvidence, error) {
	panic("not used in this test")
}

// TestChaos_RBK002_PGWDeleteFails_QuarantinesWithLeftoverResource: khi
// buoc PGW.DeleteMapping tu no that bai, Rollback KHONG duoc xoa sach
// nhu khong co gi xay ra - phai chuyen QUARANTINED (giu resource
// leftover de dieu tra) thay vi FAILED (Phan V muc 6), va IP allocation
// phai duoc danh dau QUARANTINED (khong RELEASED) vi khong the xac
// nhan PGW mapping da that su bi xoa.
func TestChaos_RBK002_PGWDeleteFails_QuarantinesWithLeftoverResource(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	clusterID := seedCluster(ctx, t, db)
	templateID := seedActiveTemplate(ctx, t, db, clusterID, 102)
	segmentID := seedSegmentWithFreeIPs(ctx, t, db, "vmbr1", 1)

	instancesRepo := instance.NewRepository(db)
	jobsRepo := jobs.NewRepository(db)
	ipamRepo := ipam.NewRepository(db)
	auditWriter := audit.NewWriter()

	inst, err := instancesRepo.Create(ctx, db, domain.VMInstance{
		LogicalName: uniqueName(t, "logical"), Hostname: uniqueName(t, "host"), TemplateID: templateID,
	})
	if err != nil {
		t.Fatalf("instances.Create() error: %v", err)
	}
	alloc, err := ipamRepo.ReserveNextFree(ctx, segmentID, inst.ID, 0)
	if err != nil {
		t.Fatalf("ReserveNextFree() error: %v", err)
	}

	cp := fullCheckpoint{IPAllocationID: alloc.ID, PGWMappingID: "pgw-mapping-rbk002"}
	cpData, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}
	job, err := jobsRepo.Create(ctx, db, inst.ID, domain.JobOpProvision, domain.InstanceNetworkBinding)
	if err != nil {
		t.Fatalf("jobs.Create() error: %v", err)
	}
	if err := jobsRepo.UpdateCheckpoint(ctx, db, job.ID, domain.InstanceNetworkBinding, cpData); err != nil {
		t.Fatalf("UpdateCheckpoint() error: %v", err)
	}
	job, err = jobsRepo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("jobs.Get() refresh error: %v", err)
	}

	rb := &Rollback{
		Proxmox:   proxmox.NewAdapter(proxmox.NewClient(proxmox.ClientConfig{BaseURL: "http://127.0.0.1:1"})), // khong duoc goi (VMID=0)
		PGW:       &fakeFailingPGW{deleteMappingErr: errors.New("pgw: connection refused (simulated)")},
		IPAM:      ipamRepo,
		Instances: instancesRepo,
		JobsRepo:  jobsRepo,
		DB:        db,
		Audit:     auditWriter,
	}

	finalState, err := rb.Execute(ctx, inst, job, "rbk-002 test")
	if err != nil {
		t.Fatalf("Rollback.Execute() error: %v", err)
	}
	if finalState != domain.InstanceQuarantined {
		t.Errorf("finalState = %s, want %s (PGW delete failed - khong duoc coi la don sach)", finalState, domain.InstanceQuarantined)
	}

	updatedInst, err := instancesRepo.Get(ctx, inst.ID)
	if err != nil {
		t.Fatalf("instances.Get() error: %v", err)
	}
	if updatedInst.State != domain.InstanceQuarantined {
		t.Errorf("instance.State = %s, want %s", updatedInst.State, domain.InstanceQuarantined)
	}

	updatedAlloc, err := ipamRepo.Get(ctx, alloc.ID)
	if err != nil {
		t.Fatalf("ipam.Get() error: %v", err)
	}
	if updatedAlloc.State != domain.AllocationQuarantined {
		t.Errorf("allocation.State = %s, want %s (khong duoc RELEASE khi khong chac PGW mapping da xoa that)", updatedAlloc.State, domain.AllocationQuarantined)
	}
}
