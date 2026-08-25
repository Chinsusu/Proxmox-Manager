package validation

import (
	"testing"
	"time"
)

func TestClassifyFailedChecks_AllPassYieldsNoFindings(t *testing.T) {
	checks := []Check{
		newCheck("ID-001", SeverityBlock, "x", "x", true, time.Now()),
		newCheck("ID-008", SeverityWarn, "x", "x", true, time.Now()),
	}
	findings := classifyFailedChecks(checks)
	if len(findings) != 0 {
		t.Fatalf("classifyFailedChecks() = %+v, want empty (khong co check nao fail)", findings)
	}
}

func TestClassifyFailedChecks_BlockFailureIsQuarantineWorthy(t *testing.T) {
	checks := []Check{
		newCheck("NET-001", SeverityBlock, "1", "2", false, time.Now()),
	}
	findings := classifyFailedChecks(checks)
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want 1", findings)
	}
	if findings[0].Classification != DriftQuarantineWorthy {
		t.Errorf("Classification = %s, want %s (BLOCK severity)", findings[0].Classification, DriftQuarantineWorthy)
	}
	if findings[0].Category != "NET-001" {
		t.Errorf("Category = %s, want NET-001", findings[0].Category)
	}
}

func TestClassifyFailedChecks_WarnFailureIsRepairable(t *testing.T) {
	checks := []Check{
		newCheck("ID-008", SeverityWarn, "valid UUID", "garbage", false, time.Now()),
	}
	findings := classifyFailedChecks(checks)
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want 1", findings)
	}
	if findings[0].Classification != DriftRepairable {
		t.Errorf("Classification = %s, want %s (WARN severity)", findings[0].Classification, DriftRepairable)
	}
}

func TestClassifyFailedChecks_MixedSeverities(t *testing.T) {
	checks := []Check{
		newCheck("ID-001", SeverityBlock, "x", "x", true, time.Now()),
		newCheck("NET-001", SeverityBlock, "1", "2", false, time.Now()),
		newCheck("ID-008", SeverityWarn, "x", "y", false, time.Now()),
	}
	findings := classifyFailedChecks(checks)
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want 2 (chi cac check FAIL)", findings)
	}
}
