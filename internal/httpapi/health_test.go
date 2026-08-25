package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler_AlwaysOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()

	HealthHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

type stubReadyChecker struct{ err error }

func (s stubReadyChecker) Ready() error { return s.err }

func TestReadyHandler_Ready(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	rec := httptest.NewRecorder()

	ReadyHandler(stubReadyChecker{})(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestReadyHandler_NotReady(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	rec := httptest.NewRecorder()

	ReadyHandler(stubReadyChecker{err: errors.New("db unreachable")})(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
