package domain

import "errors"

// Sentinel error dùng chung giữa storage repositories và caller (state
// engine, API handler ở các epic sau). Map 1:1 về error code trong error
// envelope (Phần II mục 10) tại tầng gọi, domain không tự biết HTTP status.
var (
	// ErrNotFound báo hiệu resource không tồn tại.
	ErrNotFound = errors.New("domain: resource not found")

	// ErrIdempotencyConflict báo hiệu cùng Idempotency-Key nhưng request
	// hash khác — khớp IDEMPOTENCY_CONFLICT (Phần II mục 10).
	ErrIdempotencyConflict = errors.New("domain: idempotency key conflict")

	// ErrCapacityExhausted báo hiệu không còn resource để reserve (IP
	// pool hết, ...) — khớp CAPACITY_UNAVAILABLE (Phần V mục 4.2).
	ErrCapacityExhausted = errors.New("domain: capacity exhausted")

	// ErrAlreadyLeased báo hiệu job đã bị worker khác lease còn hiệu lực.
	ErrAlreadyLeased = errors.New("domain: job already leased")

	// ErrNotClaimable báo hiệu job không ở state cho phép claim (không
	// phải QUEUED/RETRY_WAIT, hoặc next_attempt_at chưa tới).
	ErrNotClaimable = errors.New("domain: job not claimable")

	// ErrLeaseLost báo hiệu worker không còn giữ lease hợp lệ cho job
	// (hết hạn hoặc bị worker khác tiếp quản) khi heartbeat/complete/fail.
	ErrLeaseLost = errors.New("domain: job lease lost")
)
