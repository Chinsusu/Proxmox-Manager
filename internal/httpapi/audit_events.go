package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
)

// auditEventResponse khớp components.schemas.AuditEvent (UI integration,
// API_UI_Gap_Register mục 3.6).
type auditEventResponse struct {
	ID            string          `json:"id"`
	OccurredAt    time.Time       `json:"occurred_at"`
	ActorType     string          `json:"actor_type"`
	ActorID       string          `json:"actor_id"`
	Action        string          `json:"action"`
	ResourceType  string          `json:"resource_type"`
	ResourceID    string          `json:"resource_id"`
	RequestID     *string         `json:"request_id,omitempty"`
	CorrelationID *string         `json:"correlation_id,omitempty"`
	Before        json.RawMessage `json:"before,omitempty"`
	After         json.RawMessage `json:"after,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

func toAuditEventResponse(e domain.AuditEvent) auditEventResponse {
	return auditEventResponse{
		ID: e.ID, OccurredAt: e.OccurredAt, ActorType: e.ActorType, ActorID: e.ActorID,
		Action: e.Action, ResourceType: e.ResourceType, ResourceID: e.ResourceID,
		RequestID: e.RequestID, CorrelationID: e.CorrelationID,
		Before: e.Before, After: e.After, Metadata: e.Metadata,
	}
}

// AuditEventHandlers gom handler /v1/audit-events* (UI integration,
// API_UI_Gap_Register mục 3.6).
type AuditEventHandlers struct {
	AuditR *audit.Reader
}

func auditFilterFromQuery(r *http.Request) (audit.ListFilter, error) {
	q := r.URL.Query()
	filter := audit.ListFilter{
		Q: q.Get("q"), Actor: q.Get("actor"), Action: q.Get("action"),
		ResourceType: q.Get("resource_type"), ResourceID: q.Get("resource_id"),
	}
	if raw := q.Get("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return audit.ListFilter{}, err
		}
		filter.From = t
	}
	if raw := q.Get("to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return audit.ListFilter{}, err
		}
		filter.To = t
	}
	return filter, nil
}

// List implement GET /v1/audit-events?q=&actor=&action=&resource_type=&resource_id=&from=&to=&limit=&cursor=.
func (h *AuditEventHandlers) List(w http.ResponseWriter, r *http.Request) {
	cursor, limit, err := PageParams(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE_PARAMS", err.Error())
		return
	}
	filter, err := auditFilterFromQuery(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_QUERY_PARAM", "from/to must be RFC3339 timestamps")
		return
	}
	items, err := h.AuditR.List(r.Context(), filter, cursor.After, cursor.AfterID, limit+1)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list audit events")
		return
	}
	page, next := Paginate(items, limit,
		func(e domain.AuditEvent) time.Time { return e.OccurredAt },
		func(e domain.AuditEvent) string { return e.ID })

	resp := make([]auditEventResponse, len(page))
	for i, e := range page {
		resp[i] = toAuditEventResponse(e)
	}
	writeJSON(w, http.StatusOK, listEnvelope[auditEventResponse]{Items: resp, NextCursor: next})
}

// exportPageLimit giới hạn số dòng export mỗi lần gọi — export không
// phân trang qua cursor (client tải hết trong 1 lần), nhưng vẫn cần
// chặn kéo vô hạn toàn bộ bảng audit_events trong một request.
const exportPageLimit = 5000

// Export implement GET /v1/audit-events/export?format=jsonl&... — cùng
// filter với List, trả JSON Lines (mỗi dòng một event) thay vì envelope
// phân trang, tối đa exportPageLimit dòng mới nhất khớp filter.
func (h *AuditEventHandlers) Export(w http.ResponseWriter, r *http.Request) {
	if format := r.URL.Query().Get("format"); format != "" && format != "jsonl" {
		WriteError(w, r, http.StatusBadRequest, "UNSUPPORTED_FORMAT", "only format=jsonl is supported")
		return
	}
	filter, err := auditFilterFromQuery(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_QUERY_PARAM", "from/to must be RFC3339 timestamps")
		return
	}
	items, err := h.AuditR.List(r.Context(), filter, time.Time{}, "", exportPageLimit)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to export audit events")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-events.jsonl"`)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, e := range items {
		_ = enc.Encode(toAuditEventResponse(e))
	}
}
