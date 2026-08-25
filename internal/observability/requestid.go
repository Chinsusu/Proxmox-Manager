package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

// NewRequestID sinh một ID ngẫu nhiên 16 byte dạng hex, đủ dùng làm
// correlation ID (Phần II mục 12) mà không cần thêm dependency UUID.
func NewRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand chỉ fail khi hệ điều hành thiếu nguồn entropy;
		// trong trường hợp đó process không nên tiếp tục phục vụ request.
		panic("observability: crypto/rand unavailable: " + err.Error())
	}
	return "req_" + hex.EncodeToString(buf)
}

// WithRequestID gắn request ID vào context để log/handler downstream đọc lại.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext đọc request ID đã gắn, trả rỗng nếu chưa có.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

const requestIDHeader = "X-Request-Id"

// RequestIDMiddleware đảm bảo mọi request có request_id: dùng lại header
// client gửi lên nếu có (cho phép trace xuyên hệ thống), sinh mới nếu không.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = NewRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
