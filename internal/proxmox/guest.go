package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// ExecResult là kết quả đọc GET /nodes/{node}/qemu/{vmid}/agent/exec-status.
type ExecResult struct {
	Exited   bool
	ExitCode int
	Stdout   string
	Stderr   string
}

// Exec chạy một lệnh trong guest qua QGA — POST
// /nodes/{node}/qemu/{vmid}/agent/exec. Tham số command là mảng
// program+argument (khớp schema thật: "command: array"), form-encode
// bằng cách lặp key command= nhiều lần — quy ước PVE dùng cho tham số
// kiểu array trong request form-urlencoded.
func (a *Adapter) Exec(ctx context.Context, ref VMRef, command []string) (pid int, err error) {
	params := url.Values{}
	for _, c := range command {
		params.Add("command", c)
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/exec", ref.Node, ref.VMID)
	data, err := a.client.do(ctx, "POST", path, params)
	if err != nil {
		return 0, err
	}
	var raw struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, fmt.Errorf("proxmox: decode exec pid: %w", err)
	}
	return raw.PID, nil
}

// ExecStatus đọc kết quả một exec đang/đã chạy — GET
// /nodes/{node}/qemu/{vmid}/agent/exec-status.
func (a *Adapter) ExecStatus(ctx context.Context, ref VMRef, pid int) (ExecResult, error) {
	params := url.Values{}
	params.Set("pid", strconv.Itoa(pid))
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/exec-status", ref.Node, ref.VMID)
	data, err := a.client.do(ctx, "GET", path, params)
	if err != nil {
		return ExecResult{}, err
	}
	// "exited" tra ve so nguyen 1/0, khong phai true/false JSON (quy uoc
	// Perl cua Proxmox API rò qua JSON encoder) - verify that tren
	// cluster PVE 9.1.6, khong doan.
	var raw struct {
		Exited   int    `json:"exited"`
		ExitCode int    `json:"exitcode"`
		OutData  string `json:"out-data"`
		ErrData  string `json:"err-data"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ExecResult{}, fmt.Errorf("proxmox: decode exec-status: %w", err)
	}
	return ExecResult{Exited: raw.Exited != 0, ExitCode: raw.ExitCode, Stdout: raw.OutData, Stderr: raw.ErrData}, nil
}

// WaitExec chạy Exec rồi poll ExecStatus tới khi tiến trình kết thúc
// hoặc hết timeout — không dùng sleep cố định đơn (guardrail Phần II
// mục 18), poll mỗi giây vì exec trong guest thường nhanh hơn nhiều
// so với task Proxmox nên không cần backoff dài như WaitForTask.
func (a *Adapter) WaitExec(ctx context.Context, ref VMRef, command []string, timeout time.Duration) (ExecResult, error) {
	pid, err := a.Exec(ctx, ref, command)
	if err != nil {
		return ExecResult{}, err
	}

	deadline := time.Now().Add(timeout)
	for {
		result, err := a.ExecStatus(ctx, ref, pid)
		if err != nil {
			return ExecResult{}, err
		}
		if result.Exited {
			return result, nil
		}
		if time.Now().After(deadline) {
			return result, fmt.Errorf("proxmox: exec pid %d did not finish within timeout", pid)
		}
		select {
		case <-ctx.Done():
			return ExecResult{}, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}
