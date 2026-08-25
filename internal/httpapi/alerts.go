package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// alertResponse khớp API_UI_Gap_Register mục 3.5.
type alertResponse struct {
	ID               string     `json:"id"`
	Status           string     `json:"status"`
	Severity         string     `json:"severity"`
	ResourceType     string     `json:"resource_type"`
	ResourceID       string     `json:"resource_id"`
	Title            string     `json:"title"`
	Description      string     `json:"description,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	AcknowledgedAt   *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy   *string    `json:"acknowledged_by,omitempty"`
	AcknowledgedNote *string    `json:"acknowledged_note,omitempty"`
	Version          int        `json:"version"`
}

func toAlertResponse(a domain.Alert) alertResponse {
	return alertResponse{
		ID: a.ID, Status: string(a.Status), Severity: a.Severity,
		ResourceType: a.ResourceType, ResourceID: a.ResourceID, Title: a.Title, Description: a.Description,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		AcknowledgedAt: a.AcknowledgedAt, AcknowledgedBy: a.AcknowledgedBy, AcknowledgedNote: a.AcknowledgedNote,
		Version: a.Version,
	}
}

// AlertHandlers gom handler /v1/alerts* (UI integration,
// API_UI_Gap_Register mục 3.5).
type AlertHandlers struct {
	Alerts *storage.AlertRepository
}

// List implement GET /v1/alerts?status=&severity=&resource_type=&limit=&cursor=.
func (h *AlertHandlers) List(w http.ResponseWriter, r *http.Request) {
	cursor, limit, err := PageParams(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE_PARAMS", err.Error())
		return
	}
	filter := storage.AlertListFilter{
		Status: r.URL.Query().Get("status"), Severity: r.URL.Query().Get("severity"),
		ResourceType: r.URL.Query().Get("resource_type"),
	}
	items, err := h.Alerts.List(r.Context(), filter, cursor.After, cursor.AfterID, limit+1)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list alerts")
		return
	}
	page, next := Paginate(items, limit,
		func(a domain.Alert) time.Time { return a.CreatedAt },
		func(a domain.Alert) string { return a.ID })

	resp := make([]alertResponse, len(page))
	for i, a := range page {
		resp[i] = toAlertResponse(a)
	}
	writeJSON(w, http.StatusOK, listEnvelope[alertResponse]{Items: resp, NextCursor: next})
}

type alertAcknowledgeRequest struct {
	Note    string `json:"note,omitempty"`
	Version int    `json:"version,omitempty"`
}

// Acknowledge implement POST /v1/alerts/{id}/acknowledge — không qua
// RunIdempotent (không phải mutation tạo resource mới, ack lặp lại
// cùng ID vốn đã idempotent ở tầng DB qua optimistic version check).
func (h *AlertHandlers) Acknowledge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_BODY", "failed to read request body")
		return
	}
	var req alertAcknowledgeRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
			return
		}
	}
	var note *string
	if req.Note != "" {
		note = &req.Note
	}

	updated, err := h.Alerts.Acknowledge(r.Context(), id, actorFromContext(r.Context()), note, req.Version)
	if err != nil {
		writeMutationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAlertResponse(*updated))
}
