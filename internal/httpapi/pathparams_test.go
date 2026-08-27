package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsCanonicalUUID(t *testing.T) {
	valid := []string{
		"00000000-0000-0000-0000-000000000000",
		"a1b2c3d4-e5f6-7a8b-9c0d-e1f2a3b4c5d6",
		"A1B2C3D4-E5F6-7A8B-9C0D-E1F2A3B4C5D6",
	}
	for _, s := range valid {
		if !isCanonicalUUID(s) {
			t.Errorf("isCanonicalUUID(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",
		"nonexistent-999",
		"a1b2c3d4e5f67a8b9c0de1f2a3b4c5d6",                       // thiếu dấu gạch
		"{a1b2c3d4-e5f6-7a8b-9c0d-e1f2a3b4c5d6}",                 // dạng ngoặc nhọn Postgres chấp nhận nhưng API không
		"a1b2c3d4-e5f6-7a8b-9c0d-e1f2a3b4c5dg",                   // ký tự ngoài hex
		"a1b2c3d4-e5f6-7a8b-9c0d-e1f2a3b4c5d67",                  // dài 37
		"urn:uuid:a1b2c3d4-e5f6-7a8b-9c0d-e1f2a3b4c5d6", // dạng URN
		"a1b2c3d4-e5f6-7a8b-9c0d-e1f2a3b4c5d6 ",         // trailing space
	}
	for _, s := range invalid {
		if isCanonicalUUID(s) {
			t.Errorf("isCanonicalUUID(%q) = true, want false", s)
		}
	}
}

// muxWithUUIDGuard dựng mux tối giản mô phỏng cách cmd/api/main.go wrap
// handler bằng ValidateUUIDParam, để test qua routing thật (PathValue
// được mux set trước khi middleware chạy).
func muxWithUUIDGuard(t *testing.T, pattern string, h http.HandlerFunc) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(pattern, ValidateUUIDParam(h))
	return mux
}

func TestValidateUUIDParam_RejectsMalformedID(t *testing.T) {
	called := false
	mux := muxWithUUIDGuard(t, "GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs/nonexistent-999", nil))

	if called {
		t.Fatal("handler was called for malformed id")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body ErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body.Error.Code != "INVALID_ID" {
		t.Errorf("error.code = %q, want INVALID_ID", body.Error.Code)
	}
}

func TestValidateUUIDParam_PassesValidUUID(t *testing.T) {
	called := false
	mux := muxWithUUIDGuard(t, "GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := r.PathValue("id"); got != "00000000-0000-0000-0000-000000000000" {
			t.Errorf("PathValue(id) = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs/00000000-0000-0000-0000-000000000000", nil))

	if !called {
		t.Fatal("handler was not called for valid UUID")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestValidateUUIDParam_IgnoresRoutesWithoutID(t *testing.T) {
	called := false
	mux := muxWithUUIDGuard(t, "GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs", nil))

	if !called {
		t.Fatal("handler was not called on route without {id}")
	}
}

func TestValidateUUIDParam_RejectsSubresourceRoutes(t *testing.T) {
	// Các route action như POST /v1/jobs/{id}/retry cũng phải được chặn.
	called := false
	mux := muxWithUUIDGuard(t, "POST /v1/jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/jobs/not-a-uuid/retry", nil))

	if called {
		t.Fatal("handler was called for malformed id on subresource route")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
