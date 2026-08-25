package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Adapter implement ProxmoxAdapter interface (Phần II mục 3.5) cho
// một cluster. Method/param HTTP đã verify trực tiếp qua schema thật
// của cluster PVE 9.1.6 (xem package doc).
type Adapter struct {
	client *Client
}

// NewAdapter tạo Adapter từ Client đã cấu hình.
func NewAdapter(client *Client) *Adapter {
	return &Adapter{client: client}
}

// AllocateNextVMID gọi GET /cluster/nextid. VM Factory vẫn phải reserve
// VMID trong DB trước side effect tiếp theo (Phần II mục 8.1) — adapter
// chỉ trả gợi ý, không tự giữ chỗ.
func (a *Adapter) AllocateNextVMID(ctx context.Context) (int, error) {
	data, err := a.client.do(ctx, "allocate_next_vmid", "GET", "/cluster/nextid", nil)
	if err != nil {
		return 0, err
	}
	return parseFlexibleInt(data)
}

// Clone gọi POST /nodes/{source_node}/qemu/{source_vmid}/clone.
// target chỉ được set nếu target node khác source node và storage là
// shared — mặc định P0 dùng full clone trên cùng node/local storage
// (ADR-008) nên không set target trong trường hợp thường.
func (a *Adapter) Clone(ctx context.Context, req CloneRequest) (TaskRef, error) {
	params := url.Values{}
	params.Set("newid", strconv.Itoa(req.TargetVMID))
	params.Set("full", boolParam(req.FullClone))
	if req.Name != "" {
		params.Set("name", req.Name)
	}
	if req.Storage != "" {
		params.Set("storage", req.Storage)
	}
	if req.Pool != "" {
		params.Set("pool", req.Pool)
	}
	if req.Description != "" {
		params.Set("description", req.Description)
	}
	if req.TargetNode != "" && req.TargetNode != req.SourceNode {
		params.Set("target", req.TargetNode)
	}

	path := fmt.Sprintf("/nodes/%s/qemu/%d/clone", req.SourceNode, req.SourceVMID)
	data, err := a.client.do(ctx, "clone", "POST", path, params)
	if err != nil {
		return TaskRef{}, err
	}
	upid, err := parseUPID(data)
	if err != nil {
		return TaskRef{}, err
	}
	return TaskRef{Node: req.SourceNode, UPID: upid}, nil
}

// Configure gọi POST /nodes/{node}/qemu/{vmid}/config — bản POST
// (asynchronous API, trả task UPID), không dùng PUT (synchronous,
// return null) để khớp uniform External Side-effect Pattern
// (Phần II mục 7: mọi mutation đều có task reference để poll).
func (a *Adapter) Configure(ctx context.Context, req ConfigureRequest) (TaskRef, error) {
	params := url.Values{}
	if req.Cores > 0 {
		params.Set("cores", strconv.Itoa(req.Cores))
	}
	if req.Sockets > 0 {
		params.Set("sockets", strconv.Itoa(req.Sockets))
	}
	if req.MemoryMB > 0 {
		params.Set("memory", strconv.Itoa(req.MemoryMB))
	}
	if req.CPUType != "" {
		params.Set("cpu", req.CPUType)
	}
	params.Set("agent", boolParam(req.Agent))
	params.Set("onboot", boolParam(req.OnBoot))
	if req.Net0.Bridge != "" {
		params.Set("net0", buildNet0(req.Net0))
	}
	if req.IPConfig0 != "" {
		params.Set("ipconfig0", req.IPConfig0)
	}

	path := fmt.Sprintf("/nodes/%s/qemu/%d/config", req.Node, req.VMID)
	data, err := a.client.do(ctx, "configure", "POST", path, params)
	if err != nil {
		return TaskRef{}, err
	}
	upid, err := parseUPID(data)
	if err != nil {
		return TaskRef{}, err
	}
	return TaskRef{Node: req.Node, UPID: upid}, nil
}

// Start gọi POST /nodes/{node}/qemu/{vmid}/status/start.
func (a *Adapter) Start(ctx context.Context, ref VMRef) (TaskRef, error) {
	return a.statusAction(ctx, ref, "start")
}

// Stop gọi POST /nodes/{node}/qemu/{vmid}/status/stop — hard stop
// (rút phích cắm), Phần III mục 9.1 ưu tiên graceful shutdown qua
// QGA trước khi rơi vào force stop; caller chịu trách nhiệm chọn đúng
// thời điểm gọi Stop này.
func (a *Adapter) Stop(ctx context.Context, ref VMRef) (TaskRef, error) {
	return a.statusAction(ctx, ref, "stop")
}

func (a *Adapter) statusAction(ctx context.Context, ref VMRef, action string) (TaskRef, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/status/%s", ref.Node, ref.VMID, action)
	data, err := a.client.do(ctx, "vm_"+action, "POST", path, url.Values{})
	if err != nil {
		return TaskRef{}, err
	}
	upid, err := parseUPID(data)
	if err != nil {
		return TaskRef{}, err
	}
	return TaskRef{Node: ref.Node, UPID: upid}, nil
}

// Delete gọi DELETE /nodes/{node}/qemu/{vmid}. Not-found được caller
// coi là success idempotent sau khi verify (Phần III mục 9.2) — Delete
// tự nó vẫn trả lỗi thật nếu VM không tồn tại, đó là quyết định của
// caller (state engine), không phải adapter.
func (a *Adapter) Delete(ctx context.Context, ref VMRef, purge bool) (TaskRef, error) {
	params := url.Values{}
	params.Set("purge", boolParam(purge))
	path := fmt.Sprintf("/nodes/%s/qemu/%d", ref.Node, ref.VMID)
	data, err := a.client.do(ctx, "delete", "DELETE", path, params)
	if err != nil {
		return TaskRef{}, err
	}
	upid, err := parseUPID(data)
	if err != nil {
		return TaskRef{}, err
	}
	return TaskRef{Node: ref.Node, UPID: upid}, nil
}

// GetTask gọi GET /nodes/{node}/tasks/{upid}/status.
func (a *Adapter) GetTask(ctx context.Context, task TaskRef) (TaskStatus, error) {
	path := fmt.Sprintf("/nodes/%s/tasks/%s/status", task.Node, task.UPID)
	data, err := a.client.do(ctx, "get_task", "GET", path, nil)
	if err != nil {
		return TaskStatus{}, err
	}
	var raw struct {
		Status     string `json:"status"`
		ExitStatus string `json:"exitstatus"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return TaskStatus{}, fmt.Errorf("proxmox: decode task status: %w", err)
	}
	return TaskStatus{Status: raw.Status, ExitStatus: raw.ExitStatus}, nil
}

// GetVM gọi GET /nodes/{node}/qemu/{vmid}/status/current.
func (a *Adapter) GetVM(ctx context.Context, ref VMRef) (VMObservedState, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/status/current", ref.Node, ref.VMID)
	data, err := a.client.do(ctx, "get_vm", "GET", path, nil)
	if err != nil {
		return VMObservedState{}, err
	}
	var raw struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Lock   string `json:"lock"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return VMObservedState{}, fmt.Errorf("proxmox: decode vm status: %w", err)
	}
	return VMObservedState{VMRef: ref, Name: raw.Name, Status: raw.Status, Locked: raw.Lock}, nil
}

// GetConfig gọi GET /nodes/{node}/qemu/{vmid}/config — dùng để đọc lại
// MAC net0 mà Proxmox tự sinh (Configure ở Phần III mục 6 không truyền
// MAC tường minh), phục vụ ID-005 MAC match (Phần VIII mục 4) ở P0-07.
func (a *Adapter) GetConfig(ctx context.Context, ref VMRef) (VMConfig, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/config", ref.Node, ref.VMID)
	data, err := a.client.do(ctx, "get_config", "GET", path, nil)
	if err != nil {
		return VMConfig{}, err
	}
	var raw struct {
		Net0 string `json:"net0"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return VMConfig{}, fmt.Errorf("proxmox: decode vm config: %w", err)
	}
	return VMConfig{Net0MAC: parseNet0MAC(raw.Net0)}, nil
}

// parseNet0MAC trích MAC từ chuỗi net0 dạng
// "<model>=<MAC>,bridge=...,firewall=..." (vd
// "virtio=BC:24:11:AA:BB:CC,bridge=vmbr0,firewall=1") — format chuẩn
// Proxmox trả về khi MAC được tự sinh (không truyền tường minh lúc
// Configure). Trả rỗng nếu không parse được thay vì lỗi cứng — caller
// tự quyết định coi đây là UNKNOWN.
//
// Chuẩn hoá về lowercase — verify thật trên cluster PVE 9.1.6 cho thấy
// Proxmox trả MAC dạng UPPERCASE ("D8:FC:93:...") trong khi guest Linux
// (`ip -j link show`, dùng bởi internal/guest facts collector) luôn báo
// lowercase ("d8:fc:93:..."), gây sai lệch case nếu so sánh trực tiếp
// (ID-005 MAC match, Phần VIII mục 4) — không phải giả định, phát hiện
// khi chạy thật.
func parseNet0MAC(net0 string) string {
	firstField, _, _ := strings.Cut(net0, ",")
	_, mac, ok := strings.Cut(firstField, "=")
	if !ok {
		return ""
	}
	return strings.ToLower(mac)
}

// GuestPing gọi POST /nodes/{node}/qemu/{vmid}/agent/ping — đồng bộ,
// không trả task. Lỗi khi QGA chưa sẵn sàng được map về
// GUEST_AGENT_UNAVAILABLE thay vì lỗi chung, để caller phân biệt được
// "cần chờ thêm" với lỗi thật (Phần III mục 8.2, WAITING_GUEST).
func (a *Adapter) GuestPing(ctx context.Context, ref VMRef) error {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/ping", ref.Node, ref.VMID)
	_, err := a.client.do(ctx, "guest_ping", "POST", path, url.Values{})
	if err == nil {
		return nil
	}
	var pveErr *Error
	if errors.As(err, &pveErr) && pveErr.Code == CodeUnknown {
		return newGuestAgentUnavailableError(pveErr.Message)
	}
	return err
}

// WaitForTask poll GetTask theo backoff Phần III mục 10
// (1s, 2s, 3s, 5s, 8s, sau đó 10s), tới khi task terminal hoặc hết
// overallTimeout. Không dùng sleep cố định đơn (guardrail Phần II mục 18).
func (a *Adapter) WaitForTask(ctx context.Context, task TaskRef, overallTimeout time.Duration) (TaskStatus, error) {
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 3 * time.Second, 5 * time.Second, 8 * time.Second}
	deadline := time.Now().Add(overallTimeout)
	attempt := 0

	for {
		status, err := a.GetTask(ctx, task)
		if err != nil {
			return TaskStatus{}, err
		}
		if status.Done() {
			return status, nil
		}
		if time.Now().After(deadline) {
			return status, &Error{Code: CodeTaskUnknown, Message: "task did not finish within timeout", HTTPStatus: 0}
		}

		wait := 10 * time.Second
		if attempt < len(backoff) {
			wait = backoff[attempt]
		}
		attempt++

		select {
		case <-ctx.Done():
			return TaskStatus{}, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func boolParam(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func buildNet0(n NetConfig) string {
	s := "virtio,bridge=" + n.Bridge
	if n.Firewall {
		s += ",firewall=1"
	}
	return s
}

// parseUPID giải nén response Clone/Configure/status/*/Delete — tất cả
// trả một chuỗi UPID JSON-encode đơn giản (vd "\"UPID:...\"").
func parseUPID(data json.RawMessage) (string, error) {
	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return "", fmt.Errorf("proxmox: decode upid: %w", err)
	}
	if upid == "" {
		return "", fmt.Errorf("proxmox: empty upid in response")
	}
	return upid, nil
}

// parseFlexibleInt giải nén /cluster/nextid — theo schema thật trả
// integer, nhưng PVE lịch sử có phiên bản trả string; chấp nhận cả hai.
func parseFlexibleInt(data json.RawMessage) (int, error) {
	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		return asInt, nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		n, err := strconv.Atoi(asString)
		if err != nil {
			return 0, fmt.Errorf("proxmox: parse nextid string %q: %w", asString, err)
		}
		return n, nil
	}
	return 0, fmt.Errorf("proxmox: unrecognized nextid response: %s", string(data))
}
