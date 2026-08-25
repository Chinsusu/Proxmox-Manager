package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
	"github.com/Chinsusu/vm-factory/internal/template"
)

// templateResponse khớp components.schemas.Template trong api/openapi.yaml.
type templateResponse struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Family           string          `json:"family"`
	Version          string          `json:"version"`
	OSFamily         string          `json:"os_family"`
	OSVersion        string          `json:"os_version"`
	Architecture     string          `json:"architecture"`
	PVEClusterID     string          `json:"pve_cluster_id"`
	PVENode          string          `json:"pve_node"`
	PVETemplateVMID  int             `json:"pve_template_vmid"`
	Storage          *string         `json:"storage"`
	SourceChecksum   string          `json:"source_checksum"`
	BuildManifest    json.RawMessage `json:"build_manifest,omitempty"`
	State            string          `json:"state"`
	ValidationStatus string          `json:"validation_status"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

func toTemplateResponse(t domain.Template) templateResponse {
	resp := templateResponse{
		ID: t.ID, Name: t.Name, Family: t.Family, Version: t.Version,
		OSFamily: t.OSFamily, OSVersion: t.OSVersion, Architecture: t.Architecture,
		PVEClusterID: t.PVEClusterID, PVENode: t.PVENode, PVETemplateVMID: t.PVETemplateVMID,
		SourceChecksum: t.SourceChecksum, BuildManifest: t.BuildManifest,
		State: string(t.State), ValidationStatus: string(t.ValidationStatus),
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
	if t.Storage != "" {
		s := t.Storage
		resp.Storage = &s
	}
	return resp
}

// TemplateHandlers gom các handler /v1/templates*, giữ dependency ở một
// chỗ thay vì closure rời rạc trong main.go.
type TemplateHandlers struct {
	Templates *template.Repository
	DB        *storage.DB
	Idem      *storage.IdempotencyRepository
}

// List implement GET /v1/templates.
func (h *TemplateHandlers) List(w http.ResponseWriter, r *http.Request) {
	cursor, limit, err := PageParams(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_PAGE_PARAMS", err.Error())
		return
	}
	family := r.URL.Query().Get("family")

	items, err := h.Templates.List(r.Context(), family, cursor.After, cursor.AfterID, limit+1)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "failed to list templates")
		return
	}
	page, next := Paginate(items, limit,
		func(t domain.Template) time.Time { return t.CreatedAt },
		func(t domain.Template) string { return t.ID })

	resp := make([]templateResponse, len(page))
	for i, t := range page {
		resp[i] = toTemplateResponse(t)
	}
	writeJSON(w, http.StatusOK, listEnvelope[templateResponse]{Items: resp, NextCursor: next})
}

// Get implement GET /v1/templates/{id}.
func (h *TemplateHandlers) Get(w http.ResponseWriter, r *http.Request) {
	t, err := h.Templates.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeGetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTemplateResponse(*t))
}

// templateCreateRequest khớp components.schemas.TemplateCreate.
type templateCreateRequest struct {
	Name            string          `json:"name"`
	Version         string          `json:"version"`
	Family          string          `json:"family"`
	PVEClusterID    string          `json:"pve_cluster_id"`
	PVENode         string          `json:"pve_node"`
	PVETemplateVMID int             `json:"pve_template_vmid"`
	Storage         string          `json:"storage"`
	SourceChecksum  string          `json:"source_checksum"`
	BuildManifest   json.RawMessage `json:"build_manifest,omitempty"`
}

// Create implement POST /v1/templates.
func (h *TemplateHandlers) Create(w http.ResponseWriter, r *http.Request) {
	key, ok := RequireIdempotencyKey(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_BODY", "failed to read request body")
		return
	}
	var req templateCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}
	if req.Name == "" || req.Version == "" || req.PVEClusterID == "" || req.PVENode == "" ||
		req.PVETemplateVMID == 0 || req.SourceChecksum == "" {
		WriteError(w, r, http.StatusBadRequest, "MISSING_FIELD", "name/version/pve_cluster_id/pve_node/pve_template_vmid/source_checksum are required")
		return
	}

	status, respBody, replayed, err := RunIdempotent(r.Context(), h.DB, h.Idem, "templates.create", key, body,
		func(ctx context.Context, tx *sql.Tx) (int, any, string, error) {
			created, err := h.Templates.Create(ctx, tx, domain.Template{
				Name: req.Name, Family: req.Family, Version: req.Version,
				PVEClusterID: req.PVEClusterID, PVENode: req.PVENode, PVETemplateVMID: req.PVETemplateVMID,
				Storage: req.Storage, SourceChecksum: req.SourceChecksum, BuildManifest: req.BuildManifest,
			})
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusCreated, toTemplateResponse(*created), created.ID, nil
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

// forwardPromotion là bước "promote" mặc định theo Phần IV mục 9
// (DRAFT → CANDIDATE → ACTIVE → DEPRECATED) — POST .../promote không
// nhận target state tường minh (OpenAPI không có requestBody cho
// endpoint này), nên tự suy ra bước tiếp theo từ state hiện tại. Rollback
// DEPRECATED→ACTIVE là hành động khác biệt, không thuộc "promote" đơn
// giản này (cần chỉ định rõ, gap để lại cho một endpoint riêng sau).
var forwardPromotion = map[domain.TemplateState]domain.TemplateState{
	domain.TemplateDraft:     domain.TemplateCandidate,
	domain.TemplateCandidate: domain.TemplateActive,
	domain.TemplateActive:    domain.TemplateDeprecated,
}

// Promote implement POST /v1/templates/{id}/promote.
func (h *TemplateHandlers) Promote(w http.ResponseWriter, r *http.Request) {
	key, ok := RequireIdempotencyKey(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	status, respBody, replayed, err := RunIdempotent(r.Context(), h.DB, h.Idem, "templates.promote", key, []byte(id),
		func(ctx context.Context, tx *sql.Tx) (int, any, string, error) {
			current, err := h.Templates.Get(ctx, id)
			if err != nil {
				return 0, nil, "", err
			}
			target, ok := forwardPromotion[current.State]
			if !ok {
				return 0, nil, "", fmt.Errorf("%w: %s has no forward promotion", domain.ErrInvalidTransition, current.State)
			}
			promoted, err := h.Templates.PromoteTx(ctx, tx, id, target)
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusAccepted, toTemplateResponse(*promoted), promoted.ID, nil
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
