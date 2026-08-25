package validation

import (
	"strconv"
	"time"

	"github.com/Chinsusu/vm-factory/internal/pgw"
)

// EgressInput là dữ liệu đầu vào cho EGR-xxx rules (Phần VIII mục 6,
// transition 4.9 ở Phần V). Evidence đến từ pgw.Adapter.EgressProof —
// với pgw.NoopAdapter (chưa có PGW thật, epic P0-04 chưa triển khai)
// mọi giá trị đều đánh dấu SIMULATED nên các rule dưới đây sẽ hợp lệ
// trả FAIL (không rubber-stamp evidence giả), đúng như kỳ vọng.
type EgressInput struct {
	Evidence          pgw.EgressEvidence
	ExpectedMappingID string
	DesiredGeneration int64
	DenyIPv6          bool
	// ProofMaxAge là tuổi tối đa cho phép của proof (EGR-007 "within
	// configured age", Phần VIII mục 6).
	ProofMaxAge time.Duration
	// Now cho phép test tiêm thời điểm cố định; để zero-value thì dùng
	// time.Now().
	Now time.Time
}

// EvaluateEgress chạy EGR-001..007 (Phần VIII mục 6).
func EvaluateEgress(in EgressInput) []Check {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	checks := make([]Check, 0, 7)

	// EGR-001 mapping active: đúng mapping, proof PASS.
	mappingOK := in.Evidence.Result == "PASS" && in.Evidence.MappingID == in.ExpectedMappingID
	checks = append(checks, newCheck("EGR-001", SeverityBlock,
		"PASS, mapping_id="+in.ExpectedMappingID, in.Evidence.Result+", mapping_id="+in.Evidence.MappingID, mappingOK, now))

	// EGR-002 generation applied: desired == applied.
	genOK := in.Evidence.RulesGeneration == in.DesiredGeneration
	checks = append(checks, newCheck("EGR-002", SeverityBlock,
		strconv.FormatInt(in.DesiredGeneration, 10), strconv.FormatInt(in.Evidence.RulesGeneration, 10), genOK, now))

	// EGR-003 IPv4 exit: matches proof/policy — chỉ xác nhận có exit IP
	// trong proof; so khớp với một registry "expected exit IP theo
	// policy" cụ thể là gap đã biết (registry đó chưa tồn tại, PGW
	// integration thật thuộc P0-04).
	ipv4OK := in.Evidence.IPv4 != ""
	checks = append(checks, newCheck("EGR-003", SeverityBlock,
		"non-empty exit IP (so khớp policy cụ thể là gap đã biết)", in.Evidence.IPv4, ipv4OK, now))

	// EGR-004 IPv6: blocked khi policy deny — theo contract Phần VII
	// mục 5, proof đánh dấu "BLOCKED" (chuỗi, không phải địa chỉ) khi
	// IPv6 bị chặn đúng policy.
	v6OK := !in.DenyIPv6 || in.Evidence.IPv6 == "BLOCKED"
	checks = append(checks, newCheck("EGR-004", SeverityBlock,
		"BLOCKED (policy deny)", in.Evidence.IPv6, v6OK, now))

	// EGR-005 direct leak counter: zero.
	leakOK := in.Evidence.DirectLeakPackets == 0
	checks = append(checks, newCheck("EGR-005", SeverityBlock,
		"0", strconv.Itoa(in.Evidence.DirectLeakPackets), leakOK, now))

	// EGR-006 proxy health: ACTIVE — spec liệt kê severity "BLOCK/WARN
	// theo policy threshold"; dự án chưa có config knob cho ngưỡng đó
	// nên mặc định BLOCK (proxy không ACTIVE là lỗi chức năng thật sự,
	// không phải cảnh báo mềm như ID-006 cloud-init).
	healthOK := in.Evidence.ProxyHealth == "ACTIVE"
	checks = append(checks, newCheck("EGR-006", SeverityBlock,
		"ACTIVE", in.Evidence.ProxyHealth, healthOK, now))

	// EGR-007 proof freshness: within configured age.
	fresh, observed := evaluateProofFreshness(in.Evidence.CheckedAt, now, in.ProofMaxAge)
	checks = append(checks, newCheck("EGR-007", SeverityBlock,
		"within "+in.ProofMaxAge.String(), observed, fresh, now))

	return checks
}

// evaluateProofFreshness parse checked_at theo RFC3339 (contract Phần
// VII mục 5 không chốt format cụ thể — RFC3339 là chuẩn timestamp
// JSON phổ biến nhất, dùng làm giả định tường minh, verify lại khi có
// PGW thật ở P0-04). Parse lỗi hoặc tuổi âm/vượt maxAge đều FAIL —
// không coi "không parse được" là PASS.
func evaluateProofFreshness(checkedAt string, now time.Time, maxAge time.Duration) (fresh bool, observed string) {
	t, err := time.Parse(time.RFC3339, checkedAt)
	if err != nil {
		return false, "unparseable checked_at: " + checkedAt
	}
	age := now.Sub(t)
	return age >= 0 && age <= maxAge, "age=" + age.String()
}
