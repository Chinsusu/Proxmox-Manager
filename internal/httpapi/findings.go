package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// findingResponse khớp components.schemas.Finding.
type findingResponse struct {
	ID           string          `json:"id"`
	Category     string          `json:"category"`
	Severity     string          `json:"severity"`
	ResourceType *string         `json:"resource_type"`
	ResourceID   *string         `json:"resource_id"`
	Summary      string          `json:"summary"`
	Details      json.RawMessage `json:"details,omitempty"`
	State        string          `json:"state"`
	DetectedAt   time.Time       `json:"detected_at"`
}

func toFindingResponse(f domain.Finding) findingResponse {
	return findingResponse{
		ID: f.ID, Category: f.Category, Severity: string(f.Severity),
		ResourceType: f.ResourceType, ResourceID: f.ResourceID,
		Summary: f.Summary, Details: f.Details, State: string(f.State), DetectedAt: f.DetectedAt,
	}
}

// FindingHandlers gom các handler /v1/findings*.
type FindingHandlers struct {
	Findings *storage.FindingRepository
}

// List implement GET /v1/findings. Hỗ trợ lọc theo ?state= (không thuộc
// OpenAPI query params tường minh nhưng FindingRepository.List đã sẵn
// hỗ trợ — rỗng nghĩa là mọi state, khớp hành vi mặc định của endpoint).
// Không phân trang qua cursor thật (FindingRepository.List hiện chưa
// nhận limit/offset) — trả next_cursor=null luôn, gap nhỏ để lại nếu số
// lượng finding OPEN lớn trong thực tế cần cắt trang sau này.
func (h *FindingHandlers) List(w http.ResponseWriter, r *http.Request) {
	state := domain.FindingState(r.URL.Query().Get("state"))
	findings, err := h.Findings.List(r.Context(), state)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list findings")
		return
	}
	resp := make([]findingResponse, len(findings))
	for i, f := range findings {
		resp[i] = toFindingResponse(f)
	}
	writeJSON(w, http.StatusOK, listEnvelope[findingResponse]{Items: resp})
}
