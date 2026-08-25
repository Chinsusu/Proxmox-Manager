package httpapi

import (
	"encoding/json"
	"net/http"
)

// ReadyChecker báo hiệu service đã sẵn sàng nhận traffic (vd: DB đã kết
// nối được). cmd/api wiring implementation thật; P0-00 chỉ định nghĩa contract.
type ReadyChecker interface {
	Ready() error
}

// HealthHandler ứng với GET /v1/health trong api/openapi.yaml — public,
// không qua AuthMiddleware, chỉ xác nhận process còn sống.
func HealthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

// ReadyHandler ứng với GET /v1/ready — public, trả 503 nếu dependency
// (DB, ...) chưa sẵn sàng.
func ReadyHandler(checker ReadyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := checker.Ready(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "not_ready", "reason": err.Error()})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	}
}
