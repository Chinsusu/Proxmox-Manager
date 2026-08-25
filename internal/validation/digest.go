// Package validation implement identity/network/egress rule engine và
// drift scanner theo Phần VIII (ID-xxx/NET-xxx/EGR-xxx rules, HMAC
// identity digest theo ADR-007). Workload rules (WORKLOAD-xxx) đợi
// epic P0-08 (Workload Adapter) chưa triển khai.
package validation

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// IdentityDigester tính HMAC-SHA256 digest của machine-id theo ADR-007
// — raw machine-id chỉ tồn tại trong memory collector (Phần VI mục 3),
// digest này là thứ duy nhất được lưu/so sánh.
type IdentityDigester struct {
	key []byte
}

// NewIdentityDigester tạo IdentityDigester với HMAC key đã đọc sẵn
// (dùng LoadHMACKeyFromFile để đọc từ IdentityConfig.HMACKeyFile).
func NewIdentityDigester(key []byte) *IdentityDigester {
	return &IdentityDigester{key: key}
}

// LoadHMACKeyFromFile đọc HMAC key từ file theo
// config.IdentityConfig.HMACKeyFile (Phần II mục 15: secret qua file,
// không qua biến môi trường trực tiếp).
func LoadHMACKeyFromFile(path string) ([]byte, error) {
	// path đến từ config.Identity.HMACKeyFile do operator chỉ định lúc
	// deploy, không phải request input.
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path là config field do operator kiểm soát
	if err != nil {
		return nil, fmt.Errorf("validation: read hmac key file: %w", err)
	}
	key := bytes.TrimSpace(raw)
	if len(key) == 0 {
		return nil, fmt.Errorf("validation: hmac key file %s is empty", path)
	}
	return key, nil
}

// Digest trả HMAC-SHA256(key, machineID) dạng hex, tiền tố
// "hmac-sha256:" khớp format evidence ở Phần VIII mục 11 (ví dụ
// "observed": "hmac-sha256:...").
func (d *IdentityDigester) Digest(machineID string) string {
	mac := hmac.New(sha256.New, d.key)
	mac.Write([]byte(machineID))
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}
