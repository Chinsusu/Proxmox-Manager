package template

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// factScript thu thập guest facts cần thiết để đánh giá canary check
// (Phần IV mục 8.2), chạy một lần qua QGA exec, output một dòng JSON
// để parse phía Go — tránh nhiều round-trip exec riêng lẻ.
const factScript = `
mid=$(cat /etc/machine-id 2>/dev/null || echo "")
ssh_keys=$(ls /etc/ssh/ssh_host_*_key.pub 2>/dev/null | wc -l)
ci_id=$(cat /var/lib/cloud/data/instance-id 2>/dev/null || echo "")
nic_count=$(ip -o link show | grep -v ' lo:' | wc -l)
def4=$(ip -4 route show default 2>/dev/null | wc -l)
def6=$(ip -6 route show default 2>/dev/null | wc -l)
qga=$(systemctl is-active qemu-guest-agent 2>/dev/null || echo inactive)
timesync=$(timedatectl show -p NTPSynchronized --value 2>/dev/null || echo "no")
printf '{"machine_id":"%s","ssh_host_key_count":%s,"cloud_init_instance_id":"%s","nic_count":%s,"default_route_v4_count":%s,"default_route_v6_count":%s,"qga_status":"%s","time_synchronized":"%s"}' \
  "$mid" "$ssh_keys" "$ci_id" "$nic_count" "$def4" "$def6" "$qga" "$timesync"
`

// CanaryFacts là guest facts thô thu thập từ một canary clone.
type CanaryFacts struct {
	MachineID           string `json:"machine_id"`
	SSHHostKeyCount     int    `json:"ssh_host_key_count"`
	CloudInitInstanceID string `json:"cloud_init_instance_id"`
	NICCount            int    `json:"nic_count"`
	DefaultRouteV4Count int    `json:"default_route_v4_count"`
	DefaultRouteV6Count int    `json:"default_route_v6_count"`
	QGAStatus           string `json:"qga_status"`
	TimeSynchronized    string `json:"time_synchronized"`
}

// CanaryOptions cấu hình một lần clone canary để validate template.
type CanaryOptions struct {
	Node       string
	SourceVMID int
	Storage    string
	Bridge     string
	Pool       string
	// BootTimeout là thời gian chờ tối đa QGA phản hồi sau khi start.
	BootTimeout time.Duration
}

// CanaryValidator clone một canary từ template, thu thập guest facts
// qua QGA exec, đánh giá theo Phần IV mục 8.2, rồi dọn canary — không
// để lại VM thừa trên cluster dù pass hay fail.
type CanaryValidator struct {
	adapter *proxmox.Adapter
}

// NewCanaryValidator tạo CanaryValidator gắn với một *proxmox.Adapter
// (đã cấu hình sẵn credential/cluster).
func NewCanaryValidator(adapter *proxmox.Adapter) *CanaryValidator {
	return &CanaryValidator{adapter: adapter}
}

// Validate chạy một canary clone đầy đủ vòng đời (clone → configure →
// start → chờ QGA → thu thập facts → dừng+xoá) và trả domain.ValidationRun
// type="template" theo Phần VI mục 2.9. Canary LUÔN được dọn (defer),
// kể cả khi các bước sau clone thất bại giữa chừng.
//
// Chỉ đánh giá được các check khả thi từ MỘT canary (Phần IV mục 8.2):
// machine-id đúng format, SSH host key đã sinh, cloud-init instance ID
// riêng, một NIC, một default route IPv4, không default route IPv6,
// QGA active, time sync active. Check "khác clone khác" (uniqueness
// xuyên nhiều lần clone) cần so sánh Facts giữa nhiều lần gọi Validate
// — evidence trả về đủ dữ liệu để caller tự so sánh, không cần một
// method riêng.
func (v *CanaryValidator) Validate(ctx context.Context, opts CanaryOptions) (*domain.ValidationRun, error) {
	run := &domain.ValidationRun{
		Type:           "template",
		RulesetVersion: "golden-template-canary-1.0",
		StartedAt:      time.Now(),
	}

	bootTimeout := opts.BootTimeout
	if bootTimeout <= 0 {
		bootTimeout = 2 * time.Minute
	}

	targetVMID, err := v.adapter.AllocateNextVMID(ctx)
	if err != nil {
		return nil, fmt.Errorf("template: canary allocate vmid: %w", err)
	}
	ref := proxmox.VMRef{Node: opts.Node, VMID: targetVMID}

	defer func() { //nolint:contextcheck // cleanup phai chay du ctx goc da bi huy/het han giua chung Validate
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if stopTask, err := v.adapter.Stop(cleanupCtx, ref); err == nil {
			_, _ = v.adapter.WaitForTask(cleanupCtx, stopTask, 30*time.Second)
		}
		if delTask, err := v.adapter.Delete(cleanupCtx, ref, true); err == nil {
			_, _ = v.adapter.WaitForTask(cleanupCtx, delTask, 30*time.Second)
		}
	}()

	cloneTask, err := v.adapter.Clone(ctx, proxmox.CloneRequest{
		SourceNode:  opts.Node,
		SourceVMID:  opts.SourceVMID,
		TargetNode:  opts.Node,
		TargetVMID:  targetVMID,
		Name:        fmt.Sprintf("vmf-canary-%d", targetVMID),
		Storage:     opts.Storage,
		Pool:        opts.Pool,
		FullClone:   true,
		Description: fmt.Sprintf("vmf.canary=1 vmf.source_vmid=%d vmf.vmid=%d", opts.SourceVMID, targetVMID),
	})
	if err != nil {
		return nil, fmt.Errorf("template: canary clone: %w", err)
	}
	if status, err := v.adapter.WaitForTask(ctx, cloneTask, 3*time.Minute); err != nil {
		return nil, fmt.Errorf("template: canary wait clone: %w", err)
	} else if !status.Success() {
		return nil, fmt.Errorf("template: canary clone task failed: %+v", status)
	}

	configTask, err := v.adapter.Configure(ctx, proxmox.ConfigureRequest{
		VMRef:     ref,
		Cores:     1,
		Sockets:   1,
		MemoryMB:  512,
		Agent:     true,
		OnBoot:    false,
		Net0:      proxmox.NetConfig{Bridge: opts.Bridge, Firewall: true},
		IPConfig0: "ip=dhcp",
	})
	if err != nil {
		return nil, fmt.Errorf("template: canary configure: %w", err)
	}
	if status, err := v.adapter.WaitForTask(ctx, configTask, 1*time.Minute); err != nil {
		return nil, fmt.Errorf("template: canary wait configure: %w", err)
	} else if !status.Success() {
		return nil, fmt.Errorf("template: canary configure task failed: %+v", status)
	}

	startTask, err := v.adapter.Start(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("template: canary start: %w", err)
	}
	if status, err := v.adapter.WaitForTask(ctx, startTask, 1*time.Minute); err != nil {
		return nil, fmt.Errorf("template: canary wait start: %w", err)
	} else if !status.Success() {
		return nil, fmt.Errorf("template: canary start task failed: %+v", status)
	}

	if err := waitGuestAgent(ctx, v.adapter, ref, bootTimeout); err != nil {
		return nil, fmt.Errorf("template: canary wait QGA: %w", err)
	}

	execResult, err := v.adapter.WaitExec(ctx, ref, []string{"bash", "-c", factScript}, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("template: canary collect facts: %w", err)
	}
	if execResult.ExitCode != 0 {
		return nil, fmt.Errorf("template: canary fact script exited %d: %s", execResult.ExitCode, execResult.Stderr)
	}

	var facts CanaryFacts
	if err := json.Unmarshal([]byte(execResult.Stdout), &facts); err != nil {
		return nil, fmt.Errorf("template: canary parse facts (stdout=%q): %w", execResult.Stdout, err)
	}

	checks := evaluateCanaryFacts(facts)
	finished := time.Now()
	run.FinishedAt = &finished
	run.Result = domain.ValidationPass
	for _, c := range checks {
		if c.Result != "PASS" {
			run.Result = domain.ValidationFail
			break
		}
	}

	evidence, err := json.Marshal(map[string]any{
		"canary_vmid": targetVMID,
		"facts":       facts,
		"checks":      checks,
	})
	if err != nil {
		return nil, fmt.Errorf("template: canary marshal evidence: %w", err)
	}
	run.Evidence = evidence

	return run, nil
}

// canaryCheck là một dòng evidence, khớp format ở Phần VIII mục 11
// (rule_id/expected/observed/result) rút gọn cho phạm vi template.
type canaryCheck struct {
	Name     string `json:"name"`
	Result   string `json:"result"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
}

// evaluateCanaryFacts đánh giá facts thu được theo Phần IV mục 8.2 —
// chỉ các check khả thi từ một canary đơn lẻ (xem doc comment Validate).
func evaluateCanaryFacts(f CanaryFacts) []canaryCheck {
	isHex32 := len(f.MachineID) == 32 && isLowerHex(f.MachineID)

	return []canaryCheck{
		{
			Name: "machine_id_format", Expected: "32 lowercase hex chars",
			Observed: f.MachineID, Result: passFail(isHex32),
		},
		{
			Name: "ssh_host_keys_generated", Expected: ">= 1",
			Observed: strconv.Itoa(f.SSHHostKeyCount), Result: passFail(f.SSHHostKeyCount >= 1),
		},
		{
			Name: "cloud_init_instance_id_present", Expected: "non-empty",
			Observed: f.CloudInitInstanceID, Result: passFail(f.CloudInitInstanceID != ""),
		},
		{
			Name: "single_nic", Expected: "1",
			Observed: strconv.Itoa(f.NICCount), Result: passFail(f.NICCount == 1),
		},
		{
			Name: "single_ipv4_default_route", Expected: "1",
			Observed: strconv.Itoa(f.DefaultRouteV4Count), Result: passFail(f.DefaultRouteV4Count == 1),
		},
		{
			Name: "no_ipv6_default_route", Expected: "0",
			Observed: strconv.Itoa(f.DefaultRouteV6Count), Result: passFail(f.DefaultRouteV6Count == 0),
		},
		{
			Name: "qga_active", Expected: "active",
			Observed: f.QGAStatus, Result: passFail(f.QGAStatus == "active"),
		},
		{
			Name: "time_synchronized", Expected: "yes",
			Observed: f.TimeSynchronized, Result: passFail(f.TimeSynchronized == "yes"),
		},
	}
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// waitGuestAgent poll GuestPing tới khi QGA phản hồi hoặc hết timeout
// — không dùng sleep cố định đơn (guardrail Phần II mục 18).
func waitGuestAgent(ctx context.Context, adapter *proxmox.Adapter, ref proxmox.VMRef, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = adapter.GuestPing(ctx, ref)
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("guest agent did not respond within timeout, last error: %w", lastErr)
}
