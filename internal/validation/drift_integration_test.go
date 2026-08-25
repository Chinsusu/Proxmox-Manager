package validation

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/guest"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	db, err := storage.Open(dsn, 10, 5)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ready(); err != nil {
		t.Fatalf("db not ready: %v", err)
	}
	return db
}

func uniqueName(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + t.Name() + "-" + time.Now().Format("150405.000000000")
}

// seedInstanceForDrift tạo cluster + template ACTIVE + instance tối
// thiểu (cùng SQL/pattern với internal/instance's seedTemplate) để
// thoả FK, đăng ký cleanup theo đúng thứ tự phụ thuộc.
func seedInstanceForDrift(ctx context.Context, t *testing.T, db *storage.DB) *domain.VMInstance {
	t.Helper()

	var clusterID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO pve_clusters (name, base_url, secret_ref) VALUES ($1, $2, $3) RETURNING id
	`, uniqueName(t, "cluster"), "https://pve.test:8006/api2/json", "secret_ref_test").Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	var templateID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO vm_templates
			(name, family, version, os_family, os_version, architecture,
			 pve_cluster_id, pve_node, pve_template_vmid, source_checksum, state)
		VALUES ($1, $2, '2026.01.1', 'ubuntu', '22.04', 'amd64', $3, 'pve01', 9000, 'deadbeef', 'ACTIVE')
		RETURNING id
	`, uniqueName(t, "tpl"), uniqueName(t, "family"), clusterID).Scan(&templateID); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	instRepo := instance.NewRepository(db)
	inst, err := instRepo.Create(ctx, db, domain.VMInstance{
		LogicalName: uniqueName(t, "logical"),
		Hostname:    uniqueName(t, "host"),
		TemplateID:  templateID,
	})
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM identity_observations WHERE instance_id = $1`, inst.ID)
		_, _ = db.ExecContext(ctx, `DELETE FROM validation_runs WHERE instance_id = $1`, inst.ID)
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_instances WHERE id = $1`, inst.ID)
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_templates WHERE id = $1`, templateID)
		_, _ = db.ExecContext(ctx, `DELETE FROM pve_clusters WHERE id = $1`, clusterID)
	})

	return inst
}

// fakeRunner giả lập guest.Runner (QGA exec) — trả cùng một machine_id
// cố định qua fixedMachineID, đủ để test ScanInstance/digest stability
// mà không cần cluster Proxmox thật.
type fakeRunner struct {
	fixedMachineID string
}

func (f *fakeRunner) WaitExec(_ context.Context, _ proxmox.VMRef, _ []string, _ time.Duration) (proxmox.ExecResult, error) {
	stdout := `{"machine_id":"` + f.fixedMachineID + `","boot_id":"","hostname":"h","cloud_init_instance_id":"","ssh_host_key_fingerprints":"","os_release":"","kernel_version":"","nic_count":1,"default_route_v4_count":1,"default_route_v6_count":0,"link_json":[],"addr_json":[],"route4_json":[]}`
	return proxmox.ExecResult{Exited: true, ExitCode: 0, Stdout: stdout}, nil
}

func TestScanInstance_FirstScanHasNoDigestStabilityFinding(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	inst := seedInstanceForDrift(ctx, t, db)

	digester := NewIdentityDigester([]byte("test-key"))
	runner := &fakeRunner{fixedMachineID: "0123456789abcdef0123456789abcdef"}

	findings, facts, digest, err := ScanInstance(ctx, DriftScannerInput{
		InstanceID: inst.ID,
		Facts:      guest.NewFactsCollector(runner),
		Digester:   digester,
		Identity:   storage.NewIdentityRepository(db),
		VMRef:      proxmox.VMRef{Node: "n1", VMID: 100},
	})
	if err != nil {
		t.Fatalf("ScanInstance() error: %v", err)
	}
	for _, f := range findings {
		if f.Category == "identity_digest_stability" {
			t.Errorf("khong duoc co digest stability finding khi chua tung co quan sat truoc do, findings=%+v", findings)
		}
	}
	if facts.MachineID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("facts.MachineID = %q", facts.MachineID)
	}
	if digest == "" {
		t.Error("digest khong duoc rong")
	}
}

func TestScanInstance_DetectsDigestDrift(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	inst := seedInstanceForDrift(ctx, t, db)

	digester := NewIdentityDigester([]byte("test-key"))
	identityRepo := storage.NewIdentityRepository(db)

	original := &fakeRunner{fixedMachineID: "0123456789abcdef0123456789abcdef"}
	_, _, firstDigest, err := ScanInstance(ctx, DriftScannerInput{
		InstanceID: inst.ID, Facts: guest.NewFactsCollector(original), Digester: digester,
		Identity: identityRepo, VMRef: proxmox.VMRef{Node: "n1", VMID: 100},
	})
	if err != nil {
		t.Fatalf("first ScanInstance() error: %v", err)
	}
	// ScanInstance tu no khong ghi IdentityObservation (package validation
	// khong tu cham DB ghi) - phai tu tao baseline o day de mo phong lan
	// quet truoc do da duoc handler/wiring khac persist.
	if _, err := identityRepo.Create(ctx, db, domain.IdentityObservation{
		InstanceID: inst.ID, Generation: 1, MachineIDDigest: firstDigest,
		SSHHostFingerprint: "SHA256:baseline", Hostname: inst.Hostname,
	}); err != nil {
		t.Fatalf("seed baseline observation: %v", err)
	}

	changed := &fakeRunner{fixedMachineID: "ffffffffffffffffffffffffffffffff"}
	findings, _, secondDigest, err := ScanInstance(ctx, DriftScannerInput{
		InstanceID: inst.ID, Facts: guest.NewFactsCollector(changed), Digester: digester,
		Identity: identityRepo, VMRef: proxmox.VMRef{Node: "n1", VMID: 100},
	})
	if err != nil {
		t.Fatalf("second ScanInstance() error: %v", err)
	}
	if secondDigest == firstDigest {
		t.Fatal("digest phai khac nhau giua hai machine_id khac nhau")
	}

	var found bool
	for _, f := range findings {
		if f.Category == "identity_digest_stability" {
			found = true
			if f.Classification != DriftQuarantineWorthy {
				t.Errorf("Classification = %s, want %s", f.Classification, DriftQuarantineWorthy)
			}
		}
	}
	if !found {
		t.Fatalf("phai co identity_digest_stability finding khi machine-id doi, findings=%+v", findings)
	}
}
