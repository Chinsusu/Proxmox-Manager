package stateengine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// cloningCheckpoint kế thừa reservingCheckpoint (Node/VMID) qua embed
// JSON phẳng, thêm CloneTaskUPID để idempotent retry.
type cloningCheckpoint struct {
	reservingCheckpoint
	CloneTaskUPID string `json:"clone_task_upid,omitempty"`
}

// CloningHandler thực hiện 4.3 CLONING → CONFIGURING (Phần V).
// Idempotent: nếu CloneTaskUPID đã có trong checkpoint (retry sau
// worker crash), không gọi Clone() lại — chỉ tiếp tục poll task đã có.
//
// Giản lược so với Phần III mục 12 (reconciliation đầy đủ qua tìm VM
// bằng external tag khi cả checkpoint lẫn worker đều mất dấu) — gap
// đã biết, để lại cho lần củng cố sau (cần discovery API riêng).
type CloningHandler struct {
	Proxmox      *proxmox.Adapter
	ClusterID    string
	SourceVMID   int
	Storage      string
	Pool         string
	CloneTimeout time.Duration
}

// Execute implement TransitionHandler.
func (h *CloningHandler) Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error) {
	var cp cloningCheckpoint
	if err := json.Unmarshal(tctx.CheckpointData, &cp); err != nil || cp.VMID == 0 {
		return TransitionResult{}, fmt.Errorf("cloning: missing reservation checkpoint (can quarantine, khong the tu phuc hoi)")
	}
	ref := proxmox.VMRef{Node: cp.Node, VMID: cp.VMID}

	if cp.CloneTaskUPID == "" {
		task, err := h.Proxmox.Clone(ctx, proxmox.CloneRequest{
			SourceNode:  cp.Node,
			SourceVMID:  h.SourceVMID,
			TargetNode:  cp.Node,
			TargetVMID:  cp.VMID,
			Name:        tctx.Instance.Hostname,
			Storage:     h.Storage,
			Pool:        h.Pool,
			FullClone:   true,
			Description: fmt.Sprintf("vmf.instance_id=%s vmf.job_id=%s", tctx.Instance.ID, tctx.Job.ID),
		})
		if err != nil {
			return TransitionResult{}, fmt.Errorf("cloning: clone: %w", err)
		}
		cp.CloneTaskUPID = task.UPID
		// Persist NGAY - Phan II muc 7 buoc 3, truoc khi poll (buoc 4).
		data, _ := json.Marshal(cp)
		if err := tctx.SaveCheckpoint(ctx, data); err != nil {
			return TransitionResult{}, fmt.Errorf("cloning: save checkpoint after clone: %w", err)
		}
	}

	timeout := h.CloneTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	status, err := h.Proxmox.WaitForTask(ctx, proxmox.TaskRef{Node: cp.Node, UPID: cp.CloneTaskUPID}, timeout)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("cloning: wait clone task: %w", err)
	}
	if !status.Success() {
		return TransitionResult{}, fmt.Errorf("cloning: clone task failed: %+v", status)
	}

	// Phan III muc 5.3: terminal success chi duoc chap nhan khi VM
	// object thuc su ton tai, khong chi dua vao task status.
	if _, err := h.Proxmox.GetVM(ctx, ref); err != nil {
		return TransitionResult{}, fmt.Errorf("cloning: verify vm exists after clone: %w", err)
	}

	data, _ := json.Marshal(cp)
	return TransitionResult{
		NextState:      domain.InstanceConfiguring,
		CheckpointData: data,
		PVEPlacement:   &PVEPlacement{ClusterID: h.ClusterID, Node: cp.Node, VMID: cp.VMID},
	}, nil
}

// configuringCheckpoint ke thua cloningCheckpoint.
type configuringCheckpoint struct {
	cloningCheckpoint
	ConfigTaskUPID string `json:"config_task_upid,omitempty"`
}

// ConfiguringHandler thực hiện 4.4 CONFIGURING → NETWORK_BINDING (Phần
// V). Config desired hiện dùng giá trị tĩnh từ struct field (P0-05 v1)
// — chưa đọc instance.DesiredConfig JSONB / tính config hash canonicalized
// theo Phần III mục 6.1, gap đã biết, để lại cho lần củng cố sau.
type ConfiguringHandler struct {
	Proxmox          *proxmox.Adapter
	Cores            int
	MemoryMB         int
	Bridge           string
	IPConfig0        string
	ConfigureTimeout time.Duration
}

// Execute implement TransitionHandler.
func (h *ConfiguringHandler) Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error) {
	var cp configuringCheckpoint
	if err := json.Unmarshal(tctx.CheckpointData, &cp); err != nil || cp.VMID == 0 {
		return TransitionResult{}, fmt.Errorf("configuring: missing placement checkpoint")
	}
	ref := proxmox.VMRef{Node: cp.Node, VMID: cp.VMID}

	if cp.ConfigTaskUPID == "" {
		task, err := h.Proxmox.Configure(ctx, proxmox.ConfigureRequest{
			VMRef:     ref,
			Cores:     h.Cores,
			Sockets:   1,
			MemoryMB:  h.MemoryMB,
			Agent:     true,
			OnBoot:    false,
			Net0:      proxmox.NetConfig{Bridge: h.Bridge, Firewall: true},
			IPConfig0: h.IPConfig0,
		})
		if err != nil {
			return TransitionResult{}, fmt.Errorf("configuring: configure: %w", err)
		}
		cp.ConfigTaskUPID = task.UPID
		data, _ := json.Marshal(cp)
		if err := tctx.SaveCheckpoint(ctx, data); err != nil {
			return TransitionResult{}, fmt.Errorf("configuring: save checkpoint after configure: %w", err)
		}
	}

	timeout := h.ConfigureTimeout
	if timeout <= 0 {
		timeout = 1 * time.Minute
	}
	status, err := h.Proxmox.WaitForTask(ctx, proxmox.TaskRef{Node: cp.Node, UPID: cp.ConfigTaskUPID}, timeout)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("configuring: wait configure task: %w", err)
	}
	if !status.Success() {
		return TransitionResult{}, fmt.Errorf("configuring: configure task failed: %+v", status)
	}

	data, _ := json.Marshal(cp)
	return TransitionResult{NextState: domain.InstanceNetworkBinding, CheckpointData: data}, nil
}
