package httpapi

import "net/http"

// isCanonicalUUID kiểm tra dạng UUID chuẩn 8-4-4-4-12 (chữ hex, dấu
// gạch ở vị trí 8/13/18/23). Chỉ chấp nhận dạng canonical — đúng dạng
// mà API trả ra trong mọi response; các biến thể Postgres chấp nhận
// (ngoặc nhọn, bỏ gạch, urn:) không phải ID hợp lệ ở tầng API.
func isCanonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

// ValidateUUIDParam là middleware chặn path param {id} không phải UUID
// trước khi xuống repository — mọi resource có {id} đều là UUID PK
// (migrations/000001+), nên id sai format mà truyền thô xuống sẽ làm
// Postgres fail cast ::uuid và bị writeGetError/writeMutationError phân
// loại nhầm thành 500 INTERNAL. Trả 400 INVALID_ID thay vì để lộ lỗi
// internal. Route không có {id} đi qua nguyên vẹn (PathValue trả "").
func ValidateUUIDParam(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.PathValue("id"); id != "" && !isCanonicalUUID(id) {
			WriteError(w, r, http.StatusBadRequest, "INVALID_ID", "id path parameter must be a valid UUID")
			return
		}
		next.ServeHTTP(w, r)
	})
}
