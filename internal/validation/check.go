package validation

import "time"

// RulesetVersion định danh phiên bản ruleset identity/network/egress —
// khớp giá trị ví dụ ở Phần VIII mục 11
// ("ruleset_version": "identity-network-egress-1.0"). Đổi giá trị này
// khi thêm/sửa rule làm thay đổi kết quả PASS/FAIL của instance đã
// từng được đánh giá.
const RulesetVersion = "identity-network-egress-1.0"

// collectorName xuất hiện trong mọi Check — nhận diện worker nào chạy
// đánh giá (Phần VIII mục 1: field "collector").
const collectorName = "vmf-worker"

// Severity theo Phần VIII mục 1 — quyết định một check FAIL có chặn
// instance chuyển READY hay chỉ cảnh báo (Phần VIII mục 8).
type Severity string

// Các giá trị hợp lệ của Severity.
const (
	SeverityBlock Severity = "BLOCK"
	SeverityWarn  Severity = "WARN"
)

// Check là một dòng evidence cho một rule, khớp format ở Phần VIII mục
// 1 và ví dụ mục 11.
type Check struct {
	RuleID      string    `json:"rule_id"`
	Version     string    `json:"version"`
	Severity    Severity  `json:"severity"`
	Expected    string    `json:"expected"`
	Observed    string    `json:"observed"`
	Result      string    `json:"result"` // "PASS" hoặc "FAIL"
	CollectedAt time.Time `json:"collected_at"`
	Collector   string    `json:"collector"`
}

// newCheck tạo một Check đã điền version/collector, result suy từ pass.
func newCheck(ruleID string, sev Severity, expected, observed string, pass bool, collectedAt time.Time) Check {
	result := "FAIL"
	if pass {
		result = "PASS"
	}
	return Check{
		RuleID:      ruleID,
		Version:     RulesetVersion,
		Severity:    sev,
		Expected:    expected,
		Observed:    observed,
		Result:      result,
		CollectedAt: collectedAt,
		Collector:   collectorName,
	}
}

// Aggregate gộp danh sách Check thành domain.ValidationResult theo quy
// tắc Phần VIII mục 8:
//
//	FAIL: ít nhất một rule BLOCK fail
//	WARN: không có BLOCK fail, nhưng có WARN fail
//	PASS: mọi rule đều pass
//
// UNKNOWN không phải kết quả của Aggregate — đó là trạng thái riêng
// caller tự set khi bản thân collector lỗi trước khi có Check nào để
// aggregate (Phần VIII mục 8: "UNKNOWN ở identity/network/egress không
// được chuyển READY").
func Aggregate(checks []Check) (pass bool, hasWarnFailure bool) {
	blockFail := false
	warnFail := false
	for _, c := range checks {
		if c.Result == "PASS" {
			continue
		}
		if c.Severity == SeverityBlock {
			blockFail = true
		} else {
			warnFail = true
		}
	}
	return !blockFail, warnFail
}

func isLowerHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
