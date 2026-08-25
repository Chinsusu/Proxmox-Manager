package validation

import (
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/pgw"
)

func baseEgressInput(now time.Time) EgressInput {
	return EgressInput{
		Evidence: pgw.EgressEvidence{
			ClientID:          "cli_1",
			MappingID:         "map_1",
			Result:            "PASS",
			CheckedAt:         now.Add(-10 * time.Second).Format(time.RFC3339),
			IPv4:              "203.0.113.5",
			IPv6:              "BLOCKED",
			Policy:            "web_only",
			DirectLeakPackets: 0,
			ProxyHealth:       "ACTIVE",
			RulesGeneration:   42,
		},
		ExpectedMappingID: "map_1",
		DesiredGeneration: 42,
		DenyIPv6:          true,
		ProofMaxAge:       time.Minute,
		Now:               now,
	}
}

func TestEvaluateEgress_CleanProofPasses(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	checks := EvaluateEgress(baseEgressInput(now))
	pass, _ := Aggregate(checks)
	if !pass {
		t.Fatalf("clean proof phai PASS moi rule, checks=%+v", checks)
	}
}

func TestEvaluateEgress_MappingMismatchFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	in := baseEgressInput(now)
	in.Evidence.MappingID = "map_other"

	c := checkByRuleID(EvaluateEgress(in), "EGR-001")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("EGR-001 = %+v, want FAIL (mapping mismatch)", c)
	}
}

func TestEvaluateEgress_GenerationMismatchFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	in := baseEgressInput(now)
	in.Evidence.RulesGeneration = 41

	c := checkByRuleID(EvaluateEgress(in), "EGR-002")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("EGR-002 = %+v, want FAIL (desired != applied generation)", c)
	}
}

func TestEvaluateEgress_DirectLeakFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	in := baseEgressInput(now)
	in.Evidence.DirectLeakPackets = 3

	c := checkByRuleID(EvaluateEgress(in), "EGR-005")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("EGR-005 = %+v, want FAIL (direct leak packets > 0)", c)
	}
}

func TestEvaluateEgress_IPv6NotBlockedFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	in := baseEgressInput(now)
	in.Evidence.IPv6 = "2001:db8::1"

	c := checkByRuleID(EvaluateEgress(in), "EGR-004")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("EGR-004 = %+v, want FAIL (policy deny nhung IPv6 khong BLOCKED)", c)
	}
}

func TestEvaluateEgress_ProxyUnhealthyFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	in := baseEgressInput(now)
	in.Evidence.ProxyHealth = "DEGRADED"

	c := checkByRuleID(EvaluateEgress(in), "EGR-006")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("EGR-006 = %+v, want FAIL", c)
	}
}

func TestEvaluateEgress_StaleProofFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	in := baseEgressInput(now)
	in.Evidence.CheckedAt = now.Add(-10 * time.Minute).Format(time.RFC3339)
	in.ProofMaxAge = time.Minute

	c := checkByRuleID(EvaluateEgress(in), "EGR-007")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("EGR-007 = %+v, want FAIL (proof qua tuoi ProofMaxAge)", c)
	}
}

func TestEvaluateEgress_UnparseableCheckedAtFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	in := baseEgressInput(now)
	in.Evidence.CheckedAt = ""

	c := checkByRuleID(EvaluateEgress(in), "EGR-007")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("EGR-007 = %+v, want FAIL cho checked_at rong/khong parse duoc (khong duoc coi la PASS)", c)
	}
}

// TestEvaluateEgress_NoopAdapterEvidenceFails xac nhan pgw.NoopAdapter
// (chua co PGW that, P0-04 chua trien khai) KHONG lam rule engine
// rubber-stamp PASS gia — moi field SIMULATED phai lam it nhat vai
// BLOCK rule FAIL that su.
func TestEvaluateEgress_NoopAdapterEvidenceFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	noop := pgw.NewNoopAdapter()
	evidence, err := noop.EgressProof(t.Context(), "cli_1")
	if err != nil {
		t.Fatalf("NoopAdapter.EgressProof() error: %v", err)
	}

	checks := EvaluateEgress(EgressInput{
		Evidence:          evidence,
		ExpectedMappingID: "map_1",
		DesiredGeneration: 1,
		DenyIPv6:          true,
		ProofMaxAge:       time.Minute,
		Now:               now,
	})
	pass, _ := Aggregate(checks)
	if pass {
		t.Fatalf("NoopAdapter evidence khong duoc lam validation PASS (rubber-stamp gia), checks=%+v", checks)
	}
}
