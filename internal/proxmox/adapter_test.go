package proxmox

import (
	"encoding/json"
	"testing"
)

func TestTaskStatus_DoneAndSuccess(t *testing.T) {
	cases := []struct {
		name        string
		status      TaskStatus
		wantDone    bool
		wantSuccess bool
	}{
		{"running", TaskStatus{Status: "running"}, false, false},
		{"stopped ok", TaskStatus{Status: "stopped", ExitStatus: "OK"}, true, true},
		{"stopped error", TaskStatus{Status: "stopped", ExitStatus: "unable to open file"}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.status.Done(); got != c.wantDone {
				t.Errorf("Done() = %v, want %v", got, c.wantDone)
			}
			if got := c.status.Success(); got != c.wantSuccess {
				t.Errorf("Success() = %v, want %v", got, c.wantSuccess)
			}
		})
	}
}

func TestVMObservedState_IsRunning(t *testing.T) {
	if !(VMObservedState{Status: "running"}).IsRunning() {
		t.Error("status=running should be IsRunning")
	}
	if (VMObservedState{Status: "stopped"}).IsRunning() {
		t.Error("status=stopped should not be IsRunning")
	}
}

func TestBuildNet0(t *testing.T) {
	cases := []struct {
		name string
		cfg  NetConfig
		want string
	}{
		{"with firewall", NetConfig{Bridge: "vmbr1", Firewall: true}, "virtio,bridge=vmbr1,firewall=1"},
		{"without firewall", NetConfig{Bridge: "vmbr1", Firewall: false}, "virtio,bridge=vmbr1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := buildNet0(c.cfg); got != c.want {
				t.Errorf("buildNet0() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBoolParam(t *testing.T) {
	if boolParam(true) != "1" {
		t.Error("boolParam(true) should be \"1\"")
	}
	if boolParam(false) != "0" {
		t.Error("boolParam(false) should be \"0\"")
	}
}

func TestParseUPID(t *testing.T) {
	got, err := parseUPID(json.RawMessage(`"UPID:us-ny:00123:...:qmclone:9101:vmfactory@pve!automation:"`))
	if err != nil {
		t.Fatalf("parseUPID() error: %v", err)
	}
	if got == "" {
		t.Fatal("parseUPID() returned empty string")
	}

	if _, err := parseUPID(json.RawMessage(`""`)); err == nil {
		t.Fatal("parseUPID() should error on empty upid")
	}
	if _, err := parseUPID(json.RawMessage(`null`)); err == nil {
		t.Fatal("parseUPID() should error on null")
	}
}

func TestParseFlexibleInt(t *testing.T) {
	got, err := parseFlexibleInt(json.RawMessage(`9101`))
	if err != nil || got != 9101 {
		t.Fatalf("parseFlexibleInt(int) = %d, %v, want 9101, nil", got, err)
	}

	got, err = parseFlexibleInt(json.RawMessage(`"9101"`))
	if err != nil || got != 9101 {
		t.Fatalf("parseFlexibleInt(string) = %d, %v, want 9101, nil", got, err)
	}

	if _, err := parseFlexibleInt(json.RawMessage(`"not-a-number"`)); err == nil {
		t.Fatal("parseFlexibleInt() should error on non-numeric string")
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name       string
		httpStatus int
		body       string
		wantCode   string
	}{
		{"unauthorized", 401, "authentication failure", CodeAuthFailed},
		{"forbidden", 403, "Permission check failed", CodeAuthFailed},
		{"vmid conflict", 500, "VM 9101 already exists", CodeVMIDConflict},
		{"bridge missing", 500, "bridge 'vmbr9' does not exist", CodeBridgeNotFound},
		{"no space", 500, "no space left on device", CodeStorageCapacity},
		{"locked", 400, "VM is locked (clone)", CodeVMLocked},
		{"template invalid", 500, "unable to parse config: bad syntax", CodeTemplateInvalid},
		{"unknown", 500, "something else entirely", CodeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := classifyError(c.httpStatus, c.body)
			if err.Code != c.wantCode {
				t.Errorf("classifyError() code = %q, want %q", err.Code, c.wantCode)
			}
			if err.HTTPStatus != c.httpStatus {
				t.Errorf("HTTPStatus = %d, want %d", err.HTTPStatus, c.httpStatus)
			}
		})
	}
}
