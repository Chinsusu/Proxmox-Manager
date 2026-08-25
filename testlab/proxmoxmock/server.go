// Package proxmoxmock implement một Proxmox API server giả (httptest)
// đủ để lái internal/proxmox.Client/Adapter qua các kịch bản chaos ở
// docs/appendices/acceptance_test_matrix.csv (PVE-xxx) mà KHÔNG cần
// cluster Proxmox thật — theo Epic P0-11 (Test Lab & Chaos), mục
// "mocks"/"chaos scripts". Chỉ implement đúng tập endpoint
// internal/proxmox gọi (xem client.go/adapter.go/guest.go), không phải
// một Proxmox emulator đầy đủ.
//
// Task model: mọi mutation (clone/configure/start/stop/delete) tạo một
// task "PendingPolls" lần poll đầu tiên còn "running", polls tiếp theo
// mới "stopped" — mô phỏng đúng nhịp async thật của Proxmox (không trả
// xong ngay ở poll đầu, khớp Phần III mục 10 "task polling").
package proxmoxmock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
)

// Server là một Proxmox API mock chạy trên httptest.Server thật (lắng
// nghe TCP loopback) — trỏ proxmox.ClientConfig.BaseURL vào Server.URL.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	vms      map[int]*vmState
	tasks    map[string]*taskState
	taskSeq  int
	nextVMID int

	// Đếm số lần từng operation ĐƯỢC GỌI THẬT (khác task polling) — dùng
	// để assert "no duplicate side effect on resume" (PVE-003) và "no
	// duplicate start" (PVE-006).
	CloneCalls     int
	ConfigureCalls int
	StartCalls     int
	StopCalls      int
	DeleteCalls    int

	// *Err, khi khác nil, làm request TIẾP THEO tới operation đó trả lỗi
	// (status+body chỉ định) THAY VÌ hành vi mặc định — tự động clear về
	// nil sau khi dùng một lần (mô phỏng lỗi "một lần" rồi retry thành
	// công, khớp phần lớn kịch bản PVE-xxx).
	CloneErr     *InjectedError
	ConfigureErr *InjectedError
}

// InjectedError mô tả response lỗi mock trả về — Body nên chứa đúng
// substring mà internal/proxmox.classifyError nhận diện (vd "already
// exists", "bridge ... not found", "no space left").
type InjectedError struct {
	Status int
	Body   string
}

type vmState struct {
	node    string
	running bool
	locked  string
	net0MAC string
}

type taskState struct {
	node           string
	pollsRemaining int
	finalStatus    string // "OK" hoac thong bao loi
}

// NewServer khởi tạo mock server đang chạy — gọi Close() (qua
// httptest.Server nhúng) khi test xong.
func NewServer() *Server {
	s := &Server{
		vms:      make(map[int]*vmState),
		tasks:    make(map[string]*taskState),
		nextVMID: 900,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cluster/nextid", s.handleNextID)
	mux.HandleFunc("POST /nodes/{node}/qemu/{vmid}/clone", s.handleClone)
	mux.HandleFunc("POST /nodes/{node}/qemu/{vmid}/config", s.handleConfigurePost)
	mux.HandleFunc("GET /nodes/{node}/qemu/{vmid}/config", s.handleConfigureGet)
	mux.HandleFunc("POST /nodes/{node}/qemu/{vmid}/status/start", s.handleStatusAction("start"))
	mux.HandleFunc("POST /nodes/{node}/qemu/{vmid}/status/stop", s.handleStatusAction("stop"))
	mux.HandleFunc("DELETE /nodes/{node}/qemu/{vmid}", s.handleDelete)
	mux.HandleFunc("GET /nodes/{node}/qemu/{vmid}/status/current", s.handleStatusCurrent)
	mux.HandleFunc("GET /nodes/{node}/tasks/{upid}/status", s.handleTaskStatus)
	s.Server = httptest.NewServer(mux)
	return s
}

// RegisterVM đăng ký trước một VM đã tồn tại (mô phỏng VM đã clone
// xong ở lần chạy trước, hoặc VM template nguồn) — dùng cho kịch bản
// "worker resume from checkpoint" (PVE-003) và "VM already running"
// (PVE-006).
func (s *Server) RegisterVM(vmid int, node string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vms[vmid] = &vmState{node: node, running: running}
}

// RegisterCompletedTask đăng ký trước một task ĐÃ XONG (poll đầu tiên
// đã "stopped") — dùng để mô phỏng worker resume vào một task mà lần
// chạy trước đã tạo và Proxmox đã hoàn tất trong lúc worker "chết".
func (s *Server) RegisterCompletedTask(upid, node, finalStatus string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[upid] = &taskState{node: node, pollsRemaining: 0, finalStatus: finalStatus}
}

func (s *Server) newTask(node, finalStatus string) string {
	s.taskSeq++
	upid := fmt.Sprintf("UPID:%s:MOCK:%08X::qmclone:mock:root@pam:", node, s.taskSeq)
	// pollsRemaining=1: poll dau tien con "running", poll thu hai moi
	// "stopped" - khop nhip async that (Phan III muc 10).
	s.tasks[upid] = &taskState{node: node, pollsRemaining: 1, finalStatus: finalStatus}
	return upid
}

func writeError(w http.ResponseWriter, status int, body string) {
	w.WriteHeader(status)
	// gosec G705: body la string test tu viet (InjectedError.Body hoac
	// thong bao loi mock tu tao trong package nay) hoac path param request
	// noi bo trong test process, khong phai input tu nguoi dung/attacker
	// that - khong co rui ro XSS trong mot mock server chi httptest dung noi bo.
	_, _ = w.Write([]byte(body)) //nolint:gosec // G705: response body la du lieu test tu wiring, khong phai user input
}

func writeData(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func (s *Server) handleNextID(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextVMID++
	writeData(w, s.nextVMID)
}

func (s *Server) handleClone(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CloneCalls++

	if s.CloneErr != nil {
		err := s.CloneErr
		s.CloneErr = nil
		writeError(w, err.Status, err.Body)
		return
	}

	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	node := r.PathValue("node")
	newIDStr := r.Form.Get("newid")
	var newID int
	_, _ = fmt.Sscanf(newIDStr, "%d", &newID)

	if existing, ok := s.vms[newID]; ok && existing != nil {
		writeError(w, 500, fmt.Sprintf("unable to create VM %d - VM %d already exists", newID, newID))
		return
	}

	s.vms[newID] = &vmState{node: node, running: false}
	upid := s.newTask(node, "OK")
	writeData(w, upid)
}

func (s *Server) handleConfigurePost(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ConfigureCalls++

	if s.ConfigureErr != nil {
		err := s.ConfigureErr
		s.ConfigureErr = nil
		writeError(w, err.Status, err.Body)
		return
	}

	node := r.PathValue("node")
	var vmid int
	_, _ = fmt.Sscanf(r.PathValue("vmid"), "%d", &vmid)
	if vm, ok := s.vms[vmid]; ok {
		vm.net0MAC = "de:ad:be:ef:00:01"
	}
	upid := s.newTask(node, "OK")
	writeData(w, upid)
}

func (s *Server) handleConfigureGet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var vmid int
	_, _ = fmt.Sscanf(r.PathValue("vmid"), "%d", &vmid)
	vm := s.vms[vmid]
	mac := "de:ad:be:ef:00:01"
	if vm != nil && vm.net0MAC != "" {
		mac = vm.net0MAC
	}
	writeData(w, map[string]string{"net0": "virtio=" + mac + ",bridge=vmbr0,firewall=1"})
}

func (s *Server) handleStatusAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		node := r.PathValue("node")
		var vmid int
		_, _ = fmt.Sscanf(r.PathValue("vmid"), "%d", &vmid)
		switch action {
		case "start":
			s.StartCalls++
		case "stop":
			s.StopCalls++
		}
		upid := s.newTask(node, "OK")
		if vm, ok := s.vms[vmid]; ok {
			vm.running = action == "start"
		}
		writeData(w, upid)
	}
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DeleteCalls++
	node := r.PathValue("node")
	var vmid int
	_, _ = fmt.Sscanf(r.PathValue("vmid"), "%d", &vmid)
	if _, ok := s.vms[vmid]; !ok {
		writeError(w, 500, fmt.Sprintf("Configuration file 'nodes/%s/qemu/%d.conf' does not exist", node, vmid))
		return
	}
	delete(s.vms, vmid)
	upid := s.newTask(node, "OK")
	writeData(w, upid)
}

func (s *Server) handleStatusCurrent(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var vmid int
	_, _ = fmt.Sscanf(r.PathValue("vmid"), "%d", &vmid)
	vm, ok := s.vms[vmid]
	if !ok {
		writeError(w, 500, fmt.Sprintf("no such VM %d", vmid))
		return
	}
	status := "stopped"
	if vm.running {
		status = "running"
	}
	writeData(w, map[string]string{"name": fmt.Sprintf("mock-vm-%d", vmid), "status": status, "lock": vm.locked})
}

func (s *Server) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	upid := r.PathValue("upid")
	t, ok := s.tasks[upid]
	if !ok {
		writeError(w, 500, "no such task")
		return
	}
	if t.pollsRemaining > 0 {
		t.pollsRemaining--
		writeData(w, map[string]string{"status": "running", "exitstatus": ""})
		return
	}
	writeData(w, map[string]string{"status": "stopped", "exitstatus": t.finalStatus})
}
