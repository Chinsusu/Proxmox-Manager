package proxmox

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// integrationConfig đọc cấu hình cluster thật từ env var — test skip
// nếu PVE_BASE_URL rỗng, cùng pattern với DATABASE_URL ở internal/jobs
// và internal/ipam. Không có credential nào được hard-code trong repo.
type integrationConfig struct {
	baseURL     string
	tokenID     string
	secret      string
	node        string
	sourceVMID  int
	storage     string
	bridge      string
	pool        string
	insecureTLS bool
}

func loadIntegrationConfig(t *testing.T) integrationConfig {
	t.Helper()
	baseURL := os.Getenv("PVE_BASE_URL")
	if baseURL == "" {
		t.Skip("PVE_BASE_URL not set, skipping integration test against real Proxmox cluster")
	}

	sourceVMID, err := strconv.Atoi(os.Getenv("PVE_SOURCE_VMID"))
	if err != nil {
		t.Fatalf("PVE_SOURCE_VMID must be a valid integer: %v", err)
	}

	return integrationConfig{
		baseURL:     baseURL,
		tokenID:     os.Getenv("PVE_TOKEN_ID"),
		secret:      os.Getenv("PVE_TOKEN_SECRET"),
		node:        os.Getenv("PVE_NODE"),
		sourceVMID:  sourceVMID,
		storage:     os.Getenv("PVE_STORAGE"),
		bridge:      os.Getenv("PVE_BRIDGE"),
		pool:        os.Getenv("PVE_POOL"),
		insecureTLS: os.Getenv("PVE_INSECURE_TLS") == "1",
	}
}

// TestAdapter_FullLifecycle_RealCluster clone -> configure -> start ->
// guest ping -> verify -> stop -> delete trên cluster Proxmox thật.
// Dùng template nguồn chỉ để clone (không sửa), VMID đích lấy từ
// AllocateNextVMID (không đụng VMID đang dùng bởi VM khác), tag mô tả
// rõ "vm-factory-adapter-test" để phân biệt với VM production.
//
// KHÔNG chạy trong CI công khai — cluster thật cần network riêng và
// credential không thể đưa vào GitHub Actions secret của repo public
// mà không có quyết định riêng của chủ hạ tầng. Chạy thủ công:
//
//	PVE_BASE_URL=https://host:8006/api2/json \
//	PVE_TOKEN_ID='vmfactory@pve!automation' \
//	PVE_TOKEN_SECRET='...' \
//	PVE_NODE=us-ny PVE_SOURCE_VMID=102 PVE_STORAGE=local-lvm \
//	PVE_BRIDGE=vmbr1 PVE_POOL=vmfactory PVE_INSECURE_TLS=1 \
//	go test ./internal/proxmox/... -run RealCluster -v -timeout 10m
func TestAdapter_FullLifecycle_RealCluster(t *testing.T) {
	cfg := loadIntegrationConfig(t)
	ctx := context.Background()

	client := NewClient(ClientConfig{
		BaseURL:            cfg.baseURL,
		TokenID:            cfg.tokenID,
		Secret:             cfg.secret,
		InsecureSkipVerify: cfg.insecureTLS,
		RequestTimeout:     30 * time.Second,
	})
	adapter := NewAdapter(client)

	targetVMID, err := adapter.AllocateNextVMID(ctx)
	if err != nil {
		t.Fatalf("AllocateNextVMID() error: %v", err)
	}
	t.Logf("allocated target vmid=%d", targetVMID)
	ref := VMRef{Node: cfg.node, VMID: targetVMID}

	// Cleanup an toan: du buoc sau co fail giua chung, van co gang xoa
	// VM test de khong de sot tren cluster that. Bo qua loi vi VM co
	// the chua ton tai (fail truoc Clone) hoac da bi xoa boi test.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if stopTask, err := adapter.Stop(cleanupCtx, ref); err == nil {
			_, _ = adapter.WaitForTask(cleanupCtx, stopTask, 30*time.Second)
		}
		if delTask, err := adapter.Delete(cleanupCtx, ref, true); err == nil {
			_, _ = adapter.WaitForTask(cleanupCtx, delTask, 30*time.Second)
			t.Logf("cleanup: deleted vmid=%d", targetVMID)
		}
	})

	cloneTask, err := adapter.Clone(ctx, CloneRequest{
		SourceNode:  cfg.node,
		SourceVMID:  cfg.sourceVMID,
		TargetNode:  cfg.node,
		TargetVMID:  targetVMID,
		Name:        fmt.Sprintf("vmf-adapter-test-%d", targetVMID),
		Storage:     cfg.storage,
		Pool:        cfg.pool,
		FullClone:   true,
		Description: fmt.Sprintf("vmf.test=1 vmf.purpose=P0-02-adapter-verification vmf.vmid=%d", targetVMID),
	})
	if err != nil {
		t.Fatalf("Clone() error: %v", err)
	}
	status, err := adapter.WaitForTask(ctx, cloneTask, 3*time.Minute)
	if err != nil {
		t.Fatalf("WaitForTask(clone) error: %v", err)
	}
	if !status.Success() {
		t.Fatalf("clone task did not succeed: %+v", status)
	}
	t.Logf("clone OK: vmid=%d", targetVMID)

	configTask, err := adapter.Configure(ctx, ConfigureRequest{
		VMRef:     ref,
		Cores:     1,
		Sockets:   1,
		MemoryMB:  512,
		Agent:     true,
		OnBoot:    false,
		Net0:      NetConfig{Bridge: cfg.bridge, Firewall: true},
		IPConfig0: "ip=dhcp",
	})
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}
	status, err = adapter.WaitForTask(ctx, configTask, 1*time.Minute)
	if err != nil {
		t.Fatalf("WaitForTask(configure) error: %v", err)
	}
	if !status.Success() {
		t.Fatalf("configure task did not succeed: %+v", status)
	}
	t.Logf("configure OK: bridge=%s", cfg.bridge)

	startTask, err := adapter.Start(ctx, ref)
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	status, err = adapter.WaitForTask(ctx, startTask, 1*time.Minute)
	if err != nil {
		t.Fatalf("WaitForTask(start) error: %v", err)
	}
	if !status.Success() {
		t.Fatalf("start task did not succeed: %+v", status)
	}
	t.Logf("start OK")

	observed, err := adapter.GetVM(ctx, ref)
	if err != nil {
		t.Fatalf("GetVM() error: %v", err)
	}
	if !observed.IsRunning() {
		t.Fatalf("GetVM() status = %q, want running", observed.Status)
	}
	t.Logf("GetVM OK: status=%s locked=%q", observed.Status, observed.Locked)

	// QGA can vai chuc giay sau boot de khoi dong trong guest - poll
	// thay vi sleep co dinh (guardrail Phan II muc 18).
	pingDeadline := time.Now().Add(2 * time.Minute)
	var pingErr error
	for time.Now().Before(pingDeadline) {
		pingErr = adapter.GuestPing(ctx, ref)
		if pingErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled while waiting for QGA: %v", ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	if pingErr != nil {
		t.Fatalf("GuestPing() did not succeed within timeout, last error: %v", pingErr)
	}
	t.Logf("GuestPing OK - QGA responsive")

	stopTask, err := adapter.Stop(ctx, ref)
	if err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	status, err = adapter.WaitForTask(ctx, stopTask, 1*time.Minute)
	if err != nil {
		t.Fatalf("WaitForTask(stop) error: %v", err)
	}
	if !status.Success() {
		t.Fatalf("stop task did not succeed: %+v", status)
	}
	t.Logf("stop OK")

	deleteTask, err := adapter.Delete(ctx, ref, true)
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	status, err = adapter.WaitForTask(ctx, deleteTask, 1*time.Minute)
	if err != nil {
		t.Fatalf("WaitForTask(delete) error: %v", err)
	}
	if !status.Success() {
		t.Fatalf("delete task did not succeed: %+v", status)
	}
	t.Logf("delete OK: vmid=%d fully cleaned up", targetVMID)
}
