package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/Chinsusu/vm-factory/internal/domain"
)

// seedTemplateForIdentity tạo cluster + template ACTIVE tối thiểu để
// thoả FK vm_instances.template_id — cùng SQL với
// internal/instance/repository_integration_test.go's seedTemplate, lặp
// lại ở đây vì test file đó nằm ở package instance khác (không export).
func seedTemplateForIdentity(ctx context.Context, t *testing.T, db *DB) string {
	t.Helper()

	var clusterID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO pve_clusters (name, base_url, secret_ref) VALUES ($1, $2, $3) RETURNING id
	`, uniqueKey(t)+"-cluster", "https://pve.test:8006/api2/json", "secret_ref_test").Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	var templateID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO vm_templates
			(name, family, version, os_family, os_version, architecture,
			 pve_cluster_id, pve_node, pve_template_vmid, source_checksum, state)
		VALUES ($1, $2, '2026.01.1', 'ubuntu', '22.04', 'amd64', $3, 'pve01', 9000, 'deadbeef', 'ACTIVE')
		RETURNING id
	`, uniqueKey(t)+"-tpl", uniqueKey(t)+"-family", clusterID).Scan(&templateID); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_templates WHERE id = $1`, templateID)
		_, _ = db.ExecContext(ctx, `DELETE FROM pve_clusters WHERE id = $1`, clusterID)
	})

	return templateID
}

// seedInstanceForIdentity tạo một vm_instance qua raw SQL (không import
// internal/instance — package đó tự import internal/storage nên import
// ngược trong _test.go cùng package storage sẽ tạo import cycle). Đăng
// ký cleanup xoá identity_observations/validation_runs của nó TRƯỚC
// vm_instances (thứ tự LIFO của t.Cleanup) để tránh FK violation kiểu
// đã gặp ở P0-03.
func seedInstanceForIdentity(ctx context.Context, t *testing.T, db *DB, templateID string) *domain.VMInstance {
	t.Helper()
	inst := &domain.VMInstance{
		LogicalName: uniqueKey(t) + "-logical",
		Hostname:    uniqueKey(t) + "-host",
		TemplateID:  templateID,
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO vm_instances (logical_name, hostname, template_id)
		VALUES ($1, $2, $3)
		RETURNING id
	`, inst.LogicalName, inst.Hostname, inst.TemplateID).Scan(&inst.ID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM identity_observations WHERE instance_id = $1`, inst.ID)
		_, _ = db.ExecContext(ctx, `DELETE FROM validation_runs WHERE instance_id = $1`, inst.ID)
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_instances WHERE id = $1`, inst.ID)
	})
	return inst
}

func TestIdentityRepository_CreateAndRoundTripArrays(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	inst := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewIdentityRepository(db)

	ciID := "iid-test-1"
	bootID := "boot-uuid-1"
	created, err := repo.Create(ctx, db, domain.IdentityObservation{
		InstanceID:          inst.ID,
		Generation:          1,
		MachineIDDigest:     "hmac-sha256:" + uniqueKey(t),
		SSHHostFingerprint:  "SHA256:abc",
		CloudInitInstanceID: &ciID,
		Hostname:            inst.Hostname,
		MACAddresses:        []string{"bc:24:11:aa:bb:cc"},
		IPAddresses:         []string{"10.98.0.15"},
		BootID:              &bootID,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if len(created.MACAddresses) != 1 || created.MACAddresses[0] != "bc:24:11:aa:bb:cc" {
		t.Errorf("MACAddresses round-trip = %v", created.MACAddresses)
	}
	if len(created.IPAddresses) != 1 || created.IPAddresses[0] != "10.98.0.15" {
		t.Errorf("IPAddresses round-trip = %v", created.IPAddresses)
	}
	if created.CloudInitInstanceID == nil || *created.CloudInitInstanceID != ciID {
		t.Errorf("CloudInitInstanceID round-trip = %v", created.CloudInitInstanceID)
	}
	if created.BootID == nil || *created.BootID != bootID {
		t.Errorf("BootID round-trip = %v", created.BootID)
	}
}

func TestIdentityRepository_Create_EmptyArraysRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	inst := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewIdentityRepository(db)

	created, err := repo.Create(ctx, db, domain.IdentityObservation{
		InstanceID:         inst.ID,
		Generation:         1,
		MachineIDDigest:    "hmac-sha256:" + uniqueKey(t),
		SSHHostFingerprint: "SHA256:xyz",
		Hostname:           inst.Hostname,
	})
	if err != nil {
		t.Fatalf("Create() error: %v (COALESCE/NULLIF empty-array path)", err)
	}
	if created.MACAddresses != nil {
		t.Errorf("MACAddresses = %v, want nil for no MACs given", created.MACAddresses)
	}
	if created.IPAddresses != nil {
		t.Errorf("IPAddresses = %v, want nil for no IPs given", created.IPAddresses)
	}
}

func TestIdentityRepository_FindDuplicateMachineDigest(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	instA := seedInstanceForIdentity(ctx, t, db, templateID)
	instB := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewIdentityRepository(db)

	digest := "hmac-sha256:" + uniqueKey(t)
	if _, err := repo.Create(ctx, db, domain.IdentityObservation{
		InstanceID: instA.ID, Generation: 1, MachineIDDigest: digest,
		SSHHostFingerprint: "SHA256:a", Hostname: instA.Hostname,
	}); err != nil {
		t.Fatalf("seed observation A: %v", err)
	}

	matches, err := repo.FindDuplicateMachineDigest(ctx, digest, instB.ID)
	if err != nil {
		t.Fatalf("FindDuplicateMachineDigest() error: %v", err)
	}
	if len(matches) != 1 || matches[0].InstanceID != instA.ID {
		t.Fatalf("matches = %+v, want exactly instA", matches)
	}
	if matches[0].Retired {
		t.Error("instA chua retired, Retired phai la false")
	}

	selfMatches, err := repo.FindDuplicateMachineDigest(ctx, digest, instA.ID)
	if err != nil {
		t.Fatalf("FindDuplicateMachineDigest() self-exclude error: %v", err)
	}
	if len(selfMatches) != 0 {
		t.Fatalf("self-exclude matches = %+v, want empty (khong tu bao trung voi chinh minh)", selfMatches)
	}
}

func TestIdentityRepository_LatestByInstance(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	inst := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewIdentityRepository(db)

	older, err := repo.Create(ctx, db, domain.IdentityObservation{
		InstanceID: inst.ID, Generation: 1, MachineIDDigest: "hmac-sha256:older-" + uniqueKey(t),
		SSHHostFingerprint: "SHA256:a", Hostname: inst.Hostname,
	})
	if err != nil {
		t.Fatalf("create older observation: %v", err)
	}
	newer, err := repo.Create(ctx, db, domain.IdentityObservation{
		InstanceID: inst.ID, Generation: 1, MachineIDDigest: "hmac-sha256:newer-" + uniqueKey(t),
		SSHHostFingerprint: "SHA256:a", Hostname: inst.Hostname,
	})
	if err != nil {
		t.Fatalf("create newer observation: %v", err)
	}
	if older.ID == newer.ID {
		t.Fatal("older and newer observations must have distinct IDs")
	}

	got, err := repo.LatestByInstance(ctx, inst.ID)
	if err != nil {
		t.Fatalf("LatestByInstance() error: %v", err)
	}
	if got.ID != newer.ID {
		t.Errorf("LatestByInstance() = %s, want the newer observation %s", got.ID, newer.ID)
	}
}

func TestIdentityRepository_LatestByInstance_NotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	inst := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewIdentityRepository(db)

	_, err := repo.LatestByInstance(ctx, inst.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("LatestByInstance() error = %v, want domain.ErrNotFound", err)
	}
}

func TestIdentityRepository_FindDuplicateSSHFingerprint(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	instA := seedInstanceForIdentity(ctx, t, db, templateID)
	instB := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewIdentityRepository(db)

	fingerprint := "SHA256:" + uniqueKey(t)
	if _, err := repo.Create(ctx, db, domain.IdentityObservation{
		InstanceID: instA.ID, Generation: 1, MachineIDDigest: "hmac-sha256:" + uniqueKey(t),
		SSHHostFingerprint: fingerprint, Hostname: instA.Hostname,
	}); err != nil {
		t.Fatalf("seed observation A: %v", err)
	}

	matches, err := repo.FindDuplicateSSHFingerprint(ctx, fingerprint, instB.ID)
	if err != nil {
		t.Fatalf("FindDuplicateSSHFingerprint() error: %v", err)
	}
	if len(matches) != 1 || matches[0].InstanceID != instA.ID {
		t.Fatalf("matches = %+v, want exactly instA", matches)
	}

	selfMatches, err := repo.FindDuplicateSSHFingerprint(ctx, fingerprint, instA.ID)
	if err != nil {
		t.Fatalf("FindDuplicateSSHFingerprint() self-exclude error: %v", err)
	}
	if len(selfMatches) != 0 {
		t.Fatalf("self-exclude matches = %+v, want empty", selfMatches)
	}
}

func TestIdentityRepository_FindDuplicateMachineDigest_DetectsRetiredInstance(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedTemplateForIdentity(ctx, t, db)
	instA := seedInstanceForIdentity(ctx, t, db, templateID)
	instB := seedInstanceForIdentity(ctx, t, db, templateID)
	repo := NewIdentityRepository(db)

	digest := "hmac-sha256:" + uniqueKey(t)
	if _, err := repo.Create(ctx, db, domain.IdentityObservation{
		InstanceID: instA.ID, Generation: 1, MachineIDDigest: digest,
		SSHHostFingerprint: "SHA256:a", Hostname: instA.Hostname,
	}); err != nil {
		t.Fatalf("seed observation A: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE vm_instances SET retired_at = now() WHERE id = $1`, instA.ID); err != nil {
		t.Fatalf("retire instA: %v", err)
	}

	matches, err := repo.FindDuplicateMachineDigest(ctx, digest, instB.ID)
	if err != nil {
		t.Fatalf("FindDuplicateMachineDigest() error: %v", err)
	}
	if len(matches) != 1 || !matches[0].Retired {
		t.Fatalf("matches = %+v, want exactly one Retired=true match", matches)
	}
}
