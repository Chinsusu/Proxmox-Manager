package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddleware_GeneratesWhenMissing(t *testing.T) {
	var captured string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = RequestIDFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()

	RequestIDMiddleware(next).ServeHTTP(rec, req)

	if captured == "" {
		t.Fatal("expected request id to be generated in context")
	}
	if got := rec.Header().Get(requestIDHeader); got != captured {
		t.Fatalf("response header %q = %q, want %q", requestIDHeader, got, captured)
	}
}

func TestRequestIDMiddleware_ReusesClientHeader(t *testing.T) {
	const clientID = "req_client_supplied"
	var captured string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = RequestIDFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set(requestIDHeader, clientID)
	rec := httptest.NewRecorder()

	RequestIDMiddleware(next).ServeHTTP(rec, req)

	if captured != clientID {
		t.Fatalf("captured = %q, want %q (should reuse client-supplied id)", captured, clientID)
	}
}

func TestNewRequestID_Unique(t *testing.T) {
	a := NewRequestID()
	b := NewRequestID()
	if a == b {
		t.Fatalf("expected unique ids, got same value %q twice", a)
	}
}
