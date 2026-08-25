package stateengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/pgw"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
	"github.com/Chinsusu/vm-factory/internal/storage"
	"github.com/Chinsusu/vm-factory/internal/workload"
)

// WorkloadAdapterFactory tạo một workload.Adapter gắn với *proxmox.Adapter
// — registry key theo tên adapter (domain.VMInstance.WorkloadAdapter),
// vì adapter cụ thể cần chạy cho instance nào chỉ biết được lúc
// Execute (mỗi instance có thể dùng adapter khác nhau).
type WorkloadAdapterFactory func(*proxmox.Adapter) workload.Adapter

// ApplyingWorkloadHandler thực hiện 4.10 APPLYING_WORKLOAD → READY
// (Phần V): Install rồi poll Health tới khi healthy hoặc hết timeout
// (Phần II mục 18: không dùng sleep cố định đơn — WRK-002 "health
// fails" cho adapter thời gian ổn định trước khi coi là fail thật).
// Install lỗi (bao gồm WRK-001 checksum mismatch) hoặc Health không đạt
// sau timeout đều chuyển QUARANTINED CÙNG evidence persist atomic qua
// PersistEvidence — không trả Go error, để nhánh THÀNH CÔNG của Execute
// (nơi Engine.Step chạy PersistEvidence) luôn ghi được bằng chứng, kể
// cả khi FAIL (cùng nguyên tắc đã áp dụng ở
// ValidatingIdentityHandler/ValidatingEgressHandler, P0-07).
type ApplyingWorkloadHandler struct {
	Proxmox *proxmox.Adapter
	PGW     pgw.Adapter
	IPAM    *ipam.Repository
	Runs    *storage.ValidationRunRepository

	// Adapters là registry tên adapter -> factory, đăng ký ở nơi wiring
	// (cmd/worker khi triển khai). DefaultAdapter dùng khi instance
	// không set WorkloadAdapter (nil/rỗng) — mặc định "noop" nếu để
	// trống, tương thích ngược các instance tạo trước khi P0-08 tồn tại.
	Adapters       map[string]WorkloadAdapterFactory
	DefaultAdapter string

	HealthCheckTimeout time.Duration
}

// Execute implement TransitionHandler.
func (h *ApplyingWorkloadHandler) Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error) {
	var cp networkBindingCheckpoint
	if err := json.Unmarshal(tctx.CheckpointData, &cp); err != nil || cp.VMID == 0 {
		return TransitionResult{}, fmt.Errorf("applying_workload: missing placement checkpoint")
	}
	ref := proxmox.VMRef{Node: cp.Node, VMID: cp.VMID}

	adapterName := h.DefaultAdapter
	if adapterName == "" {
		adapterName = "noop"
	}
	if tctx.Instance.WorkloadAdapter != nil && *tctx.Instance.WorkloadAdapter != "" {
		adapterName = *tctx.Instance.WorkloadAdapter
	}
	factory, ok := h.Adapters[adapterName]
	if !ok {
		return TransitionResult{}, fmt.Errorf("applying_workload: unknown workload adapter %q (chua dang ky trong Adapters)", adapterName)
	}
	adapter := factory(h.Proxmox)

	installErr := adapter.Install(ctx, ref, tctx.Instance.WorkloadSpec)

	var validateReport workload.ValidationReport
	var healthReport workload.HealthReport
	var healthErr error
	if installErr == nil {
		validateReport, _ = adapter.Validate(ctx, ref)
		healthReport, healthErr = h.pollHealth(ctx, adapter, ref)
	}

	pass := installErr == nil && validateReport.Valid && healthErr == nil && healthReport.Healthy

	evidence, err := json.Marshal(map[string]any{
		"adapter":       adapterName,
		"install_error": errString(installErr),
		"validate":      validateReport,
		"health":        healthReport,
		"health_error":  errString(healthErr),
	})
	if err != nil {
		return TransitionResult{}, fmt.Errorf("applying_workload: marshal evidence: %w", err)
	}

	result := domain.ValidationFail
	if pass {
		result = domain.ValidationPass
	}

	nextState := domain.InstanceReady
	if !pass {
		nextState = domain.InstanceQuarantined
		if cp.PGWMappingID != "" {
			_ = h.PGW.SuspendMapping(ctx, cp.PGWMappingID)
		}
		if cp.IPAllocationID != "" {
			_ = h.IPAM.MarkQuarantined(ctx, cp.IPAllocationID)
		}
	}

	data, _ := json.Marshal(cp)
	jobID := tctx.Job.ID
	instanceID := tctx.Instance.ID
	return TransitionResult{
		NextState:      nextState,
		CheckpointData: data,
		PersistEvidence: func(pctx context.Context, tx *sql.Tx) error {
			if _, err := h.Runs.Create(pctx, tx, domain.ValidationRun{
				InstanceID:     instanceID,
				JobID:          &jobID,
				Type:           "workload",
				Result:         result,
				RulesetVersion: "workload-" + adapterName + "-1.0",
				Evidence:       evidence,
				StartedAt:      time.Now(),
			}); err != nil {
				return fmt.Errorf("create validation run: %w", err)
			}
			return nil
		},
	}, nil
}

// pollHealth gọi Health lặp lại tới khi healthy hoặc hết timeout —
// service có thể cần vài giây để lên "active" sau systemctl enable
// --now, không coi lần check đầu tiên fail là kết luận cuối (WRK-002).
func (h *ApplyingWorkloadHandler) pollHealth(ctx context.Context, adapter workload.Adapter, ref proxmox.VMRef) (workload.HealthReport, error) {
	timeout := h.HealthCheckTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var report workload.HealthReport
	var err error
	for {
		report, err = adapter.Health(ctx, ref)
		if err == nil && report.Healthy {
			return report, nil
		}
		if time.Now().After(deadline) {
			return report, err
		}
		select {
		case <-ctx.Done():
			return workload.HealthReport{}, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
