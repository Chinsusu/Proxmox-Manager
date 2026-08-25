package stateengine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/pgw"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// networkBindingCheckpoint kế thừa configuringCheckpoint.
type networkBindingCheckpoint struct {
	configuringCheckpoint
	PGWClientID  string `json:"pgw_client_id,omitempty"`
	PGWMappingID string `json:"pgw_mapping_id,omitempty"`
	// DesiredGeneration là generation ActivateMapping trả về — cần lưu
	// lại để ValidatingEgressHandler (P0-07) so sánh với
	// EgressEvidence.RulesGeneration (EGR-002 "desired == applied",
	// Phần VIII mục 6), không thể tính lại vì ActivateMapping là side
	// effect không idempotent để gọi lại chỉ nhằm đọc giá trị.
	DesiredGeneration int64 `json:"desired_generation,omitempty"`
}

// NetworkBindingHandler thực hiện 4.5 NETWORK_BINDING → BOOTING (Phần
// V): tạo PGW client + mapping + activate TRƯỚC khi boot (Phần VII
// mục 4: "Không tạo mapping ACTIVE sau khi guest đã boot").
//
// Dùng pgw.Adapter qua interface — với pgw.NoopAdapter (chưa có cluster
// PGW thật, epic P0-04 chưa triển khai) mọi giá trị trả về đều đánh
// dấu SIMULATED, KHÔNG phải egress binding thật. Chưa ghi bảng
// egress_bindings (gap đã biết) — chỉ lưu tham chiếu trong
// checkpoint_data, để lại cho lần củng cố khi có pgw.Adapter thật.
type NetworkBindingHandler struct {
	PGW      pgw.Adapter
	PolicyID string
}

// Execute implement TransitionHandler.
func (h *NetworkBindingHandler) Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error) {
	var cp networkBindingCheckpoint
	if err := json.Unmarshal(tctx.CheckpointData, &cp); err != nil || cp.VMID == 0 {
		return TransitionResult{}, fmt.Errorf("network_binding: missing placement checkpoint")
	}

	if cp.PGWClientID == "" {
		clientRef, err := h.PGW.CreateClient(ctx, pgw.ClientRequest{
			Name:     tctx.Instance.Hostname,
			Enabled:  true,
			Metadata: map[string]string{"vmf_instance_id": tctx.Instance.ID},
		})
		if err != nil {
			return TransitionResult{}, fmt.Errorf("network_binding: create pgw client: %w", err)
		}
		cp.PGWClientID = clientRef.ID
		data, _ := json.Marshal(cp)
		if err := tctx.SaveCheckpoint(ctx, data); err != nil {
			return TransitionResult{}, fmt.Errorf("network_binding: save checkpoint after create client: %w", err)
		}
	}

	if cp.PGWMappingID == "" {
		mappingRef, err := h.PGW.CreateMapping(ctx, pgw.MappingRequest{ClientID: cp.PGWClientID, PolicyID: h.PolicyID})
		if err != nil {
			return TransitionResult{}, fmt.Errorf("network_binding: create pgw mapping: %w", err)
		}
		cp.PGWMappingID = mappingRef.ID
		data, _ := json.Marshal(cp)
		if err := tctx.SaveCheckpoint(ctx, data); err != nil {
			return TransitionResult{}, fmt.Errorf("network_binding: save checkpoint after create mapping: %w", err)
		}
	}

	gen, err := h.PGW.ActivateMapping(ctx, cp.PGWMappingID)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("network_binding: activate mapping: %w", err)
	}
	cp.DesiredGeneration = int64(gen)

	data, _ := json.Marshal(cp)
	return TransitionResult{NextState: domain.InstanceBooting, CheckpointData: data}, nil
}

// BootingHandler thực hiện 4.6 BOOTING → WAITING_GUEST (Phần V):
// read-before-write — nếu VM đã running thì không start lại (Phần V
// mục 4.6: "if VM already running, do not issue duplicate start").
type BootingHandler struct {
	Proxmox     *proxmox.Adapter
	BootTimeout time.Duration
}

// Execute implement TransitionHandler.
func (h *BootingHandler) Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error) {
	var cp networkBindingCheckpoint
	if err := json.Unmarshal(tctx.CheckpointData, &cp); err != nil || cp.VMID == 0 {
		return TransitionResult{}, fmt.Errorf("booting: missing placement checkpoint")
	}
	ref := proxmox.VMRef{Node: cp.Node, VMID: cp.VMID}

	vm, err := h.Proxmox.GetVM(ctx, ref)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("booting: get vm status: %w", err)
	}

	if !vm.IsRunning() {
		task, err := h.Proxmox.Start(ctx, ref)
		if err != nil {
			return TransitionResult{}, fmt.Errorf("booting: start: %w", err)
		}
		timeout := h.BootTimeout
		if timeout <= 0 {
			timeout = 1 * time.Minute
		}
		status, err := h.Proxmox.WaitForTask(ctx, task, timeout)
		if err != nil {
			return TransitionResult{}, fmt.Errorf("booting: wait start task: %w", err)
		}
		if !status.Success() {
			return TransitionResult{}, fmt.Errorf("booting: start task failed: %+v", status)
		}
	}

	data, _ := json.Marshal(cp)
	return TransitionResult{NextState: domain.InstanceWaitingGuest, CheckpointData: data}, nil
}
