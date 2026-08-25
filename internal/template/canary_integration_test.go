package template

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// TestCanaryValidator_RealCluster chạy canary validator thật trên
// cluster Proxmox, dùng template nguồn do PVE_SOURCE_VMID chỉ định.
//
// KHÔNG chạy trong CI công khai — cần credential riêng, giống
// internal/proxmox/adapter_integration_test.go. Chạy thủ công:
//
//	PVE_BASE_URL=https://host:8006/api2/json \
//	PVE_TOKEN_ID='vmfactory@pve!automation' PVE_TOKEN_SECRET='...' \
//	PVE_NODE=us-ny PVE_SOURCE_VMID=102 PVE_STORAGE=local-lvm \
//	PVE_BRIDGE=vmbr1 PVE_POOL=vmfactory PVE_INSECURE_TLS=1 \
//	go test ./internal/template/... -run RealCluster -v -timeout 10m
//
// LƯU Ý: template nguồn 102 hiện tại (VM production có sẵn) CHƯA đạt
// chuẩn Golden Template (Phần IV) — test này dự kiến trả FAIL cho
// nhiều check (machine-id trùng template gốc, SSH host key cũ còn
// nguyên). Đó là kết quả ĐÚNG, chứng minh validator phát hiện được
// template không compliant thay vì false-positive PASS. Test chỉ
// assert rằng validator CHẠY được hết vòng đời và trả evidence đầy
// đủ, không assert Result phải PASS.
func TestCanaryValidator_RealCluster(t *testing.T) {
	baseURL := os.Getenv("PVE_BASE_URL")
	if baseURL == "" {
		t.Skip("PVE_BASE_URL not set, skipping integration test against real Proxmox cluster")
	}
	sourceVMID, err := strconv.Atoi(os.Getenv("PVE_SOURCE_VMID"))
	if err != nil {
		t.Fatalf("PVE_SOURCE_VMID must be a valid integer: %v", err)
	}

	client := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:            baseURL,
		TokenID:            os.Getenv("PVE_TOKEN_ID"),
		Secret:             os.Getenv("PVE_TOKEN_SECRET"),
		InsecureSkipVerify: os.Getenv("PVE_INSECURE_TLS") == "1",
		RequestTimeout:     30 * time.Second,
	})
	validator := NewCanaryValidator(proxmox.NewAdapter(client))

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	run, err := validator.Validate(ctx, CanaryOptions{
		Node:        os.Getenv("PVE_NODE"),
		SourceVMID:  sourceVMID,
		Storage:     os.Getenv("PVE_STORAGE"),
		Bridge:      os.Getenv("PVE_BRIDGE"),
		Pool:        os.Getenv("PVE_POOL"),
		BootTimeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	if run.Type != "template" {
		t.Errorf("run.Type = %q, want template", run.Type)
	}
	if run.FinishedAt == nil {
		t.Error("run.FinishedAt is nil, want set")
	}
	if run.Result != domain.ValidationPass && run.Result != domain.ValidationFail {
		t.Errorf("run.Result = %q, want PASS or FAIL (not UNKNOWN — validator phải luôn kết luận được)", run.Result)
	}

	var evidence struct {
		CanaryVMID int           `json:"canary_vmid"`
		Facts      CanaryFacts   `json:"facts"`
		Checks     []canaryCheck `json:"checks"`
	}
	if err := json.Unmarshal(run.Evidence, &evidence); err != nil {
		t.Fatalf("unmarshal evidence: %v", err)
	}
	if len(evidence.Checks) == 0 {
		t.Fatal("evidence.Checks is empty, want at least the structural checks")
	}
	t.Logf("canary vmid=%d result=%s facts=%+v", evidence.CanaryVMID, run.Result, evidence.Facts)
	for _, c := range evidence.Checks {
		t.Logf("  check %-30s %-4s expected=%q observed=%q", c.Name, c.Result, c.Expected, c.Observed)
	}
}
