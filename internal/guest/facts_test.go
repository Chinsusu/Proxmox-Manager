package guest

import (
	"context"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// fakeRunner giả lập QGA exec — trả stdout cố định bất kể command, đủ
// để unit test logic parse của FactsCollector mà không cần Proxmox
// thật (verify script thật trên cluster thật thuộc task riêng, xem
// engine_integration_test.go cho pattern tương đương).
type fakeRunner struct {
	result proxmox.ExecResult
	err    error
}

func (f *fakeRunner) WaitExec(_ context.Context, _ proxmox.VMRef, _ []string, _ time.Duration) (proxmox.ExecResult, error) {
	return f.result, f.err
}

func TestFactsCollector_Collect_ParsesNestedIPJSON(t *testing.T) {
	stdout := `{"machine_id":"0123456789abcdef0123456789abcdef","boot_id":"a1b2c3d4-0000-0000-0000-000000000000","hostname":"vmf-ins-abc123","cloud_init_instance_id":"iid-vmf-1","ssh_host_key_fingerprints":"ssh_host_ecdsa_key.pub:SHA256:aaa,ssh_host_ed25519_key.pub:SHA256:bbb,ssh_host_rsa_key.pub:SHA256:ccc","os_release":"Ubuntu 22.04.4 LTS","kernel_version":"5.15.0-91-generic","nic_count":1,"default_route_v4_count":1,"default_route_v6_count":0,"link_json":[{"ifname":"lo","address":"00:00:00:00:00:00"},{"ifname":"eth0","address":"bc:24:11:aa:bb:cc"}],"addr_json":[{"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1"}]},{"ifname":"eth0","addr_info":[{"family":"inet","local":"10.98.0.15"},{"family":"inet6","local":"fe80::1"},{"family":"inet6","local":"2001:db8::5"}]}],"route4_json":[{"dst":"default","gateway":"10.98.0.1","dev":"eth0"}]}`

	runner := &fakeRunner{result: proxmox.ExecResult{Exited: true, ExitCode: 0, Stdout: stdout}}
	c := NewFactsCollector(runner)

	facts, err := c.Collect(context.Background(), proxmox.VMRef{Node: "n1", VMID: 123}, 5*time.Second)
	if err != nil {
		t.Fatalf("Collect() error: %v", err)
	}

	if facts.MachineID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("MachineID = %q", facts.MachineID)
	}
	if facts.Hostname != "vmf-ins-abc123" {
		t.Errorf("Hostname = %q", facts.Hostname)
	}
	if len(facts.SSHHostKeyFingerprints) != 3 || facts.SSHHostKeyFingerprints["ssh_host_ed25519_key.pub"] != "SHA256:bbb" {
		t.Errorf("SSHHostKeyFingerprints = %v", facts.SSHHostKeyFingerprints)
	}
	if got := facts.CanonicalSSHFingerprint(); got != "SHA256:bbb" {
		t.Errorf("CanonicalSSHFingerprint() = %q, want SHA256:bbb (ed25519 uu tien)", got)
	}
	if len(facts.MACAddresses) != 1 || facts.MACAddresses[0] != "bc:24:11:aa:bb:cc" {
		t.Errorf("MACAddresses = %v, want [bc:24:11:aa:bb:cc] (lo phai bi loai)", facts.MACAddresses)
	}
	if len(facts.IPv4Addresses) != 1 || facts.IPv4Addresses[0] != "10.98.0.15" {
		t.Errorf("IPv4Addresses = %v, want [10.98.0.15] (lo va inet6 phai bi loai)", facts.IPv4Addresses)
	}
	if len(facts.GlobalIPv6Addresses) != 1 || facts.GlobalIPv6Addresses[0] != "2001:db8::5" {
		t.Errorf("GlobalIPv6Addresses = %v, want [2001:db8::5] (fe80::1 link-local phai bi loai)", facts.GlobalIPv6Addresses)
	}
	if facts.DefaultGatewayV4 != "10.98.0.1" {
		t.Errorf("DefaultGatewayV4 = %q", facts.DefaultGatewayV4)
	}
	if facts.NICCount != 1 || facts.DefaultRouteV4Count != 1 || facts.DefaultRouteV6Count != 0 {
		t.Errorf("counts = nic=%d v4=%d v6=%d", facts.NICCount, facts.DefaultRouteV4Count, facts.DefaultRouteV6Count)
	}
	if facts.CollectedAt.IsZero() {
		t.Error("CollectedAt should be set")
	}
}

func TestFactsCollector_Collect_EmptyRouteAndSSHFingerprints(t *testing.T) {
	stdout := `{"machine_id":"","boot_id":"","hostname":"h","cloud_init_instance_id":"","ssh_host_key_fingerprints":"","os_release":"","kernel_version":"","nic_count":0,"default_route_v4_count":0,"default_route_v6_count":0,"link_json":[],"addr_json":[],"route4_json":[]}`
	runner := &fakeRunner{result: proxmox.ExecResult{Exited: true, ExitCode: 0, Stdout: stdout}}
	c := NewFactsCollector(runner)

	facts, err := c.Collect(context.Background(), proxmox.VMRef{Node: "n1", VMID: 123}, 5*time.Second)
	if err != nil {
		t.Fatalf("Collect() error: %v", err)
	}
	if facts.SSHHostKeyFingerprints != nil {
		t.Errorf("SSHHostKeyFingerprints = %v, want nil for empty string", facts.SSHHostKeyFingerprints)
	}
	if got := facts.CanonicalSSHFingerprint(); got != "" {
		t.Errorf("CanonicalSSHFingerprint() = %q, want empty when no host keys", got)
	}
	if facts.MACAddresses != nil || facts.IPv4Addresses != nil {
		t.Errorf("expected nil MAC/IPv4 for empty arrays, got %v / %v", facts.MACAddresses, facts.IPv4Addresses)
	}
	if facts.DefaultGatewayV4 != "" {
		t.Errorf("DefaultGatewayV4 = %q, want empty when no default route", facts.DefaultGatewayV4)
	}
}

func TestFacts_CanonicalSSHFingerprint_FallsBackToAnyKnownKey(t *testing.T) {
	f := Facts{SSHHostKeyFingerprints: map[string]string{"ssh_host_dsa_key.pub": "SHA256:only"}}
	if got := f.CanonicalSSHFingerprint(); got != "SHA256:only" {
		t.Errorf("CanonicalSSHFingerprint() = %q, want SHA256:only (fallback khi khong co key type uu tien)", got)
	}
}

func TestFactsCollector_Collect_NonZeroExitReturnsError(t *testing.T) {
	runner := &fakeRunner{result: proxmox.ExecResult{Exited: true, ExitCode: 1, Stderr: "boom"}}
	c := NewFactsCollector(runner)

	_, err := c.Collect(context.Background(), proxmox.VMRef{Node: "n1", VMID: 123}, 5*time.Second)
	if err == nil {
		t.Fatal("Collect() error = nil, want error for non-zero exit code")
	}
}

func TestFactsCollector_Collect_MalformedJSONReturnsError(t *testing.T) {
	runner := &fakeRunner{result: proxmox.ExecResult{Exited: true, ExitCode: 0, Stdout: "not json"}}
	c := NewFactsCollector(runner)

	_, err := c.Collect(context.Background(), proxmox.VMRef{Node: "n1", VMID: 123}, 5*time.Second)
	if err == nil {
		t.Fatal("Collect() error = nil, want error for malformed JSON stdout")
	}
}
