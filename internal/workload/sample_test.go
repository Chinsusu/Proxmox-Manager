package workload

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chinsusu/vm-factory/internal/proxmox"
)

// fakePVE giả lập đủ endpoint QGA (exec/exec-status/file-write) để test
// SampleAdapter mà không cần cluster Proxmox thật — verify hành vi thật
// trên guest thật thuộc task riêng (P0-08 real-cluster verification).
// respond quyết định output theo command, cho phép test kịch bản khác
// nhau (marker tồn tại/không tồn tại) mà không cần server có state phức tạp.
type fakePVE struct {
	mu       sync.Mutex
	nextPID  int
	execCmds map[int][]string
	respond  func(command []string) (exitCode int, stdout, stderr string)
	writes   map[string][]byte
	hit      bool
}

func newFakePVE(respond func(command []string) (int, string, string)) *fakePVE {
	return &fakePVE{execCmds: make(map[int][]string), respond: respond, writes: make(map[string][]byte)}
}

func (f *fakePVE) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hit = true
		f.mu.Unlock()
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/agent/exec") && !strings.Contains(r.URL.Path, "exec-status"):
			f.mu.Lock()
			f.nextPID++
			pid := f.nextPID
			f.execCmds[pid] = r.Form["command"]
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"pid": pid}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/agent/exec-status"):
			pid, _ := strconv.Atoi(r.Form.Get("pid"))
			f.mu.Lock()
			cmd := f.execCmds[pid]
			f.mu.Unlock()
			exitCode, stdout, stderr := f.respond(cmd)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"exited": 1, "exitcode": exitCode, "out-data": stdout, "err-data": stderr,
			}})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/agent/file-write"):
			path := r.Form.Get("file")
			content, _ := base64.StdEncoding.DecodeString(r.Form.Get("content"))
			f.mu.Lock()
			f.writes[path] = content
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": nil})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}
}

func newAdapterAgainst(srv *httptest.Server) *proxmox.Adapter {
	client := proxmox.NewClient(proxmox.ClientConfig{BaseURL: srv.URL, TokenID: "test@pve!test", Secret: "secret", RequestTimeout: 5 * time.Second})
	return proxmox.NewAdapter(client)
}

func testSpec() SampleSpec {
	content := []byte("#!/bin/sh\necho hello\n")
	sum := sha256.Sum256(content)
	return SampleSpec{
		Name: "demo", ServiceName: "vmf-demo", InstallPath: "/opt/vmf-workload/demo/run.sh",
		Artifact: Artifact{Content: content, SHA256: hex.EncodeToString(sum[:])},
	}
}

func TestSampleAdapter_Install_ChecksumMismatch_NoGuestCallMade(t *testing.T) {
	fake := newFakePVE(func(_ []string) (int, string, string) { return 0, "", "" })
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	spec := testSpec()
	spec.Artifact.SHA256 = strings.Repeat("0", 64)
	specJSON, _ := json.Marshal(spec)

	adapter := NewSampleAdapter(newAdapterAgainst(srv))
	err := adapter.Install(context.Background(), proxmox.VMRef{Node: "n1", VMID: 100}, specJSON)
	if err == nil {
		t.Fatal("Install() error = nil, want checksum mismatch error (WRK-001)")
	}
	fake.mu.Lock()
	hit := fake.hit
	fake.mu.Unlock()
	if hit {
		t.Error("checksum mismatch phai fail TRUOC khi cham guest - khong duoc co HTTP request nao toi PVE")
	}
}

func TestSampleAdapter_Install_IdempotentSkipWhenMarkerMatches(t *testing.T) {
	spec := testSpec()
	specJSON, _ := json.Marshal(spec)
	marker, _ := json.Marshal(installMarker{
		Name: spec.Name, ServiceName: spec.ServiceName, InstallPath: spec.InstallPath, SHA256: spec.Artifact.SHA256,
	})

	var writeCount int
	fake := newFakePVE(func(cmd []string) (int, string, string) {
		if len(cmd) >= 2 && cmd[0] == "cat" {
			return 0, string(marker), ""
		}
		return 0, "", ""
	})
	srv := httptest.NewServer(func() http.HandlerFunc {
		h := fake.handler()
		return func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "file-write") {
				writeCount++
			}
			h(w, r)
		}
	}())
	defer srv.Close()

	adapter := NewSampleAdapter(newAdapterAgainst(srv))
	if err := adapter.Install(context.Background(), proxmox.VMRef{Node: "n1", VMID: 100}, specJSON); err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if writeCount != 0 {
		t.Errorf("write count = %d, want 0 (marker da khop, phai bo qua toan bo cai dat)", writeCount)
	}
}

func TestSampleAdapter_Install_FullFlowWritesArtifactUnitAndMarker(t *testing.T) {
	spec := testSpec()
	specJSON, _ := json.Marshal(spec)
	fake := newFakePVE(func(cmd []string) (int, string, string) {
		if len(cmd) >= 2 && cmd[0] == "cat" {
			return 1, "", "No such file or directory" // chua tung cai
		}
		return 0, "", ""
	})
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	adapter := NewSampleAdapter(newAdapterAgainst(srv))
	if err := adapter.Install(context.Background(), proxmox.VMRef{Node: "n1", VMID: 100}, specJSON); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if string(fake.writes[spec.InstallPath]) != string(spec.Artifact.Content) {
		t.Errorf("artifact content khong khop, got %q", fake.writes[spec.InstallPath])
	}
	unitPath := "/etc/systemd/system/" + spec.ServiceName + ".service"
	if _, ok := fake.writes[unitPath]; !ok {
		t.Errorf("systemd unit khong duoc ghi tai %s", unitPath)
	}
	var m installMarker
	if err := json.Unmarshal(fake.writes[sampleMarkerPath], &m); err != nil {
		t.Fatalf("marker khong parse duoc: %v", err)
	}
	if m.SHA256 != spec.Artifact.SHA256 || m.ServiceName != spec.ServiceName {
		t.Errorf("marker = %+v, khong khop spec", m)
	}
}

func TestSampleAdapter_Health_MapsSystemctlIsActive(t *testing.T) {
	spec := testSpec()
	marker, _ := json.Marshal(installMarker{Name: spec.Name, ServiceName: spec.ServiceName, InstallPath: spec.InstallPath, SHA256: spec.Artifact.SHA256})
	fake := newFakePVE(func(cmd []string) (int, string, string) {
		switch {
		case len(cmd) >= 1 && cmd[0] == "cat":
			return 0, string(marker), ""
		case len(cmd) >= 2 && cmd[0] == "systemctl" && cmd[1] == "is-active":
			return 0, "active\n", ""
		}
		return 1, "", "unexpected"
	})
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	adapter := NewSampleAdapter(newAdapterAgainst(srv))
	report, err := adapter.Health(context.Background(), proxmox.VMRef{Node: "n1", VMID: 100})
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if !report.Healthy || report.ServiceState != "active" {
		t.Errorf("report = %+v, want Healthy=true ServiceState=active", report)
	}
}

func TestSampleAdapter_Health_InactiveIsUnhealthy(t *testing.T) {
	spec := testSpec()
	marker, _ := json.Marshal(installMarker{Name: spec.Name, ServiceName: spec.ServiceName, InstallPath: spec.InstallPath, SHA256: spec.Artifact.SHA256})
	fake := newFakePVE(func(cmd []string) (int, string, string) {
		if len(cmd) >= 1 && cmd[0] == "cat" {
			return 0, string(marker), ""
		}
		return 3, "inactive\n", ""
	})
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	adapter := NewSampleAdapter(newAdapterAgainst(srv))
	report, err := adapter.Health(context.Background(), proxmox.VMRef{Node: "n1", VMID: 100})
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if report.Healthy {
		t.Errorf("report = %+v, want Healthy=false (exit code 3, inactive)", report)
	}
}

func TestSampleAdapter_Health_NoMarkerReturnsError(t *testing.T) {
	fake := newFakePVE(func(_ []string) (int, string, string) { return 1, "", "No such file or directory" })
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	adapter := NewSampleAdapter(newAdapterAgainst(srv))
	_, err := adapter.Health(context.Background(), proxmox.VMRef{Node: "n1", VMID: 100})
	if err == nil {
		t.Fatal("Health() error = nil, want error khi chua tung cai (khong co marker)")
	}
}

func TestSampleAdapter_Validate_WellFormedMarkerIsValid(t *testing.T) {
	spec := testSpec()
	marker, _ := json.Marshal(installMarker{Name: spec.Name, ServiceName: spec.ServiceName, InstallPath: spec.InstallPath, SHA256: spec.Artifact.SHA256})
	fake := newFakePVE(func(_ []string) (int, string, string) { return 0, string(marker), "" })
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	adapter := NewSampleAdapter(newAdapterAgainst(srv))
	report, err := adapter.Validate(context.Background(), proxmox.VMRef{Node: "n1", VMID: 100})
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if !report.Valid {
		t.Errorf("report = %+v, want Valid=true", report)
	}
}

func TestSampleAdapter_Validate_MissingMarkerIsInvalid(t *testing.T) {
	fake := newFakePVE(func(_ []string) (int, string, string) { return 1, "", "No such file or directory" })
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	adapter := NewSampleAdapter(newAdapterAgainst(srv))
	report, err := adapter.Validate(context.Background(), proxmox.VMRef{Node: "n1", VMID: 100})
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if report.Valid {
		t.Errorf("report = %+v, want Valid=false (chua tung cai)", report)
	}
}

func TestSampleAdapter_Remove_NoMarkerIsIdempotentSuccess(t *testing.T) {
	fake := newFakePVE(func(_ []string) (int, string, string) { return 1, "", "No such file or directory" })
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	adapter := NewSampleAdapter(newAdapterAgainst(srv))
	if err := adapter.Remove(context.Background(), proxmox.VMRef{Node: "n1", VMID: 100}); err != nil {
		t.Fatalf("Remove() error: %v, want nil (chua tung cai, treat not-found as success)", err)
	}
}
