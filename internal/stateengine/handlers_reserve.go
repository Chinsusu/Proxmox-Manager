package stateengine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
	"github.com/Chinsusu/vm-factory/internal/template"
)

// RequestedHandler thực hiện 4.1 REQUESTED → RESERVING (Phần V).
// Guard: template phải ACTIVE. Không có side effect ngoài DB.
type RequestedHandler struct {
	Templates *template.Repository
}

// Execute implement TransitionHandler.
func (h *RequestedHandler) Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error) {
	tpl, err := h.Templates.Get(ctx, tctx.Instance.TemplateID)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("requested: load template: %w", err)
	}
	if !tpl.IsUsable() {
		return TransitionResult{}, fmt.Errorf("%w: template %s state=%s not ACTIVE", domain.ErrInvalidTransition, tpl.ID, tpl.State)
	}
	return TransitionResult{NextState: domain.InstanceReserving, CheckpointData: tctx.CheckpointData}, nil
}

// reservingCheckpoint là dữ liệu ReservingHandler stash để CloningHandler
// đọc lại (Phần II mục 6.2).
type reservingCheckpoint struct {
	IPAllocationID string `json:"ip_allocation_id"`
	VMID           int    `json:"vmid"`
	Node           string `json:"node"`
}

// ReservingHandler thực hiện 4.2 RESERVING → CLONING (Phần V): reserve
// IP + xác định VMID/node.
//
// KHÔNG dùng resource_locks pre-reservation cho VMID (gap đã biết) —
// dựa vào PVE_VMID_CONFLICT từ chính Proxmox làm backstop, đúng như
// Phần III mục 12 đã chấp nhận: "adapter retry với VMID mới trong
// cùng job" khi Proxmox trả VMID đã dùng. CloningHandler hiện KHÔNG
// tự retry với VMID mới khi gặp lỗi này — gap thứ hai, để lại follow-up.
type ReservingHandler struct {
	IPAM           *ipam.Repository
	Proxmox        *proxmox.Adapter
	Node           string
	SegmentID      string
	ReservationTTL time.Duration
}

// Execute implement TransitionHandler.
func (h *ReservingHandler) Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error) {
	var existing reservingCheckpoint
	if len(tctx.CheckpointData) > 0 {
		_ = json.Unmarshal(tctx.CheckpointData, &existing)
	}
	if existing.IPAllocationID != "" && existing.VMID != 0 {
		// Đã reserve ở lần chạy trước (worker crash trước đó) — dùng
		// lại, không reserve thêm lần nữa (Phần II mục 6.3 idempotency).
		data, _ := json.Marshal(existing)
		return TransitionResult{NextState: domain.InstanceCloning, CheckpointData: data}, nil
	}

	alloc, err := h.IPAM.ReserveNextFree(ctx, h.SegmentID, tctx.Instance.ID, h.ReservationTTL)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("reserving: reserve ip: %w", err)
	}

	vmid, err := h.Proxmox.AllocateNextVMID(ctx)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("reserving: allocate vmid: %w", err)
	}

	cp := reservingCheckpoint{IPAllocationID: alloc.ID, VMID: vmid, Node: h.Node}
	data, _ := json.Marshal(cp)
	return TransitionResult{NextState: domain.InstanceCloning, CheckpointData: data}, nil
}
