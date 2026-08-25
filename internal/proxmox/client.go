package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClientConfig cấu hình kết nối một Proxmox cluster — khớp
// appendices/vm-factory-config.example.yaml (proxmox.clusters[]).
type ClientConfig struct {
	BaseURL string // vd https://pve.example:8006/api2/json
	TokenID string // vd vmfactory@pve!automation
	Secret  string
	// InsecureSkipVerify chỉ dùng cho lab/dev với self-signed cert.
	// Phần III mục 2: "CA validation bắt buộc; không InsecureSkipVerify
	// production" — set true ở đây là vi phạm guardrail nếu dùng cho
	// cluster production.
	InsecureSkipVerify bool
	RequestTimeout     time.Duration
	// Metrics, khi khác nil, nhận một sự kiện cho MỖI request Proxmox
	// (Phần 3 tài liệu 09: vmf_pve_api_requests_total/vmf_pve_api_latency_seconds).
	// Optional — nil là no-op, không bắt buộc caller phải wiring metrics.
	Metrics MetricsRecorder
}

// MetricsRecorder là hook observability tối thiểu Client gọi sau MỖI
// request thật tới Proxmox — đặt ở đây (thay vì import internal/observability
// trực tiếp) để package proxmox không phụ thuộc ngược vào observability;
// *observability.Metrics thoả interface này qua duck typing.
type MetricsRecorder interface {
	ObserveProxmoxRequest(operation, status string, duration time.Duration)
}

// Client là HTTP client thô gọi REST API Proxmox bằng API token,
// không dùng SDK ngoài — theo ADR-004 (Proxmox API thay shelling out qm).
type Client struct {
	baseURL    string
	authHeader string
	httpClient *http.Client
	metrics    MetricsRecorder
}

// NewClient tạo Client từ ClientConfig.
func NewClient(cfg ClientConfig) *Client {
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL:    strings.TrimSuffix(cfg.BaseURL, "/"),
		authHeader: fmt.Sprintf("PVEAPIToken=%s=%s", cfg.TokenID, cfg.Secret),
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}, //nolint:gosec // G402: co dieu kien qua ClientConfig.InsecureSkipVerify, mac dinh false, chi bat tuong minh cho lab/dev (xem doc field).
			},
		},
		metrics: cfg.Metrics,
	}
}

// apiEnvelope là shape chung của mọi response Proxmox: {"data": ...}.
type apiEnvelope struct {
	Data json.RawMessage `json:"data"`
}

// do gọi một endpoint Proxmox, form-encode params theo đúng cách PVE
// API nhận tham số (không phải JSON body), trả về phần "data" đã giải
// nén. Lỗi được classify về *Error theo Phần III mục 11. operation là
// tên nghiệp vụ ổn định (vd "clone", "get_vm") dùng làm metric label —
// KHÔNG dùng path thô vì path chứa node/vmid biến thiên, sẽ tạo cardinality
// vô hạn cho vmf_pve_api_requests_total.
func (c *Client) do(ctx context.Context, operation, method, path string, params url.Values) (json.RawMessage, error) {
	start := time.Now()
	data, err := c.doRequest(ctx, method, path, params)
	if c.metrics != nil {
		c.metrics.ObserveProxmoxRequest(operation, requestStatusLabel(err), time.Since(start))
	}
	return data, err
}

// requestStatusLabel map error về nhãn status ổn định cho metric — dùng
// *Error.Code khi có (vd PVE_AUTH_FAILED) vì hữu ích hơn hẳn cho alert
// (Phần 4 tài liệu 09: ProxmoxAuthFailed) so với riêng HTTP status thô.
func requestStatusLabel(err error) string {
	if err == nil {
		return "ok"
	}
	var pveErr *Error
	if errors.As(err, &pveErr) {
		return pveErr.Code
	}
	return "network_error"
}

func (c *Client) doRequest(ctx context.Context, method, path string, params url.Values) (json.RawMessage, error) {
	var body io.Reader
	fullPath := c.baseURL + path

	// GET/DELETE nhan tham so qua query string; Proxmox tra 501 "Unexpected
	// content for method 'DELETE'" neu gui form body cho DELETE (verify
	// that tren cluster PVE 9.1.6 that, khong phai suy doan).
	if (method == http.MethodGet || method == http.MethodDelete) && params != nil {
		if encoded := params.Encode(); encoded != "" {
			fullPath += "?" + encoded
		}
	} else if params != nil {
		body = strings.NewReader(params.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, fullPath, body)
	if err != nil {
		return nil, fmt.Errorf("proxmox: build request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxmox: request %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("proxmox: read response %s %s: %w", method, path, err)
	}

	if resp.StatusCode >= 300 {
		return nil, classifyError(resp.StatusCode, extractErrorMessage(raw))
	}

	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("proxmox: decode envelope %s %s: %w", method, path, err)
	}
	return env.Data, nil
}

// extractErrorMessage đọc field "message"/"errors" trong response lỗi
// Proxmox nếu có, fallback về raw body.
func extractErrorMessage(raw []byte) string {
	var parsed struct {
		Message string                     `json:"message"`
		Errors  map[string]json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || (parsed.Message == "" && len(parsed.Errors) == 0) {
		return string(raw)
	}
	if len(parsed.Errors) > 0 {
		details, _ := json.Marshal(parsed.Errors)
		return fmt.Sprintf("%s %s", parsed.Message, details)
	}
	return parsed.Message
}
