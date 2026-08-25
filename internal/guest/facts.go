package guest

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// Runner là tập con method của *proxmox.Adapter mà FactsCollector cần
// (QGA exec) — khai báo dạng interface để unit test tự giả lập output
// exec mà không cần HTTP client Proxmox thật.
type Runner interface {
	WaitExec(ctx context.Context, ref proxmox.VMRef, command []string, timeout time.Duration) (proxmox.ExecResult, error)
}

// factsScript thu thập guest facts P0 theo Phần VIII mục 3 trong MỘT
// lần exec (tránh nhiều round-trip poll riêng lẻ, cùng kỹ thuật với
// factScript của canary validator ở epic P0-06). Dùng `ip -j` để lấy
// JSON có cấu trúc cho MAC/IPv4/route thay vì parse text bằng awk —
// iproute2 trên golden template Ubuntu 22.04 hỗ trợ cờ này.
const factsScript = `
mid=$(cat /etc/machine-id 2>/dev/null || echo "")
bootid=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null || echo "")
host=$(hostname 2>/dev/null || echo "")
ci_id=$(cat /var/lib/cloud/data/instance-id 2>/dev/null || echo "")
ssh_fps=$(for k in /etc/ssh/ssh_host_*_key.pub; do [ -f "$k" ] && printf '%s:%s,' "$(basename "$k")" "$(ssh-keygen -lf "$k" 2>/dev/null | awk '{print $2}')"; done | sed 's/,$//' 2>/dev/null || echo "")
osrel=$( (. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME") || echo "")
kernel=$(uname -r 2>/dev/null || echo "")
nic_count=$(ip -o link show 2>/dev/null | grep -v ' lo:' | wc -l)
def4=$(ip -4 route show default 2>/dev/null | wc -l)
def6=$(ip -6 route show default 2>/dev/null | wc -l)
link_json=$(ip -j link show 2>/dev/null || echo '[]')
addr_json=$(ip -j addr show 2>/dev/null || echo '[]')
route4_json=$(ip -4 -j route show default 2>/dev/null || echo '[]')
printf '{"machine_id":"%s","boot_id":"%s","hostname":"%s","cloud_init_instance_id":"%s","ssh_host_key_fingerprints":"%s","os_release":"%s","kernel_version":"%s","nic_count":%s,"default_route_v4_count":%s,"default_route_v6_count":%s,"link_json":%s,"addr_json":%s,"route4_json":%s}' \
  "$mid" "$bootid" "$host" "$ci_id" "$ssh_fps" "$osrel" "$kernel" "$nic_count" "$def4" "$def6" "$link_json" "$addr_json" "$route4_json"
`

// Facts là kết quả thu thập đã chuẩn hoá, dùng làm input cho rule
// engine identity/network (internal/validation) — khớp danh sách "Guest
// facts P0" ở Phần VIII mục 3.
type Facts struct {
	MachineID           string
	BootID              string
	Hostname            string
	CloudInitInstanceID string
	// SSHHostKeyFingerprints ánh xạ tên file khoá (vd
	// "ssh_host_ed25519_key.pub") sang fingerprint — nhiều key type
	// cùng tồn tại (rsa/ecdsa/ed25519), caller tự chọn canonical
	// (CanonicalSSHFingerprint ưu tiên ed25519) cho ID-003.
	SSHHostKeyFingerprints map[string]string
	MACAddresses           []string
	IPv4Addresses          []string
	// GlobalIPv6Addresses chỉ gồm địa chỉ IPv6 global unicast — loại
	// link-local (fe80::/10) và loopback, dùng cho NET-005 (Phần VIII
	// mục 5: "absent khi policy deny").
	GlobalIPv6Addresses []string
	OSRelease           string
	KernelVersion       string
	NICCount            int
	DefaultRouteV4Count int
	DefaultRouteV6Count int
	DefaultGatewayV4    string
	CollectedAt         time.Time
}

// sshKeyPreference là thứ tự ưu tiên chọn fingerprint canonical khi có
// nhiều host key — ed25519 trước (khuyến nghị hiện đại), rồi ecdsa,
// cuối cùng rsa (Ubuntu 22.04 mặc định sinh cả ba qua ssh-keygen -A).
var sshKeyPreference = []string{"ssh_host_ed25519_key.pub", "ssh_host_ecdsa_key.pub", "ssh_host_rsa_key.pub"}

// CanonicalSSHFingerprint chọn một fingerprint đại diện cho ID-003 SSH
// host key uniqueness — ưu tiên theo sshKeyPreference; nếu không có key
// type nào trong danh sách ưu tiên (setup lạ, key type khác) thì lấy
// đại một fingerprint bất kỳ còn hơn bỏ trống — chấp nhận không xác
// định key nào được chọn trong trường hợp hiếm này. Trả "" nếu guest
// không có SSH host key nào (chưa boot xong / lỗi collector).
func (f Facts) CanonicalSSHFingerprint() string {
	for _, key := range sshKeyPreference {
		if fp, ok := f.SSHHostKeyFingerprints[key]; ok && fp != "" {
			return fp
		}
	}
	for _, fp := range f.SSHHostKeyFingerprints {
		if fp != "" {
			return fp
		}
	}
	return ""
}

// FactsCollector chạy factsScript qua QGA exec và chuẩn hoá kết quả
// thành Facts.
type FactsCollector struct {
	runner Runner
}

// NewFactsCollector tạo FactsCollector gắn với một Runner (thường là
// *proxmox.Adapter đã cấu hình credential/cluster).
func NewFactsCollector(runner Runner) *FactsCollector {
	return &FactsCollector{runner: runner}
}

// Collect chạy factsScript trong guest qua ref, trả Facts đã
// chuẩn hoá. Lỗi exec hoặc script thoát khác 0 trả về error — caller
// (state engine ValidatingIdentityHandler) quyết định UNKNOWN theo
// Phần VIII mục 8 khi collector không thu thập được.
func (c *FactsCollector) Collect(ctx context.Context, ref proxmox.VMRef, timeout time.Duration) (Facts, error) {
	result, err := c.runner.WaitExec(ctx, ref, []string{"bash", "-c", factsScript}, timeout)
	if err != nil {
		return Facts{}, fmt.Errorf("guest: collect facts exec: %w", err)
	}
	if result.ExitCode != 0 {
		return Facts{}, fmt.Errorf("guest: facts script exited %d: stderr=%s", result.ExitCode, result.Stderr)
	}

	var raw rawFacts
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return Facts{}, fmt.Errorf("guest: parse facts (stdout=%q): %w", result.Stdout, err)
	}
	facts := normalizeFacts(raw)
	facts.CollectedAt = time.Now()
	return facts, nil
}

// rawFacts khớp 1:1 output JSON của factsScript trước khi chuẩn hoá.
type rawFacts struct {
	MachineID              string          `json:"machine_id"`
	BootID                 string          `json:"boot_id"`
	Hostname               string          `json:"hostname"`
	CloudInitInstanceID    string          `json:"cloud_init_instance_id"`
	SSHHostKeyFingerprints string          `json:"ssh_host_key_fingerprints"`
	OSRelease              string          `json:"os_release"`
	KernelVersion          string          `json:"kernel_version"`
	NICCount               int             `json:"nic_count"`
	DefaultRouteV4Count    int             `json:"default_route_v4_count"`
	DefaultRouteV6Count    int             `json:"default_route_v6_count"`
	LinkJSON               json.RawMessage `json:"link_json"`
	AddrJSON               json.RawMessage `json:"addr_json"`
	Route4JSON             json.RawMessage `json:"route4_json"`
}

// ipLinkEntry là một phần tử output của `ip -j link show`.
type ipLinkEntry struct {
	IfName  string `json:"ifname"`
	Address string `json:"address"`
}

// ipAddrInfoEntry là một phần tử addr_info bên trong output `ip -j addr show`.
type ipAddrInfoEntry struct {
	Family string `json:"family"`
	Local  string `json:"local"`
}

// ipAddrEntry là một phần tử output của `ip -j addr show`.
type ipAddrEntry struct {
	IfName   string            `json:"ifname"`
	AddrInfo []ipAddrInfoEntry `json:"addr_info"`
}

// ipRouteEntry là một phần tử output của `ip -j route show default`.
type ipRouteEntry struct {
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
}

// normalizeFacts chuyển rawFacts (bao gồm 3 khối JSON lồng từ `ip -j`)
// thành Facts phẳng. Lỗi parse JSON lồng bị bỏ qua có chủ đích —
// để lại field rỗng thay vì fail toàn bộ collect chỉ vì một khối phụ
// (vd route4_json rỗng khi guest chưa có default route) hỏng định dạng.
func normalizeFacts(raw rawFacts) Facts {
	var links []ipLinkEntry
	_ = json.Unmarshal(raw.LinkJSON, &links)
	var addrs []ipAddrEntry
	_ = json.Unmarshal(raw.AddrJSON, &addrs)
	var routes4 []ipRouteEntry
	_ = json.Unmarshal(raw.Route4JSON, &routes4)

	var macs []string
	for _, l := range links {
		if l.IfName == "lo" || l.Address == "" {
			continue
		}
		macs = append(macs, l.Address)
	}

	var ipv4s []string
	var globalIPv6s []string
	for _, a := range addrs {
		if a.IfName == "lo" {
			continue
		}
		for _, info := range a.AddrInfo {
			switch info.Family {
			case "inet":
				ipv4s = append(ipv4s, info.Local)
			case "inet6":
				if ip := net.ParseIP(info.Local); ip != nil && !ip.IsLinkLocalUnicast() && !ip.IsLoopback() {
					globalIPv6s = append(globalIPv6s, info.Local)
				}
			}
		}
	}

	var gateway string
	if len(routes4) > 0 {
		gateway = routes4[0].Gateway
	}

	var fingerprints map[string]string
	if raw.SSHHostKeyFingerprints != "" {
		fingerprints = make(map[string]string)
		for _, entry := range strings.Split(raw.SSHHostKeyFingerprints, ",") {
			keyName, fp, ok := strings.Cut(entry, ":")
			if !ok || keyName == "" || fp == "" {
				continue
			}
			fingerprints[keyName] = fp
		}
	}

	return Facts{
		MachineID:              raw.MachineID,
		BootID:                 raw.BootID,
		Hostname:               raw.Hostname,
		CloudInitInstanceID:    raw.CloudInitInstanceID,
		SSHHostKeyFingerprints: fingerprints,
		MACAddresses:           macs,
		IPv4Addresses:          ipv4s,
		GlobalIPv6Addresses:    globalIPv6s,
		OSRelease:              raw.OSRelease,
		KernelVersion:          raw.KernelVersion,
		NICCount:               raw.NICCount,
		DefaultRouteV4Count:    raw.DefaultRouteV4Count,
		DefaultRouteV6Count:    raw.DefaultRouteV6Count,
		DefaultGatewayV4:       gateway,
	}
}
