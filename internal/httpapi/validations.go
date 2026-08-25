package httpapi

import (
	"net/http"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// ValidationHandlers gom handler /v1/validations* (UI integration,
// API_UI_Gap_Register mục 3.4).
//
// CHỈ có List (GET) — không có Create (POST /v1/validations "trigger
// ad-hoc validation"). Validation run trong hệ thống này luôn là SẢN
// PHẨM PHỤ của một state transition thật trong pipeline provisioning
// (WAITING_GUEST→VALIDATING_EGRESS, VALIDATING_EGRESS→APPLYING_WORKLOAD,
// xem internal/stateengine) — không có cơ chế "chạy validation độc lập
// ngoài pipeline" nào tồn tại để implement POST cho thật. Xây POST giả
// (chỉ ghi audit event mà không chạy rule thật) sẽ là bằng chứng giả —
// để lại gap này rõ ràng thay vì tạo automation không thật.
type ValidationHandlers struct {
	Runs *storage.ValidationRunRepository
}

// List implement GET /v1/validations?instance_id=&result=&type=&limit=&cursor=.
func (h *ValidationHandlers) List(w http.ResponseWriter, r *http.Request) {
	cursor, limit, err := PageParams(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE_PARAMS", err.Error())
		return
	}
	filter := storage.ValidationListFilter{
		InstanceID: r.URL.Query().Get("instance_id"),
		Result:     r.URL.Query().Get("result"),
		Type:       r.URL.Query().Get("type"),
	}
	items, err := h.Runs.List(r.Context(), filter, cursor.After, cursor.AfterID, limit+1)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list validation runs")
		return
	}
	page, next := Paginate(items, limit,
		func(v domain.ValidationRun) time.Time { return v.StartedAt },
		func(v domain.ValidationRun) string { return v.ID })

	resp := make([]validationRunResponse, len(page))
	for i, v := range page {
		resp[i] = toValidationRunResponse(v)
	}
	writeJSON(w, http.StatusOK, listEnvelope[validationRunResponse]{Items: resp, NextCursor: next})
}
