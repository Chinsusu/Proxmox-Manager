package proxmoxmock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

func newTestAdapter(t *testing.T, mock *Server) *proxmox.Adapter {
	t.Helper()
	client := proxmox.NewClient(proxmox.ClientConfig{BaseURL: mock.URL, TokenID: "test@pve!mock", Secret: "x"})
	return proxmox.NewAdapter(client)
}

func TestMock_CloneThenPollCompletes(t *testing.T) {
	mock := NewServer()
	defer mock.Close()
	adapter := newTestAdapter(t, mock)
	ctx := context.Background()

	task, err := adapter.Clone(ctx, proxmox.CloneRequest{SourceNode: "pve01", SourceVMID: 102, TargetNode: "pve01", TargetVMID: 950, FullClone: true})
	if err != nil {
		t.Fatalf("Clone() error: %v", err)
	}
	status, err := adapter.WaitForTask(ctx, task, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitForTask() error: %v", err)
	}
	if !status.Success() {
		t.Fatalf("status = %+v, want success", status)
	}
	if mock.CloneCalls != 1 {
		t.Errorf("CloneCalls = %d, want 1", mock.CloneCalls)
	}

	vm, err := adapter.GetVM(ctx, proxmox.VMRef{Node: "pve01", VMID: 950})
	if err != nil {
		t.Fatalf("GetVM() error: %v", err)
	}
	if vm.Status != "stopped" {
		t.Errorf("newly cloned VM status = %s, want stopped", vm.Status)
	}
}

func TestMock_CloneVMIDConflict(t *testing.T) {
	mock := NewServer()
	defer mock.Close()
	mock.RegisterVM(950, "pve01", false)
	adapter := newTestAdapter(t, mock)
	ctx := context.Background()

	_, err := adapter.Clone(ctx, proxmox.CloneRequest{SourceNode: "pve01", SourceVMID: 102, TargetNode: "pve01", TargetVMID: 950, FullClone: true})
	if err == nil {
		t.Fatal("Clone() into an already-registered VMID succeeded, want VMID conflict error")
	}
	var pveErr *proxmox.Error
	if !errors.As(err, &pveErr) || pveErr.Code != proxmox.CodeVMIDConflict {
		t.Fatalf("Clone() error = %v, want classified as %s", err, proxmox.CodeVMIDConflict)
	}
}

func TestMock_InjectedConfigureError(t *testing.T) {
	mock := NewServer()
	defer mock.Close()
	mock.RegisterVM(950, "pve01", false)
	mock.ConfigureErr = &InjectedError{Status: 500, Body: "bridge 'vmbr9' does not exist"}
	adapter := newTestAdapter(t, mock)
	ctx := context.Background()

	_, err := adapter.Configure(ctx, proxmox.ConfigureRequest{VMRef: proxmox.VMRef{Node: "pve01", VMID: 950}, Net0: proxmox.NetConfig{Bridge: "vmbr9"}})
	if err == nil {
		t.Fatal("Configure() with injected error succeeded, want error")
	}
	var pveErr *proxmox.Error
	if !errors.As(err, &pveErr) || pveErr.Code != proxmox.CodeBridgeNotFound {
		t.Fatalf("Configure() error = %v, want classified as %s", err, proxmox.CodeBridgeNotFound)
	}

	// InjectedError chi ap dung MOT lan - goi lai phai thanh cong.
	if _, err := adapter.Configure(ctx, proxmox.ConfigureRequest{VMRef: proxmox.VMRef{Node: "pve01", VMID: 950}}); err != nil {
		t.Fatalf("Configure() retry after injected error cleared should succeed, got: %v", err)
	}
}

func TestMock_DeleteNotFound(t *testing.T) {
	mock := NewServer()
	defer mock.Close()
	adapter := newTestAdapter(t, mock)
	ctx := context.Background()

	_, err := adapter.Delete(ctx, proxmox.VMRef{Node: "pve01", VMID: 999}, true)
	if err == nil {
		t.Fatal("Delete() of a non-existent VM succeeded at the adapter layer, want an error (caller must treat not-found as idempotent success per docs/03 muc 9.2)")
	}
}

func TestMock_ResumeFromCompletedTask_NoDuplicateClone(t *testing.T) {
	mock := NewServer()
	defer mock.Close()
	mock.RegisterVM(950, "pve01", false)
	mock.RegisterCompletedTask("UPID:pve01:PRIOR:00000001::qmclone:mock:root@pam:", "pve01", "OK")
	adapter := newTestAdapter(t, mock)
	ctx := context.Background()

	// Mo phong worker resume: KHONG goi Clone() nua, chi poll task da
	// dang ky san - dung dung invariant PVE-003 "next worker attaches
	// existing VM", khong tao side effect trung lap.
	status, err := adapter.WaitForTask(ctx, proxmox.TaskRef{Node: "pve01", UPID: "UPID:pve01:PRIOR:00000001::qmclone:mock:root@pam:"}, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitForTask() on pre-registered task error: %v", err)
	}
	if !status.Success() {
		t.Fatalf("status = %+v, want success", status)
	}
	if mock.CloneCalls != 0 {
		t.Errorf("CloneCalls = %d, want 0 (resume phai khong goi lai Clone)", mock.CloneCalls)
	}
}
