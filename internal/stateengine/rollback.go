package stateengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// fullCheckpoint gom mọi field có thể có trong checkpoint_data (Phần
// II mục 6.2) qua các bước RESERVING..NETWORK_BINDING — dùng để đọc
// lại khi rollback/quarantine mà không cần biết instance đã dừng ở
// bước nào (field nào chưa từng ghi thì giữ giá trị zero, bỏ qua).
type fullCheckpoint struct {
	IPAllocationID string `json:"ip_allocation_id"`
	VMID           int    `json:"vmid"`
	Node           string `json:"node"`
	PGWClientID    string `json:"pgw_client_id"`
	PGWMappingID   string `json:"pgw_mapping_id"`
}

// Rollback thực hiện compensating action ngược thứ tự side effect
// (Phần V mục 6): dừng/xoá VM nếu có, suspend/xoá PGW mapping nếu có,
// giải phóng IP nếu có, rồi chuyển instance về FAILED — hoặc
// QUARANTINED nếu một bước rollback tự nó thất bại, giữ nguyên resource
// leftover thay vì xoá record để che giấu lỗi (Phần V mục 6: "Rollback
// cũng là state machine... Nếu rollback thất bại, instance chuyển
// QUARANTINED với resource inventory còn lại").
//
// Bước "stop/remove workload" (đầu tiên theo Phần V mục 6) CHƯA có vì
// Workload Adapter thuộc epic P0-08 chưa triển khai — gap đã biết.
type Rollback struct {
	Proxmox   *proxmox.Adapter
	PGW       PGWAdapter
	IPAM      *ipam.Repository
	Instances *instance.Repository
	JobsRepo  *jobs.Repository
	DB        *storage.DB
	Audit     *audit.Writer
}

// Execute chạy rollback cho một instance/job, trả về state cuối cùng
// (FAILED hoặc QUARANTINED) và error nếu bản thân việc PERSIST kết quả
// rollback thất bại (khác với lỗi trong TỪNG bước compensating action,
// vốn được gom vào failures và quyết định QUARANTINED thay vì propagate).
func (rb *Rollback) Execute(ctx context.Context, inst *domain.VMInstance, job *domain.ProvisioningJob, reason string) (domain.InstanceState, error) {
	var cp fullCheckpoint
	_ = json.Unmarshal(job.CheckpointData, &cp)

	var failures []string

	if cp.VMID != 0 {
		ref := proxmox.VMRef{Node: cp.Node, VMID: cp.VMID}
		if stopTask, err := rb.Proxmox.Stop(ctx, ref); err == nil {
			_, _ = rb.Proxmox.WaitForTask(ctx, stopTask, 30*time.Second)
		}
		delTask, err := rb.Proxmox.Delete(ctx, ref, true)
		if err != nil {
			failures = append(failures, fmt.Sprintf("delete vm: %v", err))
		} else if status, err := rb.Proxmox.WaitForTask(ctx, delTask, 1*time.Minute); err != nil || !status.Success() {
			failures = append(failures, fmt.Sprintf("wait delete vm task: err=%v status=%+v", err, status))
		}
	}

	if cp.PGWMappingID != "" {
		if err := rb.PGW.SuspendMapping(ctx, cp.PGWMappingID); err != nil {
			failures = append(failures, fmt.Sprintf("suspend pgw mapping: %v", err))
		}
		if err := rb.PGW.DeleteMapping(ctx, cp.PGWMappingID); err != nil {
			failures = append(failures, fmt.Sprintf("delete pgw mapping: %v", err))
		}
	}

	ipReleased := false
	if cp.IPAllocationID != "" {
		if err := rb.IPAM.Release(ctx, cp.IPAllocationID); err != nil {
			failures = append(failures, fmt.Sprintf("release ip: %v", err))
		} else {
			ipReleased = true
		}
	}

	finalState := domain.InstanceFailed
	if len(failures) > 0 {
		finalState = domain.InstanceQuarantined
		if cp.IPAllocationID != "" && !ipReleased {
			_ = rb.IPAM.MarkQuarantined(ctx, cp.IPAllocationID)
		}
	}

	err := storage.WithTx(ctx, rb.DB, func(tx *sql.Tx) error {
		if err := rb.Instances.UpdateState(ctx, tx, inst.ID, finalState); err != nil {
			return fmt.Errorf("update instance state: %w", err)
		}
		if err := rb.JobsRepo.UpdateCheckpoint(ctx, tx, job.ID, finalState, job.CheckpointData); err != nil {
			return fmt.Errorf("update checkpoint: %w", err)
		}
		metadata, _ := json.Marshal(map[string]any{"reason": reason, "failures": failures})
		return rb.Audit.Append(ctx, tx, domain.AuditEvent{
			ActorType:    "system",
			ActorID:      "state-engine-rollback",
			Action:       "rollback",
			ResourceType: "vm_instance",
			ResourceID:   inst.ID,
			Metadata:     metadata,
		})
	})
	if err != nil {
		return "", fmt.Errorf("stateengine: persist rollback: %w", err)
	}
	return finalState, nil
}
