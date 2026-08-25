package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestExecStatus_ExitedAsIntegerNotBoolean bao ve dung bug that da bat
// duoc khi verify tren cluster PVE 9.1.6: field "exited" tra ve so
// nguyen 1/0, khong phai JSON boolean true/false (quy uoc Perl cua
// Proxmox API). unmarshal truc tiep vao Go bool se loi
// "cannot unmarshal number into Go struct field .exited of type bool".
func TestExecStatus_ExitedAsIntegerNotBoolean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"exited":   1,
				"exitcode": 0,
				"out-data": "hello-vmf-test\n",
			},
		})
	}))
	defer srv.Close()

	client := NewClient(ClientConfig{BaseURL: srv.URL, TokenID: "test@pve!test", Secret: "secret", RequestTimeout: 5 * time.Second})
	adapter := NewAdapter(client)

	result, err := adapter.ExecStatus(context.Background(), VMRef{Node: "n1", VMID: 100}, 123)
	if err != nil {
		t.Fatalf("ExecStatus() error: %v", err)
	}
	if !result.Exited {
		t.Error("Exited = false, want true (exited:1 tu server phai duoc parse thanh true)")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "hello-vmf-test\n" {
		t.Errorf("Stdout = %q", result.Stdout)
	}
}

func TestExecStatus_NotYetExited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"exited": 0},
		})
	}))
	defer srv.Close()

	client := NewClient(ClientConfig{BaseURL: srv.URL, TokenID: "test@pve!test", Secret: "secret", RequestTimeout: 5 * time.Second})
	adapter := NewAdapter(client)

	result, err := adapter.ExecStatus(context.Background(), VMRef{Node: "n1", VMID: 100}, 123)
	if err != nil {
		t.Fatalf("ExecStatus() error: %v", err)
	}
	if result.Exited {
		t.Error("Exited = true, want false (exited:0 tu server phai duoc parse thanh false)")
	}
}

func TestExec_EncodesCommandArrayAsRepeatedFormKey(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		capturedBody = r.Form["command"][0] + "|" + r.Form["command"][1]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"pid": 42}})
	}))
	defer srv.Close()

	client := NewClient(ClientConfig{BaseURL: srv.URL, TokenID: "test@pve!test", Secret: "secret", RequestTimeout: 5 * time.Second})
	adapter := NewAdapter(client)

	pid, err := adapter.Exec(context.Background(), VMRef{Node: "n1", VMID: 100}, []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("Exec() error: %v", err)
	}
	if pid != 42 {
		t.Errorf("pid = %d, want 42", pid)
	}
	if capturedBody != "echo|hello" {
		t.Errorf("server received command = %q, want \"echo|hello\" (command array phai encode thanh nhieu key command= lap lai)", capturedBody)
	}
}
