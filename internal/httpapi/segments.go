package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// segmentCapacityResponse khớp NetworkSegment.capacity trong OpenAPI.
type segmentCapacityResponse struct {
	Total       int `json:"total"`
	Free        int `json:"free"`
	Reserved    int `json:"reserved"`
	Assigned    int `json:"assigned"`
	Quarantined int `json:"quarantined"`
}

// segmentResponse khớp components.schemas.NetworkSegment.
type segmentResponse struct {
	ID                 string                  `json:"id"`
	Name               string                  `json:"name"`
	CIDR               string                  `json:"cidr"`
	Gateway            string                  `json:"gateway"`
	Bridge             string                  `json:"bridge"`
	DNSServers         []string                `json:"dns_servers,omitempty"`
	IPv6Policy         string                  `json:"ipv6_policy"`
	AllocationStrategy string                  `json:"allocation_strategy"`
	State              string                  `json:"state"`
	Capacity           segmentCapacityResponse `json:"capacity"`
}

func toSegmentResponse(seg domain.NetworkSegment, capacity ipam.SegmentCapacity) segmentResponse {
	return segmentResponse{
		ID: seg.ID, Name: seg.Name, CIDR: seg.CIDR, Gateway: seg.Gateway, Bridge: seg.Bridge,
		DNSServers: seg.DNSServers, IPv6Policy: seg.IPv6Policy, AllocationStrategy: seg.AllocationStrategy,
		State: seg.State,
		Capacity: segmentCapacityResponse{
			Total: capacity.Total, Free: capacity.Free, Reserved: capacity.Reserved, Assigned: capacity.Assigned, Quarantined: capacity.Quarantined,
		},
	}
}

// SegmentHandlers gom các handler /v1/network-segments*.
type SegmentHandlers struct {
	Segments *ipam.SegmentRepository
	DB       *storage.DB
	Idem     *storage.IdempotencyRepository
}

// List implement GET /v1/network-segments — không phân trang (OpenAPI
// response schema cho endpoint này không có next_cursor, khác các list
// endpoint khác — số lượng segment trong thực tế nhỏ, một vài subnet).
func (h *SegmentHandlers) List(w http.ResponseWriter, r *http.Request) {
	segments, err := h.Segments.List(r.Context(), "")
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list network segments")
		return
	}
	resp := make([]segmentResponse, len(segments))
	for i, seg := range segments {
		capacity, err := h.Segments.Capacity(r.Context(), seg.ID)
		if err != nil {
			WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to compute segment capacity")
			return
		}
		resp[i] = toSegmentResponse(seg, capacity)
	}
	writeJSON(w, http.StatusOK, listEnvelope[segmentResponse]{Items: resp})
}

type segmentCreateRequest struct {
	Name               string   `json:"name"`
	CIDR               string   `json:"cidr"`
	Gateway            string   `json:"gateway"`
	Bridge             string   `json:"bridge"`
	DNSServers         []string `json:"dns_servers,omitempty"`
	IPv6Policy         string   `json:"ipv6_policy,omitempty"`
	AllocationStrategy string   `json:"allocation_strategy,omitempty"`
}

// Create implement POST /v1/network-segments.
func (h *SegmentHandlers) Create(w http.ResponseWriter, r *http.Request) {
	key, ok := RequireIdempotencyKey(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_BODY", "failed to read request body")
		return
	}
	var req segmentCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}
	if req.Name == "" || req.CIDR == "" || req.Gateway == "" || req.Bridge == "" {
		WriteError(w, r, http.StatusBadRequest, "MISSING_FIELD", "name/cidr/gateway/bridge are required")
		return
	}

	status, respBody, replayed, err := RunIdempotent(r.Context(), h.DB, h.Idem, "network_segments.create", key, body,
		func(ctx context.Context, tx *sql.Tx) (int, any, string, error) {
			created, err := h.Segments.Create(ctx, tx, domain.NetworkSegment{
				Name: req.Name, CIDR: req.CIDR, Gateway: req.Gateway, Bridge: req.Bridge,
				DNSServers: req.DNSServers, IPv6Policy: req.IPv6Policy, AllocationStrategy: req.AllocationStrategy,
			})
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusCreated, toSegmentResponse(*created, ipam.SegmentCapacity{}), created.ID, nil
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
