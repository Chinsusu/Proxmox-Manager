package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/Chinsusu/vm-factory/internal/observability"
)

// ErrorEnvelope khớp components.schemas.ErrorEnvelope trong
// api/openapi.yaml — mọi response lỗi của vmf-api phải theo format này.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id"`
}

// WriteError ghi ErrorEnvelope JSON với request_id lấy từ context
// (gắn bởi observability.RequestIDMiddleware).
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	WriteErrorWithDetails(w, r, status, code, message, nil)
}

func WriteErrorWithDetails(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	envelope := ErrorEnvelope{Error: ErrorBody{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: observability.RequestIDFromContext(r.Context()),
	}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope)
}
