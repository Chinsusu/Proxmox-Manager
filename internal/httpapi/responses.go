package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

// actorFromContext trả subject của principal đã xác thực (gắn bởi
// AuthMiddleware) để ghi vào AuditEvent.ActorID — "unknown" nếu vì lý do
// nào đó context chưa qua middleware (không nên xảy ra ở route cần auth,
// nhưng audit event không được để trống actor).
func actorFromContext(ctx context.Context) string {
	if p, ok := PrincipalFromContext(ctx); ok {
		return p.Subject
	}
	return "unknown"
}

// listEnvelope khớp cấu trúc { items: [...], next_cursor: string|null }
// dùng chung cho mọi endpoint list có pagination (Phần II mục 10).
type listEnvelope[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

// writeJSON ghi body dạng JSON với status code cho trước.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeGetError map lỗi từ một GET-by-id sang ErrorEnvelope phù hợp —
// domain.ErrNotFound → 404, còn lại → 500.
func writeGetError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
		return
	}
	WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal error")
}

// writeMutationError map lỗi từ một mutation (create/action) sang
// ErrorEnvelope phù hợp theo domain sentinel error (Phần II mục 10).
func writeMutationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		WriteError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key already used with a different request body")
	case errors.Is(err, domain.ErrInvalidTransition):
		WriteError(w, r, http.StatusConflict, "INVALID_TRANSITION", err.Error())
	case errors.Is(err, domain.ErrCapacityExhausted):
		WriteError(w, r, http.StatusConflict, "CAPACITY_UNAVAILABLE", "no capacity available")
	case errors.Is(err, domain.ErrAlreadyLeased), errors.Is(err, domain.ErrLeaseLost):
		WriteError(w, r, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, domain.ErrVersionConflict):
		WriteError(w, r, http.StatusConflict, "VERSION_CONFLICT", "resource has changed since it was last read")
	default:
		WriteError(w, r, http.StatusInternalServerError, "INTERNAL", "internal error")
	}
}
