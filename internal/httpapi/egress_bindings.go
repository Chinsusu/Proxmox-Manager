package httpapi

import (
	"net/http"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// egressBindingResponse khớp API_UI_Gap_Register mục 3.3 — SUY RA từ
// provisioning_jobs.checkpoint_data + validation_runs (type=egress),
// KHÔNG phải bảng egress_bindings riêng (gap đã biết, chờ P0-04). Field
// "policy"/"expected_exit" không có nguồn dữ liệu nào hiện tại (không
// track theo từng instance) — luôn rỗng cho tới khi có pgw.Adapter thật,
// cố tình để rỗng thay vì suy đoán giá trị giả.
type egressBindingResponse struct {
	InstanceID      string     `json:"instance_id"`
	LogicalName     string     `json:"logical_name"`
	Hostname        string     `json:"hostname"`
	PGWClientID     string     `json:"pgw_client_id"`
	PGWMappingID    string     `json:"pgw_mapping_id"`
	Generation      int64      `json:"generation"`
	BoundAt         time.Time  `json:"bound_at"`
	State           string     `json:"state"`
	ProofResult     string     `json:"proof_result"`
	ProofAgeSeconds *float64   `json:"proof_age_seconds,omitempty"`
	ProofCheckedAt  *time.Time `json:"proof_checked_at,omitempty"`
}

// EgressBindingHandlers gom handler /v1/egress-bindings (UI
// integration, API_UI_Gap_Register mục 3.3).
type EgressBindingHandlers struct {
	DB   *storage.DB
	Runs *storage.ValidationRunRepository
}

// List implement GET /v1/egress-bindings?state=&instance_id=&proof_result=.
// KHÔNG phân trang cursor — nguồn dữ liệu (checkpoint_data JSONB) không
// có index hỗ trợ keyset hiệu quả, và số lượng binding thực tế nhỏ
// (bằng số instance đã qua NETWORK_BINDING), giống cách /v1/network-segments
// không phân trang.
func (h *EgressBindingHandlers) List(w http.ResponseWriter, r *http.Request) {
	instanceID := r.URL.Query().Get("instance_id")
	wantState := r.URL.Query().Get("state")
	wantProofResult := r.URL.Query().Get("proof_result")

	bindings, err := h.DB.ListEgressBindings(r.Context(), instanceID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list egress bindings")
		return
	}

	resp := make([]egressBindingResponse, 0, len(bindings))
	for _, b := range bindings {
		item := egressBindingResponse{
			InstanceID: b.InstanceID, LogicalName: b.LogicalName, Hostname: b.Hostname,
			PGWClientID: b.PGWClientID, PGWMappingID: b.PGWMappingID, Generation: b.Generation, BoundAt: b.BoundAt,
			State: "PENDING", ProofResult: "UNKNOWN",
		}
		if run, err := h.Runs.LatestByType(r.Context(), b.InstanceID, "egress"); err == nil {
			item.ProofResult = string(run.Result)
			startedAt := run.StartedAt
			item.ProofCheckedAt = &startedAt
			age := time.Since(startedAt).Seconds()
			item.ProofAgeSeconds = &age
			if run.Result == domain.ValidationPass {
				item.State = "ACTIVE"
			} else {
				item.State = "SUSPENDED"
			}
		}

		if wantState != "" && item.State != wantState {
			continue
		}
		if wantProofResult != "" && item.ProofResult != wantProofResult {
			continue
		}
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, listEnvelope[egressBindingResponse]{Items: resp})
}
