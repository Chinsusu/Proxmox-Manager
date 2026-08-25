package validation

import (
	"strconv"
	"strings"
	"time"

	"github.com/Chinsusu/vm-factory/internal/guest"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// IdentityInput là dữ liệu đầu vào cho ID-xxx/NET-xxx rules (Phần VIII
// mục 4-5, transition 4.8 ở Phần V).
type IdentityInput struct {
	Facts guest.Facts

	// MachineIDDigest là HMAC digest đã tính sẵn (IdentityDigester.Digest)
	// của Facts.MachineID — truyền vào thay vì tự tính lại để caller
	// kiểm soát key một chỗ duy nhất.
	MachineIDDigest          string
	MachineIDDuplicates      []storage.DuplicateMatch
	SSHFingerprintDuplicates []storage.DuplicateMatch
	// BlockRetiredDuplicate khớp
	// config.Identity.DuplicatePolicy.{ActiveFleet,RetiredHistory} —
	// active fleet LUÔN hard block theo Phần VIII mục 10 nên không cần
	// cờ riêng; field này chỉ chi phối duplicate với instance đã RETIRED.
	BlockRetiredDuplicate bool

	ExpectedHostname     string
	ExpectedMACAddresses []string
	ExpectedIPv4         string
	ExpectedGatewayV4    string

	RequireSingleNIC          bool
	RequireSingleDefaultRoute bool
	DenyIPv6DefaultRoute      bool
}

// EvaluateIdentityAndNetwork chạy ID-001,002,003,004,005,006,008 và
// NET-001,002,003,004,005,009 (Phần VIII mục 4-5, transition 4.8).
//
// Rule CHƯA implement (gap đã biết, không fake PASS):
//   - ID-007 stale application state: cần state-path denylist từ
//     Workload Adapter (Phần VII mục 7 workload rules), thuộc P0-08
//     chưa triển khai.
//   - NET-006 DNS resolver policy: cần expected resolver config, chưa
//     có knob cấu hình và chưa thu thập resolver facts.
//   - NET-007 unexpected tunnel interface: cần phân loại interface
//     type (tun/tap/wireguard...), FactsCollector hiện chưa thu thập.
//   - NET-008 PGW client identity (IP/MAC/VLAN match): cần API tra cứu
//     client đã đăng ký ở PGW, pgw.Adapter hiện không có method này —
//     đợi epic P0-04 triển khai PGW client thật.
func EvaluateIdentityAndNetwork(in IdentityInput) []Check {
	now := in.Facts.CollectedAt
	if now.IsZero() {
		now = time.Now()
	}
	checks := make([]Check, 0, 15)

	// ID-001 machine-id format: 32 lowercase hex. Observed KHÔNG được là
	// raw machine-id (Phần III mục 3: "Raw machine-id chỉ tồn tại trong
	// memory collector", redaction rule Phần IX mục 2, test case
	// "evidence redaction" ở Phần VIII mục 13) — evidence này bị persist
	// vào validation_runs.evidence, dùng digest (đã an toàn để lưu) thay.
	isHex32 := len(in.Facts.MachineID) == 32 && isLowerHex(in.Facts.MachineID)
	checks = append(checks, newCheck("ID-001", SeverityBlock,
		"32 lowercase hex chars", in.MachineIDDigest, isHex32, now))

	// ID-002 machine-id uniqueness: không match active fleet, và (theo
	// policy) không match lịch sử retired.
	checks = append(checks, newCheck("ID-002", SeverityBlock,
		"unique", in.MachineIDDigest, !hasBlockingDuplicate(in.MachineIDDuplicates, in.BlockRetiredDuplicate), now))

	// ID-003 SSH host key uniqueness: cùng cơ chế ID-002 nhưng theo
	// ssh_host_fingerprint (Phần VIII mục 10 áp dụng chung).
	canonicalFP := in.Facts.CanonicalSSHFingerprint()
	sshUnique := canonicalFP != "" && !hasBlockingDuplicate(in.SSHFingerprintDuplicates, in.BlockRetiredDuplicate)
	checks = append(checks, newCheck("ID-003", SeverityBlock,
		"fingerprint mới, chưa từng thấy ở instance khác", canonicalFP, sshUnique, now))

	// ID-004 hostname match: đúng inventory.
	checks = append(checks, newCheck("ID-004", SeverityBlock,
		in.ExpectedHostname, in.Facts.Hostname, in.Facts.Hostname == in.ExpectedHostname, now))

	// ID-005 MAC match: mọi MAC kỳ vọng (từ Proxmox/PGW checkpoint)
	// phải nằm trong MAC guest quan sát được. So sánh không phân biệt
	// hoa/thường — verify thật trên cluster cho thấy Proxmox config trả
	// MAC uppercase còn guest (`ip -j link show`) báo lowercase; nguồn
	// đã chuẩn hoá ở proxmox.parseNet0MAC, so sánh ở đây chuẩn hoá lại
	// lần nữa để không phụ thuộc một nguồn duy nhất nhớ đúng quy ước.
	macMatch := len(in.ExpectedMACAddresses) > 0 && containsAll(lowerAll(in.Facts.MACAddresses), lowerAll(in.ExpectedMACAddresses))
	checks = append(checks, newCheck("ID-005", SeverityBlock,
		strings.Join(in.ExpectedMACAddresses, ","), strings.Join(in.Facts.MACAddresses, ","), macMatch, now))

	// ID-006 cloud-init instance ID: severity WARN/BLOCK theo policy —
	// dự án chưa có knob cấu hình severity riêng cho rule này (chỉ có
	// DuplicatePolicy cho ID-002), mặc định WARN (an toàn hơn BLOCK khi
	// chưa chắc chắn). Chỉ xác nhận non-empty — so sánh "đúng
	// generation" cần một giá trị cloud-init ID kỳ vọng mà state engine
	// hiện chưa lưu ở bất kỳ checkpoint nào (gap đã biết).
	checks = append(checks, newCheck("ID-006", SeverityWarn,
		"non-empty (so khớp generation là gap đã biết)", in.Facts.CloudInitInstanceID, in.Facts.CloudInitInstanceID != "", now))

	// ID-008 boot ID present: valid UUID (36 ký tự, có dấu gạch ngang).
	checks = append(checks, newCheck("ID-008", SeverityWarn,
		"valid UUID", in.Facts.BootID, isUUIDLike(in.Facts.BootID), now))

	// NET-001 NIC count: đúng một workload NIC.
	nicOK := !in.RequireSingleNIC || in.Facts.NICCount == 1
	checks = append(checks, newCheck("NET-001", SeverityBlock,
		"1", strconv.Itoa(in.Facts.NICCount), nicOK, now))

	// NET-002 IPv4 address: đúng allocated IP.
	ipMatch := in.ExpectedIPv4 != "" && containsAll(in.Facts.IPv4Addresses, []string{in.ExpectedIPv4})
	checks = append(checks, newCheck("NET-002", SeverityBlock,
		in.ExpectedIPv4, strings.Join(in.Facts.IPv4Addresses, ","), ipMatch, now))

	// NET-003 default route: duy nhất qua expected gateway.
	routeOK := !in.RequireSingleDefaultRoute ||
		(in.Facts.DefaultRouteV4Count == 1 && in.Facts.DefaultGatewayV4 == in.ExpectedGatewayV4)
	checks = append(checks, newCheck("NET-003", SeverityBlock,
		"1 route qua "+in.ExpectedGatewayV4,
		strconv.Itoa(in.Facts.DefaultRouteV4Count)+" route, gateway="+in.Facts.DefaultGatewayV4, routeOK, now))

	// NET-004 IPv6 default route: absent khi policy deny.
	v6RouteOK := !in.DenyIPv6DefaultRoute || in.Facts.DefaultRouteV6Count == 0
	checks = append(checks, newCheck("NET-004", SeverityBlock,
		"0 (policy deny)", strconv.Itoa(in.Facts.DefaultRouteV6Count), v6RouteOK, now))

	// NET-005 global IPv6: absent khi policy deny.
	v6AddrOK := !in.DenyIPv6DefaultRoute || len(in.Facts.GlobalIPv6Addresses) == 0
	checks = append(checks, newCheck("NET-005", SeverityBlock,
		"none (policy deny)", strings.Join(in.Facts.GlobalIPv6Addresses, ","), v6AddrOK, now))

	// NET-009 second route/NIC: absent — kiểm tra riêng trường hợp ">1"
	// (khác NET-001/003 vốn kiểm tra "đúng 1"; ở đây bắt cụ thể tín
	// hiệu có NIC/route THỨ HAI xuất hiện, phòng khi RequireSingleNIC/
	// RequireSingleDefaultRoute tắt nhưng vẫn cần chặn bất thường rõ).
	noSecond := in.Facts.NICCount <= 1 && in.Facts.DefaultRouteV4Count <= 1
	checks = append(checks, newCheck("NET-009", SeverityBlock,
		"absent", strconv.Itoa(in.Facts.NICCount)+" nic, "+strconv.Itoa(in.Facts.DefaultRouteV4Count)+" v4 route", noSecond, now))

	return checks
}

// hasBlockingDuplicate quyết định một danh sách DuplicateMatch có nên
// chặn hay không: bất kỳ match active-fleet nào (Retired=false) luôn
// chặn; match retired chỉ chặn nếu blockRetired=true (Phần VIII mục 10).
func hasBlockingDuplicate(matches []storage.DuplicateMatch, blockRetired bool) bool {
	for _, m := range matches {
		if !m.Retired || blockRetired {
			return true
		}
	}
	return false
}

// containsAll báo hiệu mọi phần tử want đều có mặt trong have.
func containsAll(have, want []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, h := range have {
		set[h] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

// lowerAll trả bản sao ss với mọi phần tử lowercase — dùng chuẩn hoá
// MAC trước khi so sánh (ID-005), xem comment tại nơi gọi.
func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}

// isUUIDLike kiểm tra format UUID cơ bản (36 ký tự, dấu gạch ngang
// đúng vị trí) — không cần parse chuẩn RFC 4122 đầy đủ, boot_id chỉ
// cần valid-looking theo ID-008.
func isUUIDLike(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}
