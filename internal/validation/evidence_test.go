package validation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

func TestBuildEvidence_AllPassYieldsPass(t *testing.T) {
	checks := []Check{
		newCheck("ID-001", SeverityBlock, "x", "x", true, time.Now()),
		newCheck("ID-008", SeverityWarn, "x", "x", true, time.Now()),
	}
	data, result, err := BuildEvidence("inst-1", checks)
	if err != nil {
		t.Fatalf("BuildEvidence() error: %v", err)
	}
	if result != domain.ValidationPass {
		t.Errorf("result = %s, want PASS", result)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("evidence JSON khong parse duoc: %v", err)
	}
	if decoded["ruleset_version"] != RulesetVersion {
		t.Errorf("ruleset_version = %v, want %v", decoded["ruleset_version"], RulesetVersion)
	}
	if decoded["instance_id"] != "inst-1" {
		t.Errorf("instance_id = %v, want inst-1", decoded["instance_id"])
	}
	if _, ok := decoded["checks"].([]any); !ok {
		t.Errorf("checks phai la mang, got %T", decoded["checks"])
	}
}

func TestBuildEvidence_WarnFailureYieldsWarn(t *testing.T) {
	checks := []Check{
		newCheck("ID-001", SeverityBlock, "x", "x", true, time.Now()),
		newCheck("ID-008", SeverityWarn, "valid UUID", "not-a-uuid", false, time.Now()),
	}
	_, result, err := BuildEvidence("inst-1", checks)
	if err != nil {
		t.Fatalf("BuildEvidence() error: %v", err)
	}
	if result != domain.ValidationWarn {
		t.Errorf("result = %s, want WARN", result)
	}
}

func TestBuildEvidence_BlockFailureYieldsFail(t *testing.T) {
	checks := []Check{
		newCheck("ID-001", SeverityBlock, "x", "y", false, time.Now()),
		newCheck("ID-008", SeverityWarn, "x", "x", true, time.Now()),
	}
	_, result, err := BuildEvidence("inst-1", checks)
	if err != nil {
		t.Fatalf("BuildEvidence() error: %v", err)
	}
	if result != domain.ValidationFail {
		t.Errorf("result = %s, want FAIL", result)
	}
}
