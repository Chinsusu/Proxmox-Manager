package template

import "testing"

func TestIsLowerHex(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"6a16a6cb447d4b66a507390199fc5b8", true},
		{"6A16A6CB447D4B66A507390199FC5B8", false}, // uppercase khong hop le
		{"6a16a6cb447d4b66a507390199fc5bg", false}, // ky tu ngoai hex
		{"", true}, // chuoi rong khong co ky tu sai, do dai kiem tra rieng o isHex32
	}
	for _, c := range cases {
		if got := isLowerHex(c.in); got != c.want {
			t.Errorf("isLowerHex(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestEvaluateCanaryFacts_AllPass(t *testing.T) {
	facts := CanaryFacts{
		MachineID:           "120f5c274cba31ee5addc8d86c929211", // dung 32 hex char
		SSHHostKeyCount:     4,
		CloudInitInstanceID: "iid-canary-1",
		NICCount:            1,
		DefaultRouteV4Count: 1,
		DefaultRouteV6Count: 0,
		QGAStatus:           "active",
		TimeSynchronized:    "yes",
	}

	checks := evaluateCanaryFacts(facts)
	for _, c := range checks {
		if c.Result != "PASS" {
			t.Errorf("check %s = %s, want PASS (observed=%s expected=%s)", c.Name, c.Result, c.Observed, c.Expected)
		}
	}
}

func TestEvaluateCanaryFacts_DetectsNonCompliantTemplate(t *testing.T) {
	// Facts thuc te thu duoc khi verify tren cluster that voi template
	// chua generalize dung chuan (xem PR description P0-06): machine-id
	// dung format nhung SSH host key van con nguyen tu template (khong
	// bi xoa/tao lai) - o day mo phong truong hop nic_count/route sai
	// de kiem tra check phat hien duoc loi that.
	facts := CanaryFacts{
		MachineID:           "not-32-hex-chars",
		SSHHostKeyCount:     0,
		CloudInitInstanceID: "",
		NICCount:            2,
		DefaultRouteV4Count: 0,
		DefaultRouteV6Count: 1,
		QGAStatus:           "inactive",
		TimeSynchronized:    "no",
	}

	checks := evaluateCanaryFacts(facts)
	failCount := 0
	for _, c := range checks {
		if c.Result == "FAIL" {
			failCount++
		}
	}
	if failCount != len(checks) {
		t.Fatalf("expected all %d checks to FAIL for non-compliant facts, got %d FAIL", len(checks), failCount)
	}
}
