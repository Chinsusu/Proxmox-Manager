package validation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/guest"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// DriftClassification theo Phần VIII mục 12.
type DriftClassification string

// Các giá trị hợp lệ của DriftClassification. DriftBenign không xuất
// hiện trong DriftFinding — không có phát hiện gì mới là trường hợp
// "benign", biểu diễn bằng danh sách findings rỗng thay vì một giá trị
// classification riêng.
const (
	DriftRepairable       DriftClassification = "repairable_drift"
	DriftQuarantineWorthy DriftClassification = "quarantine_worthy_drift"
)

// DriftFinding là một bất thường phát hiện được khi so sánh guest facts
// mới thu thập với kỳ vọng/lịch sử của một instance READY (Phần VIII
// mục 12).
type DriftFinding struct {
	Category       string              `json:"category"`
	Classification DriftClassification `json:"classification"`
	Expected       string              `json:"expected"`
	Observed       string              `json:"observed"`
}

// DriftScannerInput là dữ liệu một lần quét drift cho MỘT instance
// READY (Phần VIII mục 12). VM Factory chưa có bảng egress_bindings
// được populate (gap đã biết từ P0-05, xem rollback.go) nên PGW
// binding/proof KHÔNG quét được ở đây — thiếu registry tra "PGWClientID
// của instance này". Proxmox VM config hash và workload health cũng
// chưa quét được — config hash canonicalized (Phần III mục 6.1) và
// Workload Adapter (P0-08) đều chưa triển khai. Chỉ quét được: identity
// digest stability + NIC/route facts (qua rule engine ID/NET đã có).
type DriftScannerInput struct {
	InstanceID string
	Facts      *guest.FactsCollector
	Digester   *IdentityDigester
	Identity   *storage.IdentityRepository

	VMRef                     proxmox.VMRef
	ExpectedHostname          string
	ExpectedMACAddresses      []string
	ExpectedIPv4              string
	ExpectedGatewayV4         string
	RequireSingleNIC          bool
	RequireSingleDefaultRoute bool
	DenyIPv6DefaultRoute      bool

	FactsTimeout time.Duration
}

// ScanInstance thu thập guest facts hiện tại của một instance READY, so
// sánh với lần quan sát gần nhất (identity digest stability) và với
// policy hiện hành (NIC/route facts qua EvaluateIdentityAndNetwork),
// trả về danh sách DriftFinding cùng GuestFacts/digest vừa thu thập —
// caller (worker loop định kỳ, chưa xây dựng) chịu trách nhiệm persist
// IdentityObservation/ValidationRun mới, giữ nguyên tắc "package
// validation không tự chạm DB ghi" đã áp dụng cho rule engine.
//
// Instance READY lần đầu được quét (chưa từng có quan sát trước) không
// phải lỗi — bỏ qua phần digest stability, chỉ còn NIC/route facts so
// với policy.
func ScanInstance(ctx context.Context, in DriftScannerInput) ([]DriftFinding, guest.Facts, string, error) {
	timeout := in.FactsTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	facts, err := in.Facts.Collect(ctx, in.VMRef, timeout)
	if err != nil {
		return nil, guest.Facts{}, "", fmt.Errorf("validation: drift collect facts: %w", err)
	}
	digest := in.Digester.Digest(facts.MachineID)

	var findings []DriftFinding

	stability, err := compareDigestStability(ctx, in.Identity, in.InstanceID, digest)
	if err != nil {
		return nil, guest.Facts{}, "", err
	}
	if stability != nil {
		findings = append(findings, *stability)
	}

	checks := EvaluateIdentityAndNetwork(IdentityInput{
		Facts:                     facts,
		MachineIDDigest:           digest,
		ExpectedHostname:          in.ExpectedHostname,
		ExpectedMACAddresses:      in.ExpectedMACAddresses,
		ExpectedIPv4:              in.ExpectedIPv4,
		ExpectedGatewayV4:         in.ExpectedGatewayV4,
		RequireSingleNIC:          in.RequireSingleNIC,
		RequireSingleDefaultRoute: in.RequireSingleDefaultRoute,
		DenyIPv6DefaultRoute:      in.DenyIPv6DefaultRoute,
	})
	findings = append(findings, classifyFailedChecks(checks)...)

	return findings, facts, digest, nil
}

// classifyFailedChecks chuyển các Check FAIL thành DriftFinding — BLOCK
// severity luôn quarantine-worthy (rule đó vốn là điều kiện cứng cho
// READY), WARN severity chỉ repairable. Hàm thuần, không side effect,
// tách riêng để unit test không cần DB/network (khác ScanInstance vốn
// gọi FactsCollector.Collect + storage.IdentityRepository thật).
func classifyFailedChecks(checks []Check) []DriftFinding {
	var findings []DriftFinding
	for _, c := range checks {
		if c.Result == "PASS" {
			continue
		}
		classification := DriftRepairable
		if c.Severity == SeverityBlock {
			classification = DriftQuarantineWorthy
		}
		findings = append(findings, DriftFinding{
			Category:       c.RuleID,
			Classification: classification,
			Expected:       c.Expected,
			Observed:       c.Observed,
		})
	}
	return findings
}

// compareDigestStability so sánh digest vừa thu thập với quan sát gần
// nhất đã lưu của CÙNG instance — digest đổi trên một instance READY
// (không phải rebuild generation mới, vốn tạo instance ID khác) là dấu
// hiệu identity bị thay đổi sau khi đã qua validation, luôn
// quarantine-worthy (Phần VIII mục 12: "identity digest stability").
func compareDigestStability(ctx context.Context, identity *storage.IdentityRepository, instanceID, freshDigest string) (*DriftFinding, error) {
	latest, err := identity.LatestByInstance(ctx, instanceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("validation: drift load latest identity observation: %w", err)
	}
	if latest.MachineIDDigest == freshDigest {
		return nil, nil
	}
	return &DriftFinding{
		Category:       "identity_digest_stability",
		Classification: DriftQuarantineWorthy,
		Expected:       latest.MachineIDDigest,
		Observed:       freshDigest,
	}, nil
}
