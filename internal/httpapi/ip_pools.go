package httpapi

import (
	"net/http"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/ipam"
)

// ipPoolWarningThreshold là ngưỡng utilization (0..1) coi là "sắp hết
// IP" — chưa có field cấu hình riêng cho việc này (chưa phải nhu cầu
// thật ngoài UI mới yêu cầu), dùng hằng số cố định thay vì suy đoán một
// cơ chế cấu hình chưa cần tới.
const ipPoolWarningThreshold = 0.85

// ipPoolResponse khớp API_UI_Gap_Register mục 3.2 ("segment, CIDR,
// total/available/reserved/assigned/quarantined counts, utilization,
// warning threshold").
type ipPoolResponse struct {
	SegmentID        string  `json:"segment_id"`
	SegmentName      string  `json:"segment_name"`
	CIDR             string  `json:"cidr"`
	State            string  `json:"state"`
	Total            int     `json:"total"`
	Available        int     `json:"available"`
	Reserved         int     `json:"reserved"`
	Assigned         int     `json:"assigned"`
	Quarantined      int     `json:"quarantined"`
	UtilizationRatio float64 `json:"utilization_ratio"`
	WarningThreshold float64 `json:"warning_threshold"`
}

func toIPPoolResponse(seg domain.NetworkSegment, capacity ipam.SegmentCapacity) ipPoolResponse {
	var utilization float64
	if capacity.Total > 0 {
		utilization = float64(capacity.Assigned+capacity.Reserved) / float64(capacity.Total)
	}
	return ipPoolResponse{
		SegmentID: seg.ID, SegmentName: seg.Name, CIDR: seg.CIDR, State: seg.State,
		Total: capacity.Total, Available: capacity.Free, Reserved: capacity.Reserved,
		Assigned: capacity.Assigned, Quarantined: capacity.Quarantined,
		UtilizationRatio: utilization, WarningThreshold: ipPoolWarningThreshold,
	}
}

// IPPoolHandlers gom handler /v1/ip-pools (API_UI_Gap_Register mục
// 3.2) — dữ liệu nguồn giống hệt /v1/network-segments (segment +
// ip_allocations capacity), chỉ khác shape response theo đúng contract
// UI cần (utilization/warning_threshold thay vì object capacity lồng nhau).
type IPPoolHandlers struct {
	Segments *ipam.SegmentRepository
}

// List implement GET /v1/ip-pools?segment_id=&state=.
func (h *IPPoolHandlers) List(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	segmentID := r.URL.Query().Get("segment_id")

	segments, err := h.Segments.List(r.Context(), state)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list network segments")
		return
	}

	resp := make([]ipPoolResponse, 0, len(segments))
	for _, seg := range segments {
		if segmentID != "" && seg.ID != segmentID {
			continue
		}
		capacity, err := h.Segments.Capacity(r.Context(), seg.ID)
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to compute segment capacity")
			return
		}
		resp = append(resp, toIPPoolResponse(seg, capacity))
	}
	writeJSON(w, http.StatusOK, listEnvelope[ipPoolResponse]{Items: resp})
}
