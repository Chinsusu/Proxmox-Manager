package proxmox

import (
	"fmt"
	"strings"
)

// Error là lỗi đã map về error code ổn định theo bảng Error Mapping ở
// Phần III mục 11. Message giữ nguyên văn Proxmox trả về để debug,
// nhưng caller (API/CLI layer) chỉ nên hiển thị Code cho client bên ngoài.
type Error struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *Error) Error() string {
	return fmt.Sprintf("proxmox: %s (http %d): %s", e.Code, e.HTTPStatus, e.Message)
}

// Mã lỗi ổn định, khớp nguyên văn bảng ở Phần III mục 11.
const (
	CodeAuthFailed        = "PVE_AUTH_FAILED"
	CodeVMIDConflict      = "PVE_VMID_CONFLICT"
	CodeBridgeNotFound    = "PVE_BRIDGE_NOT_FOUND"
	CodeStorageCapacity   = "PVE_STORAGE_CAPACITY"
	CodeTaskUnknown       = "PVE_TASK_UNKNOWN"
	CodeVMLocked          = "PVE_VM_LOCKED"
	CodeTemplateInvalid   = "PVE_TEMPLATE_INVALID"
	CodeGuestAgentUnavail = "GUEST_AGENT_UNAVAILABLE"
	// CodeUnknown dùng khi response không khớp pattern nào đã biết —
	// caller nên coi là retryable transient theo Failure Taxonomy
	// (Phần II mục 13), không phải permanent.
	CodeUnknown = "PVE_UNKNOWN"
)

// classifyError map (status, body) từ Proxmox API response về Error
// theo heuristic tốt nhất có thể — message lỗi của Proxmox là free-text
// nên chỉ match được bằng substring; danh sách match dựa trên các
// pattern thật quan sát được khi verify với cluster PVE 9.1.6, cần bổ
// sung thêm nếu gặp pattern mới trong quá trình vận hành thật.
func classifyError(httpStatus int, body string) *Error {
	lower := strings.ToLower(body)

	switch {
	case httpStatus == 401 || httpStatus == 403:
		return &Error{Code: CodeAuthFailed, Message: body, HTTPStatus: httpStatus}
	case strings.Contains(lower, "already exists"):
		return &Error{Code: CodeVMIDConflict, Message: body, HTTPStatus: httpStatus}
	case strings.Contains(lower, "bridge") && (strings.Contains(lower, "does not exist") || strings.Contains(lower, "not found")):
		return &Error{Code: CodeBridgeNotFound, Message: body, HTTPStatus: httpStatus}
	case strings.Contains(lower, "no space left") || strings.Contains(lower, "not enough space") || strings.Contains(lower, "insufficient"):
		return &Error{Code: CodeStorageCapacity, Message: body, HTTPStatus: httpStatus}
	case strings.Contains(lower, "locked"):
		return &Error{Code: CodeVMLocked, Message: body, HTTPStatus: httpStatus}
	case strings.Contains(lower, "unable to parse") && strings.Contains(lower, "config"):
		return &Error{Code: CodeTemplateInvalid, Message: body, HTTPStatus: httpStatus}
	default:
		return &Error{Code: CodeUnknown, Message: body, HTTPStatus: httpStatus}
	}
}

// ErrGuestAgentUnavailable báo hiệu QGA không phản hồi — dùng ở
// GuestPing khi response Proxmox báo agent chưa sẵn sàng (thường 500
// kèm "QEMU guest agent is not running").
func newGuestAgentUnavailableError(body string) *Error {
	return &Error{Code: CodeGuestAgentUnavail, Message: body, HTTPStatus: 500}
}
