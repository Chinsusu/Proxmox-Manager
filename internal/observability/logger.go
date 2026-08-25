// Package observability cung cấp structured JSON logger dùng chung cho
// vmf-api, vmf-worker và vmf-cli theo Phần II mục 12 và Phần IX mục 2
// của bộ tài liệu thiết kế.
package observability

import (
	"context"
	"log/slog"
	"os"
)

// redactedKeys liệt kê field không bao giờ được log nguyên văn, khớp
// danh sách redaction ở Phần IX mục 2 (token/password/private key,
// raw machine-id, cloud-init user-data thô).
var redactedKeys = map[string]struct{}{
	"token":           {},
	"password":        {},
	"secret":          {},
	"private_key":     {},
	"machine_id":      {},
	"cloud_init_data": {},
	"authorization":   {},
	"api_key":         {},
}

const redactedPlaceholder = "[REDACTED]"

// redactHandler bọc một slog.Handler để chặn field nhạy cảm trước khi ghi ra.
type redactHandler struct {
	slog.Handler
}

func (h redactHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redactAttr(a))
		return true
	})
	return h.Handler.Handle(ctx, nr)
}

func (h redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return redactHandler{Handler: h.Handler.WithAttrs(out)}
}

func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{Handler: h.Handler.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if _, found := redactedKeys[a.Key]; found {
		return slog.String(a.Key, redactedPlaceholder)
	}
	return a
}

// New tạo logger JSON structured, component là tên service
// (vmf-api / vmf-worker / vmf-cli) gắn vào mọi log line.
func New(component string, level slog.Level) *slog.Logger {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	h := redactHandler{Handler: base}
	return slog.New(h).With("component", component)
}

// WithCorrelation gắn request_id/job_id/instance_id vào logger con,
// tham số rỗng bị bỏ qua để không log field rác.
func WithCorrelation(l *slog.Logger, requestID, jobID, instanceID string) *slog.Logger {
	if requestID != "" {
		l = l.With("request_id", requestID)
	}
	if jobID != "" {
		l = l.With("job_id", jobID)
	}
	if instanceID != "" {
		l = l.With("instance_id", instanceID)
	}
	return l
}
