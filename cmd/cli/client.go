package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// apiClient gọi vmf-api qua HTTP — theo Phần X mục 4 ("Operator →
// vmf-api: REST/CLI, TLS + JWT/API token").
type apiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newAPIClient(baseURL, token string) *apiClient {
	return &apiClient{baseURL: baseURL, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// apiError là ErrorEnvelope.error giải mã lại từ response lỗi — khớp
// api/openapi.yaml components.schemas.ErrorEnvelope.
type apiError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id"`
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s: %s (request_id=%s)", e.Code, e.Message, e.RequestID)
}

// do gửi request, giải mã JSON response vào out (nil nếu không cần đọc
// body — vd 204 No Content). idempotencyKey rỗng nghĩa là không gửi
// header đó (dùng cho GET).
func (c *apiClient) do(ctx context.Context, method, path string, idempotencyKey string, body, out any) (statusCode int, err error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	// c.baseURL đến từ --api-url flag/VMF_API_URL env do operator CLI tự
	// cấu hình lúc chạy lệnh (giống Auth.JWTPublicKeyFile ở cmd/api), path
	// là hằng số nội bộ do từng lệnh CLI tự truyền — không phải input máy
	// chủ nhận từ bên ngoài, gosec G704 SSRF taint ở đây là false positive.
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader) //nolint:gosec // G704: baseURL do operator tu cau hinh, khong phai request input tu xa
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := c.http.Do(req) //nolint:gosec // G704: cung ly do voi NewRequestWithContext o tren
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var envelope struct {
			Error apiError `json:"error"`
		}
		if jsonErr := json.Unmarshal(respBody, &envelope); jsonErr == nil && envelope.Error.Code != "" {
			return resp.StatusCode, &envelope.Error
		}
		return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// randomIdempotencyKey sinh key ngẫu nhiên khi operator không tự truyền
// --idempotency-key — mỗi lần chạy CLI là một ý định mới, không nên bắt
// operator tự nghĩ key cho thao tác một lần (khác kịch bản automation
// muốn retry-safe, nơi nên tự truyền --idempotency-key cố định).
func randomIdempotencyKey(prefix string) string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return prefix + "-" + hex.EncodeToString(buf)
}
