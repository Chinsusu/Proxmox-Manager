package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	base := slog.NewJSONHandler(buf, nil)
	return slog.New(redactHandler{Handler: base})
}

func TestLogger_RedactsSensitiveFields(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	logger.Info("bootstrap done", "token", "super-secret-value", "instance_id", "ins_123")

	line := buf.String()
	if strings.Contains(line, "super-secret-value") {
		t.Fatalf("log line leaked secret value: %s", line)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if decoded["token"] != redactedPlaceholder {
		t.Fatalf("token field = %v, want %q", decoded["token"], redactedPlaceholder)
	}
	if decoded["instance_id"] != "ins_123" {
		t.Fatalf("non-sensitive field instance_id was altered: %v", decoded["instance_id"])
	}
}

func TestLogger_RedactsAttrsAddedViaWith(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf).With("password", "hunter2", "component", "vmf-api")

	logger.Info("service started")

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if decoded["password"] != redactedPlaceholder {
		t.Fatalf("password field leaked via With(): %v", decoded["password"])
	}
	if decoded["component"] != "vmf-api" {
		t.Fatalf("component field missing/altered: %v", decoded["component"])
	}
}

func TestWithCorrelation_SkipsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	logger := WithCorrelation(base, "req_1", "", "ins_1")

	logger.Info("event")

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if _, present := decoded["job_id"]; present {
		t.Fatalf("job_id should be absent when empty, got: %v", decoded["job_id"])
	}
	if decoded["request_id"] != "req_1" || decoded["instance_id"] != "ins_1" {
		t.Fatalf("unexpected correlation fields: %v", decoded)
	}
}
