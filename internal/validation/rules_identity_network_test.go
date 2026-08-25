package validation

import (
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/guest"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// baseIdentityInput trả một IdentityInput "clean clone PASS" — mọi test
// case khác chỉ chỉnh một field để lệch khỏi baseline này (Phần VIII
// mục 13: "clean clone PASS" là test case nền).
func baseIdentityInput() IdentityInput {
	return IdentityInput{
		Facts: guest.Facts{
			MachineID:           "0123456789abcdef0123456789abcdef",
			BootID:              "a1b2c3d4-0000-0000-0000-000000000000",
			Hostname:            "vmf-ins-abc123",
			CloudInitInstanceID: "iid-vmf-1",
			SSHHostKeyFingerprints: map[string]string{
				"ssh_host_ed25519_key.pub": "SHA256:fresh",
			},
			MACAddresses:        []string{"bc:24:11:aa:bb:cc"},
			IPv4Addresses:       []string{"10.98.0.15"},
			GlobalIPv6Addresses: nil,
			NICCount:            1,
			DefaultRouteV4Count: 1,
			DefaultRouteV6Count: 0,
			DefaultGatewayV4:    "10.98.0.1",
			CollectedAt:         time.Now(),
		},
		MachineIDDigest:           "hmac-sha256:deadbeef",
		ExpectedHostname:          "vmf-ins-abc123",
		ExpectedMACAddresses:      []string{"bc:24:11:aa:bb:cc"},
		ExpectedIPv4:              "10.98.0.15",
		ExpectedGatewayV4:         "10.98.0.1",
		RequireSingleNIC:          true,
		RequireSingleDefaultRoute: true,
		DenyIPv6DefaultRoute:      true,
	}
}

func checkByRuleID(checks []Check, ruleID string) *Check {
	for i := range checks {
		if checks[i].RuleID == ruleID {
			return &checks[i]
		}
	}
	return nil
}

func TestEvaluateIdentityAndNetwork_CleanClonePasses(t *testing.T) {
	checks := EvaluateIdentityAndNetwork(baseIdentityInput())
	pass, warnFail := Aggregate(checks)
	if !pass {
		t.Fatalf("clean clone phai PASS moi BLOCK rule, checks=%+v", checks)
	}
	if warnFail {
		t.Errorf("clean clone khong nen co WARN rule fail, checks=%+v", checks)
	}
	// evidence khong duoc chua raw machine-id (Phan VIII muc 13: "evidence redaction").
	if c := checkByRuleID(checks, "ID-001"); c == nil || c.Observed == baseIdentityInput().Facts.MachineID {
		t.Fatalf("ID-001 Observed khong duoc la raw machine-id, got %+v", c)
	}
}

func TestEvaluateIdentityAndNetwork_DuplicateMachineIDFails(t *testing.T) {
	in := baseIdentityInput()
	in.MachineIDDuplicates = []storage.DuplicateMatch{{InstanceID: "other-instance", Retired: false}}

	checks := EvaluateIdentityAndNetwork(in)
	c := checkByRuleID(checks, "ID-002")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("ID-002 = %+v, want FAIL (active fleet duplicate)", c)
	}
	if pass, _ := Aggregate(checks); pass {
		t.Error("aggregate phai FAIL khi ID-002 (BLOCK) that bai")
	}
}

func TestEvaluateIdentityAndNetwork_RetiredDuplicate_PolicyControlsResult(t *testing.T) {
	in := baseIdentityInput()
	in.MachineIDDuplicates = []storage.DuplicateMatch{{InstanceID: "old-instance", Retired: true}}

	in.BlockRetiredDuplicate = false
	pass1 := checkByRuleID(EvaluateIdentityAndNetwork(in), "ID-002")
	if pass1 == nil || pass1.Result != "PASS" {
		t.Errorf("BlockRetiredDuplicate=false: ID-002 = %+v, want PASS", pass1)
	}

	in.BlockRetiredDuplicate = true
	pass2 := checkByRuleID(EvaluateIdentityAndNetwork(in), "ID-002")
	if pass2 == nil || pass2.Result != "FAIL" {
		t.Errorf("BlockRetiredDuplicate=true: ID-002 = %+v, want FAIL", pass2)
	}
}

func TestEvaluateIdentityAndNetwork_DuplicateSSHKeyFails(t *testing.T) {
	in := baseIdentityInput()
	in.SSHFingerprintDuplicates = []storage.DuplicateMatch{{InstanceID: "other-instance", Retired: false}}

	c := checkByRuleID(EvaluateIdentityAndNetwork(in), "ID-003")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("ID-003 = %+v, want FAIL (duplicate SSH host key)", c)
	}
}

func TestEvaluateIdentityAndNetwork_WrongHostnameFails(t *testing.T) {
	in := baseIdentityInput()
	in.Facts.Hostname = "wrong-hostname"

	c := checkByRuleID(EvaluateIdentityAndNetwork(in), "ID-004")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("ID-004 = %+v, want FAIL", c)
	}
}

func TestEvaluateIdentityAndNetwork_SecondNICFails(t *testing.T) {
	in := baseIdentityInput()
	in.Facts.NICCount = 2

	checks := EvaluateIdentityAndNetwork(in)
	if c := checkByRuleID(checks, "NET-001"); c == nil || c.Result != "FAIL" {
		t.Errorf("NET-001 = %+v, want FAIL", c)
	}
	if c := checkByRuleID(checks, "NET-009"); c == nil || c.Result != "FAIL" {
		t.Errorf("NET-009 = %+v, want FAIL (second NIC)", c)
	}
}

func TestEvaluateIdentityAndNetwork_SecondDefaultRouteFails(t *testing.T) {
	in := baseIdentityInput()
	in.Facts.DefaultRouteV4Count = 2

	checks := EvaluateIdentityAndNetwork(in)
	if c := checkByRuleID(checks, "NET-003"); c == nil || c.Result != "FAIL" {
		t.Errorf("NET-003 = %+v, want FAIL", c)
	}
	if c := checkByRuleID(checks, "NET-009"); c == nil || c.Result != "FAIL" {
		t.Errorf("NET-009 = %+v, want FAIL (second route)", c)
	}
}

func TestEvaluateIdentityAndNetwork_IPv6RouteFails(t *testing.T) {
	in := baseIdentityInput()
	in.Facts.DefaultRouteV6Count = 1

	c := checkByRuleID(EvaluateIdentityAndNetwork(in), "NET-004")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("NET-004 = %+v, want FAIL (policy deny nhung co IPv6 default route)", c)
	}
}

func TestEvaluateIdentityAndNetwork_GlobalIPv6Fails(t *testing.T) {
	in := baseIdentityInput()
	in.Facts.GlobalIPv6Addresses = []string{"2001:db8::5"}

	c := checkByRuleID(EvaluateIdentityAndNetwork(in), "NET-005")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("NET-005 = %+v, want FAIL (policy deny nhung co global IPv6)", c)
	}
}

func TestEvaluateIdentityAndNetwork_WrongIPv4Fails(t *testing.T) {
	in := baseIdentityInput()
	in.Facts.IPv4Addresses = []string{"10.98.0.99"}

	c := checkByRuleID(EvaluateIdentityAndNetwork(in), "NET-002")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("NET-002 = %+v, want FAIL", c)
	}
}

func TestEvaluateIdentityAndNetwork_MalformedMachineIDFailsFormat(t *testing.T) {
	in := baseIdentityInput()
	in.Facts.MachineID = "not-hex-and-wrong-length"

	c := checkByRuleID(EvaluateIdentityAndNetwork(in), "ID-001")
	if c == nil || c.Result != "FAIL" {
		t.Fatalf("ID-001 = %+v, want FAIL", c)
	}
}
