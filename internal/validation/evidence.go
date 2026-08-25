package validation

import (
	"encoding/json"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

// BuildEvidence gộp danh sách Check thành evidence JSON khớp ví dụ ở
// Phần VIII mục 11 ("ruleset_version"/"instance_id"/"result"/"checks"),
// đồng thời trả domain.ValidationResult tương ứng để caller không phải
// tự lặp lại logic PASS/WARN/FAIL (Phần VIII mục 8).
func BuildEvidence(instanceID string, checks []Check) (json.RawMessage, domain.ValidationResult, error) {
	pass, warnFail := Aggregate(checks)
	result := domain.ValidationPass
	switch {
	case !pass:
		result = domain.ValidationFail
	case warnFail:
		result = domain.ValidationWarn
	}
	data, err := json.Marshal(map[string]any{
		"ruleset_version": RulesetVersion,
		"instance_id":     instanceID,
		"result":          result,
		"checks":          checks,
	})
	return data, result, err
}
