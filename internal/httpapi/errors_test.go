package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Chinsusu/vm-factory/internal/observability"
)

func TestWriteError_EnvelopeShapeAndRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/instances", nil)
	req = req.WithContext(observability.WithRequestID(req.Context(), "req_abc"))
	rec := httptest.NewRecorder()

	WriteError(rec, req, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "same key, different payload")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}

	var body ErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body.Error.Code != "IDEMPOTENCY_CONFLICT" {
		t.Errorf("error.code = %q", body.Error.Code)
	}
	if body.Error.RequestID != "req_abc" {
		t.Errorf("error.request_id = %q, want req_abc", body.Error.RequestID)
	}
}
