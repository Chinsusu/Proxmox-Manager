package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/storage"
	"github.com/Chinsusu/vm-factory/internal/template"
)

// instanceResponse khớp components.schemas.Instance. egress_binding
// luôn null — bảng egress_bindings chưa được populate bởi bất kỳ code
// nào (gap đã biết từ P0-05, xem internal/stateengine/rollback.go) nên
// trả null thay vì bịa dữ liệu trung thực hơn là giả vờ có.
type instanceResponse struct {
	ID                string          `json:"id"`
	LogicalName       string          `json:"logical_name"`
	Hostname          string          `json:"hostname"`
	State             string          `json:"state"`
	Generation        int             `json:"generation"`
	TemplateID        string          `json:"template_id"`
	PVEClusterID      *string         `json:"pve_cluster_id"`
	PVENode           *string         `json:"pve_node"`
	VMID              *int            `json:"vmid"`
	IPAddress         *string         `json:"ip_address"`
	NetworkSegmentID  *string         `json:"network_segment_id"`
	EgressBinding     any             `json:"egress_binding"`
	DesiredConfigHash *string         `json:"desired_config_hash"`
	WorkloadAdapter   *string         `json:"workload_adapter"`
	CurrentJobID      *string         `json:"current_job_id"`
	Version           int64           `json:"version"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	RetiredAt         *time.Time      `json:"retired_at"`
	Capabilities      capabilities    `json:"capabilities"`
	DesiredConfig     json.RawMessage `json:"-"`
}

// capabilities khớp API_UI_Gap_Register mục 6 — GẦN ĐÚNG theo instance
// state, KHÔNG kiểm tra state job hiện tại (vd Retry thật ra cần đúng
// job đang FAILED/RETRY_WAIT, không chỉ instance chưa READY/RETIRED).
// Backend vẫn là nguồn thật cuối cùng — mỗi handler tự re-check tại
// thời điểm action, trả 409 nếu state đã đổi giữa lúc render và lúc
// gọi (đúng như gap register mục 6 yêu cầu), field này chỉ giúp UI ẩn/
// hiện nút hợp lý, không thay thế check đó.
type capabilities struct {
	Retry        bool `json:"retry"`
	Rebuild      bool `json:"rebuild"`
	Quarantine   bool `json:"quarantine"`
	Decommission bool `json:"decommission"`
}

func capabilitiesForState(state domain.InstanceState) capabilities {
	switch state {
	case domain.InstanceRetired, domain.InstanceDraining, domain.InstanceDecommissioning, domain.InstanceReleasingResources:
		return capabilities{}
	case domain.InstanceReady:
		return capabilities{Rebuild: true, Quarantine: true, Decommission: true}
	case domain.InstanceQuarantined:
		return capabilities{Retry: true, Rebuild: true, Decommission: true}
	default:
		// dang trong pipeline provisioning (RESERVING..APPLYING_WORKLOAD)
		// hoac REQUESTED - co the dang ket, retry/quarantine hop ly;
		// rebuild/decommission cho phep nhung it dung khi chua toi READY.
		return capabilities{Retry: true, Rebuild: true, Quarantine: true, Decommission: true}
	}
}

// desiredConfigPayload là shape lưu trong vm_instances.desired_config —
// vm-factory chưa có cột riêng cho network_segment_id/egress_policy_id/
// resources trên vm_instances (chỉ có DesiredConfig JSONB catch-all,
// theo đúng thiết kế gốc — xem comment gap ở P0-05
// ConfiguringHandler), nên intent lúc tạo instance được gói vào đây.
// egress_policy_id là tham chiếu OPAQUE tới PGW thật (P0-04 chưa triển
// khai) — vm-factory không có bảng egress_policies riêng để validate.
type desiredConfigPayload struct {
	NetworkSegmentID string          `json:"network_segment_id"`
	EgressPolicyID   string          `json:"egress_policy_id"`
	RequestedIP      *string         `json:"requested_ip,omitempty"`
	Resources        json.RawMessage `json:"resources,omitempty"`
}

func toInstanceResponse(inst domain.VMInstance, ipAddress, currentJobID *string) instanceResponse {
	resp := instanceResponse{
		ID: inst.ID, LogicalName: inst.LogicalName, Hostname: inst.Hostname,
		State: string(inst.State), Generation: inst.Generation, TemplateID: inst.TemplateID,
		PVEClusterID: inst.PVEClusterID, PVENode: inst.PVENode, VMID: inst.VMID,
		IPAddress: ipAddress, DesiredConfigHash: inst.DesiredConfigHash, WorkloadAdapter: inst.WorkloadAdapter,
		CurrentJobID: currentJobID, Version: inst.Version,
		CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt, RetiredAt: inst.RetiredAt,
		Capabilities: capabilitiesForState(inst.State),
	}
	var cfg desiredConfigPayload
	if err := json.Unmarshal(inst.DesiredConfig, &cfg); err == nil && cfg.NetworkSegmentID != "" {
		resp.NetworkSegmentID = &cfg.NetworkSegmentID
	}
	return resp
}

// InstanceHandlers gom các handler /v1/instances*.
type InstanceHandlers struct {
	Instances *instance.Repository
	Templates *template.Repository
	Segments  *ipam.SegmentRepository
	IPAM      *ipam.Repository
	Hostnames *ipam.HostnameRepository
	Jobs      *jobs.Repository
	Runs      *storage.ValidationRunRepository
	AuditR    *audit.Reader
	AuditW    *audit.Writer
	DB        *storage.DB
	Idem      *storage.IdempotencyRepository
}

// enrich đọc thêm ip_address/current_job_id (không có sẵn trên
// domain.VMInstance) cho response — best-effort, lỗi ErrNotFound (chưa
// có allocation/job) không phải lỗi thật, chỉ để trống.
func (h *InstanceHandlers) enrich(ctx context.Context, inst domain.VMInstance) instanceResponse {
	var ipAddr, jobID *string
	if alloc, err := h.IPAM.FindByInstance(ctx, inst.ID); err == nil {
		ipAddr = &alloc.Address
	}
	if id, err := h.Instances.FindCurrentJobID(ctx, inst.ID); err == nil {
		jobID = &id
	}
	return toInstanceResponse(inst, ipAddr, jobID)
}

// List implement GET /v1/instances.
func (h *InstanceHandlers) List(w http.ResponseWriter, r *http.Request) {
	cursor, limit, err := PageParams(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE_PARAMS", err.Error())
		return
	}
	items, err := h.Instances.List(r.Context(), cursor.After, cursor.AfterID, limit+1)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list instances")
		return
	}
	page, next := Paginate(items, limit,
		func(i domain.VMInstance) time.Time { return i.CreatedAt },
		func(i domain.VMInstance) string { return i.ID })

	resp := make([]instanceResponse, len(page))
	for i, inst := range page {
		resp[i] = h.enrich(r.Context(), inst)
	}
	writeJSON(w, http.StatusOK, listEnvelope[instanceResponse]{Items: resp, NextCursor: next})
}

// Get implement GET /v1/instances/{id}.
func (h *InstanceHandlers) Get(w http.ResponseWriter, r *http.Request) {
	inst, err := h.Instances.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeGetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.enrich(r.Context(), *inst))
}

type acceptedJobResponse struct {
	InstanceID string `json:"instance_id"`
	JobID      string `json:"job_id"`
	State      string `json:"state"`
}

type instanceCreateRequest struct {
	LogicalName      string          `json:"logical_name"`
	TemplateID       string          `json:"template_id"`
	NetworkSegmentID string          `json:"network_segment_id"`
	EgressPolicyID   string          `json:"egress_policy_id"`
	RequestedIP      *string         `json:"requested_ip"`
	Resources        json.RawMessage `json:"resources,omitempty"`
	Workload         *struct {
		Adapter string          `json:"adapter"`
		Spec    json.RawMessage `json:"spec"`
	} `json:"workload"`
}

// hostnamePrefix là prefix cố định cho HostnameRepository.Next (Phần II
// mục 8.3: "{prefix}-{sequence:04d}") — chưa có config riêng cho giá
// trị này, dùng một default cố định thay vì đọc từ request (hostname
// không nên do client tự chọn tuỳ ý, tránh xung đột/đặt tên tuỳ tiện).
const hostnamePrefix = "vmf"

// Create implement POST /v1/instances.
func (h *InstanceHandlers) Create(w http.ResponseWriter, r *http.Request) {
	key, ok := RequireIdempotencyKey(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_BODY", "failed to read request body")
		return
	}
	var req instanceCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}
	if req.TemplateID == "" || req.NetworkSegmentID == "" || req.EgressPolicyID == "" {
		WriteError(w, r, http.StatusBadRequest, "MISSING_FIELD", "template_id/network_segment_id/egress_policy_id are required")
		return
	}

	status, respBody, replayed, err := RunIdempotent(r.Context(), h.DB, h.Idem, "instances.create", key, body,
		func(ctx context.Context, tx *sql.Tx) (int, any, string, error) {
			tpl, err := h.Templates.Get(ctx, req.TemplateID)
			if err != nil {
				return 0, nil, "", err
			}
			if !tpl.IsUsable() {
				return 0, nil, "", fmt.Errorf("%w: template %s is state %s, must be ACTIVE", domain.ErrInvalidTransition, req.TemplateID, tpl.State)
			}
			if _, err := h.Segments.Get(ctx, req.NetworkSegmentID); err != nil {
				return 0, nil, "", err
			}

			hostname, err := h.Hostnames.Next(ctx, hostnamePrefix)
			if err != nil {
				return 0, nil, "", err
			}
			logicalName := req.LogicalName
			if logicalName == "" {
				logicalName = hostname
			}

			cfg, err := json.Marshal(desiredConfigPayload{
				NetworkSegmentID: req.NetworkSegmentID, EgressPolicyID: req.EgressPolicyID,
				RequestedIP: req.RequestedIP, Resources: req.Resources,
			})
			if err != nil {
				return 0, nil, "", fmt.Errorf("httpapi: marshal desired config: %w", err)
			}

			newInst := domain.VMInstance{LogicalName: logicalName, Hostname: hostname, TemplateID: req.TemplateID, DesiredConfig: cfg}
			if req.Workload != nil && req.Workload.Adapter != "" {
				adapter := req.Workload.Adapter
				newInst.WorkloadAdapter = &adapter
				newInst.WorkloadSpec = req.Workload.Spec
			}

			created, err := h.Instances.Create(ctx, tx, newInst)
			if err != nil {
				return 0, nil, "", err
			}
			job, err := h.Jobs.Create(ctx, tx, created.ID, domain.JobOpProvision, domain.InstanceRequested)
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusAccepted, acceptedJobResponse{InstanceID: created.ID, JobID: job.ID, State: string(domain.InstanceRequested)}, created.ID, nil
		})
	if err != nil {
		writeMutationError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, status, respBody)
}

// validationRunResponse khớp components.schemas.ValidationRun.
type validationRunResponse struct {
	ID             string          `json:"id"`
	InstanceID     string          `json:"instance_id"`
	JobID          *string         `json:"job_id"`
	Type           string          `json:"type"`
	Result         string          `json:"result"`
	RulesetVersion string          `json:"ruleset_version"`
	Evidence       json.RawMessage `json:"evidence,omitempty"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
}

func toValidationRunResponse(v domain.ValidationRun) validationRunResponse {
	return validationRunResponse{
		ID: v.ID, InstanceID: v.InstanceID, JobID: v.JobID, Type: v.Type, Result: string(v.Result),
		RulesetVersion: v.RulesetVersion, Evidence: v.Evidence, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt,
	}
}

// Evidence implement GET /v1/instances/{id}/evidence.
func (h *InstanceHandlers) Evidence(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.Instances.Get(r.Context(), id); err != nil {
		writeGetError(w, r, err)
		return
	}
	runs, err := h.Runs.LatestPerType(r.Context(), id)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to load evidence")
		return
	}
	resp := make([]validationRunResponse, len(runs))
	for i, run := range runs {
		resp[i] = toValidationRunResponse(run)
	}
	writeJSON(w, http.StatusOK, listEnvelope[validationRunResponse]{Items: resp})
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

func readReasonRequest(w http.ResponseWriter, r *http.Request) (reasonRequest, []byte, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_BODY", "failed to read request body")
		return reasonRequest{}, nil, false
	}
	var req reasonRequest
	if err := json.Unmarshal(body, &req); err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return reasonRequest{}, nil, false
	}
	if len(req.Reason) < 3 || len(req.Reason) > 1000 {
		WriteError(w, r, http.StatusBadRequest, "INVALID_REASON", "reason must be 3-1000 characters")
		return reasonRequest{}, nil, false
	}
	return req, body, true
}

// Retry implement POST /v1/instances/{id}/retry — tìm job hiện tại của
// instance rồi Requeue (cùng cơ chế với POST /v1/jobs/{id}/retry, chỉ
// khác điểm vào: theo instance thay vì theo job).
func (h *InstanceHandlers) Retry(w http.ResponseWriter, r *http.Request) {
	key, ok := RequireIdempotencyKey(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	status, respBody, replayed, err := RunIdempotent(r.Context(), h.DB, h.Idem, "instances.retry", key, []byte(id),
		func(ctx context.Context, tx *sql.Tx) (int, any, string, error) {
			if _, err := h.Instances.Get(ctx, id); err != nil {
				return 0, nil, "", err
			}
			jobID, err := h.Instances.FindCurrentJobID(ctx, id)
			if err != nil {
				return 0, nil, "", err
			}
			requeued, err := h.Jobs.Requeue(ctx, tx, jobID)
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusAccepted, acceptedJobResponse{InstanceID: id, JobID: requeued.ID, State: string(requeued.Checkpoint)}, id, nil
		})
	if err != nil {
		writeMutationError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, status, respBody)
}

// Quarantine implement POST /v1/instances/{id}/quarantine — chuyển
// state trực tiếp + ghi audit reason. KHÔNG tự suspend PGW mapping hay
// đánh dấu IP quarantined (đó là stateengine.Quarantine, cần
// pgw.Adapter/*proxmox.Adapter thật) — hành động qua API chỉ tạo intent
// rõ ràng trong DB, phần dọn dẹp side-effect ngoài DB do worker xử lý
// khi job-lease loop được nối dây (gap đã biết, cmd/worker hiện chỉ là
// stub — xem P0-05..P0-08).
func (h *InstanceHandlers) Quarantine(w http.ResponseWriter, r *http.Request) {
	key, ok := RequireIdempotencyKey(w, r)
	if !ok {
		return
	}
	req, body, ok := readReasonRequest(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	status, respBody, replayed, err := RunIdempotent(r.Context(), h.DB, h.Idem, "instances.quarantine", key, body,
		func(ctx context.Context, tx *sql.Tx) (int, any, string, error) {
			inst, err := h.Instances.Get(ctx, id)
			if err != nil {
				return 0, nil, "", err
			}
			if err := h.Instances.UpdateState(ctx, tx, id, domain.InstanceQuarantined); err != nil {
				return 0, nil, "", err
			}
			metadata, _ := json.Marshal(map[string]string{"reason": req.Reason, "from": string(inst.State), "to": string(domain.InstanceQuarantined)})
			if err := h.AuditW.Append(ctx, tx, domain.AuditEvent{
				ActorType: "operator", ActorID: actorFromContext(ctx), Action: "quarantine",
				ResourceType: "vm_instance", ResourceID: id, Metadata: metadata,
			}); err != nil {
				return 0, nil, "", err
			}
			updated, err := h.Instances.Get(ctx, id)
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusAccepted, h.enrich(ctx, *updated), id, nil
		})
	if err != nil {
		writeMutationError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, status, respBody)
}

// Rebuild implement POST /v1/instances/{id}/rebuild — nghỉ hưu instance
// hiện tại, tạo instance mới cùng logical_name với generation+1 (Phần V
// mục 8), kèm job PROVISION mới.
func (h *InstanceHandlers) Rebuild(w http.ResponseWriter, r *http.Request) {
	key, ok := RequireIdempotencyKey(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	status, respBody, replayed, err := RunIdempotent(r.Context(), h.DB, h.Idem, "instances.rebuild", key, []byte(id),
		func(ctx context.Context, tx *sql.Tx) (int, any, string, error) {
			old, err := h.Instances.Get(ctx, id)
			if err != nil {
				return 0, nil, "", err
			}
			if err := h.Instances.Retire(ctx, tx, id); err != nil {
				return 0, nil, "", err
			}

			hostname, err := h.Hostnames.Next(ctx, hostnamePrefix)
			if err != nil {
				return 0, nil, "", err
			}
			newInst, err := h.Instances.Create(ctx, tx, domain.VMInstance{
				LogicalName: old.LogicalName, Hostname: hostname, TemplateID: old.TemplateID,
				Generation: old.Generation + 1, DesiredConfig: old.DesiredConfig,
				WorkloadAdapter: old.WorkloadAdapter, WorkloadSpec: old.WorkloadSpec,
			})
			if err != nil {
				return 0, nil, "", err
			}
			job, err := h.Jobs.Create(ctx, tx, newInst.ID, domain.JobOpRebuild, domain.InstanceRequested)
			if err != nil {
				return 0, nil, "", err
			}
			metadata, _ := json.Marshal(map[string]string{"rebuilt_from": id})
			if err := h.AuditW.Append(ctx, tx, domain.AuditEvent{
				ActorType: "operator", ActorID: actorFromContext(ctx), Action: "rebuild",
				ResourceType: "vm_instance", ResourceID: newInst.ID, Metadata: metadata,
			}); err != nil {
				return 0, nil, "", err
			}
			return http.StatusAccepted, acceptedJobResponse{InstanceID: newInst.ID, JobID: job.ID, State: string(domain.InstanceRequested)}, newInst.ID, nil
		})
	if err != nil {
		writeMutationError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, status, respBody)
}

// Decommission implement POST /v1/instances/{id}/decommission — chuyển
// DRAINING (bước đầu chuỗi retirement Phần II mục 2.3: DRAINING →
// DECOMMISSIONING → RELEASING_RESOURCES → RETIRED) + job DECOMMISSION.
// Handler cho các bước retirement sau DRAINING CHƯA tồn tại trong state
// engine (gap đã biết, ngoài phạm vi P0-09) — API chỉ tạo intent.
func (h *InstanceHandlers) Decommission(w http.ResponseWriter, r *http.Request) {
	key, ok := RequireIdempotencyKey(w, r)
	if !ok {
		return
	}
	req, body, ok := readReasonRequest(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	status, respBody, replayed, err := RunIdempotent(r.Context(), h.DB, h.Idem, "instances.decommission", key, body,
		func(ctx context.Context, tx *sql.Tx) (int, any, string, error) {
			inst, err := h.Instances.Get(ctx, id)
			if err != nil {
				return 0, nil, "", err
			}
			if err := h.Instances.UpdateState(ctx, tx, id, domain.InstanceDraining); err != nil {
				return 0, nil, "", err
			}
			job, err := h.Jobs.Create(ctx, tx, id, domain.JobOpDecommission, domain.InstanceDraining)
			if err != nil {
				return 0, nil, "", err
			}
			metadata, _ := json.Marshal(map[string]string{"reason": req.Reason, "from": string(inst.State), "to": string(domain.InstanceDraining)})
			if err := h.AuditW.Append(ctx, tx, domain.AuditEvent{
				ActorType: "operator", ActorID: actorFromContext(ctx), Action: "decommission",
				ResourceType: "vm_instance", ResourceID: id, Metadata: metadata,
			}); err != nil {
				return 0, nil, "", err
			}
			return http.StatusAccepted, acceptedJobResponse{InstanceID: id, JobID: job.ID, State: string(domain.InstanceDraining)}, id, nil
		})
	if err != nil {
		writeMutationError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, status, respBody)
}
