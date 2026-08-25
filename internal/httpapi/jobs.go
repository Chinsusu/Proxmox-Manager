package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// jobResponse khớp components.schemas.Job.
type jobResponse struct {
	ID             string          `json:"id"`
	InstanceID     string          `json:"instance_id"`
	Operation      string          `json:"operation"`
	State          string          `json:"state"`
	Checkpoint     string          `json:"checkpoint"`
	CheckpointData json.RawMessage `json:"checkpoint_data,omitempty"`
	Priority       int             `json:"priority"`
	Attempt        int             `json:"attempt"`
	MaxAttempts    int             `json:"max_attempts"`
	NextAttemptAt  *time.Time      `json:"next_attempt_at"`
	LeaseOwner     *string         `json:"lease_owner"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at"`
	ErrorCode      *string         `json:"error_code"`
	ErrorMessage   *string         `json:"error_message"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
}

func toJobResponse(j domain.ProvisioningJob) jobResponse {
	return jobResponse{
		ID: j.ID, InstanceID: j.InstanceID, Operation: string(j.Operation), State: string(j.State),
		Checkpoint: string(j.Checkpoint), CheckpointData: j.CheckpointData,
		Priority: j.Priority, Attempt: j.Attempt, MaxAttempts: j.MaxAttempts,
		NextAttemptAt: &j.NextAttemptAt, LeaseOwner: j.LeaseOwner, LeaseExpiresAt: j.LeaseExpiresAt,
		ErrorCode: j.ErrorCode, ErrorMessage: j.ErrorMessage,
		CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
	}
}

// jobEventResponse khớp components.schemas.JobEvent. JobID luôn được
// điền bằng job đang truy vấn — audit_events không có cột job_id
// (append-only, gắn theo resource_type/resource_id="vm_instance"/
// instance_id, xem internal/audit/reader.go), nên "events của job X"
// thực chất là lịch sử audit của INSTANCE sở hữu job X, có thể gồm cả
// event từ job khác trên cùng instance (vd job trước đó đã FAILED rồi
// job này được tạo để retry) — đây là gần đúng đã biết, không phải lỗi.
type jobEventResponse struct {
	EventID       string          `json:"event_id"`
	Type          string          `json:"type"`
	InstanceID    string          `json:"instance_id"`
	JobID         string          `json:"job_id"`
	From          *string         `json:"from"`
	To            *string         `json:"to"`
	CorrelationID *string         `json:"correlation_id"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

func toJobEventResponse(e domain.AuditEvent, jobID string) jobEventResponse {
	resp := jobEventResponse{
		EventID: e.ID, Type: e.Action, InstanceID: e.ResourceID, JobID: jobID,
		CorrelationID: e.CorrelationID, OccurredAt: e.OccurredAt, Metadata: e.Metadata,
	}
	var fromTo struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(e.Metadata, &fromTo); err == nil {
		if fromTo.From != "" {
			resp.From = &fromTo.From
		}
		if fromTo.To != "" {
			resp.To = &fromTo.To
		}
	}
	return resp
}

// JobHandlers gom các handler /v1/jobs*.
type JobHandlers struct {
	Jobs   *jobs.Repository
	AuditR *audit.Reader
	DB     *storage.DB
	Idem   *storage.IdempotencyRepository
}

// List implement GET /v1/jobs?state=&operation=&instance_id=&q=&limit=&cursor=
// (UI integration, API_UI_Gap_Register mục 3.1).
func (h *JobHandlers) List(w http.ResponseWriter, r *http.Request) {
	cursor, limit, err := PageParams(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE_PARAMS", err.Error())
		return
	}
	filter := jobs.ListFilter{
		State:      r.URL.Query().Get("state"),
		Operation:  r.URL.Query().Get("operation"),
		InstanceID: r.URL.Query().Get("instance_id"),
		Q:          r.URL.Query().Get("q"),
	}
	items, err := h.Jobs.List(r.Context(), filter, cursor.After, cursor.AfterID, limit+1)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list jobs")
		return
	}
	page, next := Paginate(items, limit,
		func(j domain.ProvisioningJob) time.Time { return j.CreatedAt },
		func(j domain.ProvisioningJob) string { return j.ID })

	resp := make([]jobResponse, len(page))
	for i, j := range page {
		resp[i] = toJobResponse(j)
	}
	writeJSON(w, http.StatusOK, listEnvelope[jobResponse]{Items: resp, NextCursor: next})
}

// Get implement GET /v1/jobs/{id}.
func (h *JobHandlers) Get(w http.ResponseWriter, r *http.Request) {
	job, err := h.Jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeGetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toJobResponse(*job))
}

// Retry implement POST /v1/jobs/{id}/retry — reason trong ReasonRequest
// không có cột riêng để lưu trên provisioning_jobs (khác
// instance.Quarantine/Decommission có audit event riêng); OpenAPI vẫn
// yêu cầu body này nên vẫn validate, chỉ chưa persist reason ở đâu khác
// ngoài log — gap nhỏ, không chặn chức năng retry.
func (h *JobHandlers) Retry(w http.ResponseWriter, r *http.Request) {
	_, body, ok := readReasonRequest(w, r)
	if !ok {
		return
	}
	key, ok := RequireIdempotencyKey(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	status, respBody, replayed, err := RunIdempotent(r.Context(), h.DB, h.Idem, "jobs.retry", key, body,
		func(ctx context.Context, tx *sql.Tx) (int, any, string, error) {
			requeued, err := h.Jobs.Requeue(ctx, tx, id)
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusAccepted, acceptedJobResponse{InstanceID: requeued.InstanceID, JobID: requeued.ID, State: string(requeued.State)}, requeued.ID, nil
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

// Events implement GET /v1/jobs/{id}/events.
func (h *JobHandlers) Events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := h.Jobs.Get(r.Context(), id)
	if err != nil {
		writeGetError(w, r, err)
		return
	}
	cursor, limit, err := PageParams(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE_PARAMS", err.Error())
		return
	}

	events, err := h.AuditR.ListByResource(r.Context(), "vm_instance", job.InstanceID, cursor.After, cursor.AfterID, limit+1)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list job events")
		return
	}
	page, next := Paginate(events, limit,
		func(e domain.AuditEvent) time.Time { return e.OccurredAt },
		func(e domain.AuditEvent) string { return e.ID })

	resp := make([]jobEventResponse, len(page))
	for i, e := range page {
		resp[i] = toJobEventResponse(e, id)
	}
	writeJSON(w, http.StatusOK, listEnvelope[jobEventResponse]{Items: resp, NextCursor: next})
}
