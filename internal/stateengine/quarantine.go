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

// Quarantine thực hiện quarantine action theo Phần V mục 7: suspend
// egress mapping, tuỳ chọn dừng VM, giữ nguyên resource (KHÔNG xoá —
// khác Rollback), chặn tái sử dụng IP, ghi audit làm bằng chứng.
//
// Dùng khi phát hiện bất thường CẦN GIỮ LẠI để điều tra (duplicate
// identity, ownership không rõ, rollback tự nó thất bại...) — khác
// Rollback vốn dọn sạch resource để trả hệ thống về trạng thái sạch.
type Quarantine struct {
	Proxmox   *proxmox.Adapter
	PGW       PGWAdapter
	IPAM      *ipam.Repository
	Instances *instance.Repository
	JobsRepo  *jobs.Repository
	DB        *storage.DB
	Audit     *audit.Writer
}

// Execute quarantine một instance. stopVM=true nếu muốn dừng VM ngay
// (Phần V mục 7: "optionally stop VM") — mặc định false, chỉ cô lập
// network, giữ VM chạy để không làm gián đoạn nếu chưa chắc chắn có
// vấn đề thật.
func (q *Quarantine) Execute(ctx context.Context, inst *domain.VMInstance, job *domain.ProvisioningJob, reason string, stopVM bool) error {
	var cp fullCheckpoint
	_ = json.Unmarshal(job.CheckpointData, &cp)

	var warnings []string

	if cp.PGWMappingID != "" {
		if err := q.PGW.SuspendMapping(ctx, cp.PGWMappingID); err != nil {
			warnings = append(warnings, fmt.Sprintf("suspend pgw mapping: %v", err))
		}
	}

	if stopVM && cp.VMID != 0 {
		ref := proxmox.VMRef{Node: cp.Node, VMID: cp.VMID}
		if stopTask, err := q.Proxmox.Stop(ctx, ref); err != nil {
			warnings = append(warnings, fmt.Sprintf("stop vm: %v", err))
		} else if status, err := q.Proxmox.WaitForTask(ctx, stopTask, 1*time.Minute); err != nil || !status.Success() {
			warnings = append(warnings, fmt.Sprintf("wait stop vm task: err=%v status=%+v", err, status))
		}
	}

	if cp.IPAllocationID != "" {
		if err := q.IPAM.MarkQuarantined(ctx, cp.IPAllocationID); err != nil {
			warnings = append(warnings, fmt.Sprintf("mark ip quarantined: %v", err))
		}
	}

	err := storage.WithTx(ctx, q.DB, func(tx *sql.Tx) error {
		if err := q.Instances.UpdateState(ctx, tx, inst.ID, domain.InstanceQuarantined); err != nil {
			return fmt.Errorf("update instance state: %w", err)
		}
		if err := q.JobsRepo.UpdateCheckpoint(ctx, tx, job.ID, domain.InstanceQuarantined, job.CheckpointData); err != nil {
			return fmt.Errorf("update checkpoint: %w", err)
		}
		metadata, _ := json.Marshal(map[string]any{"reason": reason, "stop_vm": stopVM, "warnings": warnings})
		return q.Audit.Append(ctx, tx, domain.AuditEvent{
			ActorType:    "system",
			ActorID:      "state-engine-quarantine",
			Action:       "quarantine",
			ResourceType: "vm_instance",
			ResourceID:   inst.ID,
			Metadata:     metadata,
		})
	})
	if err != nil {
		return fmt.Errorf("stateengine: persist quarantine: %w", err)
	}
	return nil
}
