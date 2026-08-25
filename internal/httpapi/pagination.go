package httpapi

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultPageLimit/MaxPageLimit theo Phần II mục 10 ("Pagination
// cursor-based") — mặc định vừa phải, chặn client tự ý kéo toàn bộ
// bảng trong một lần gọi.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// Cursor là vị trí keyset pagination (created_at, id) — encode/decode
// thành opaque string cho client, khớp OpenAPI "next_cursor: string|null".
type Cursor struct {
	After   time.Time
	AfterID string
}

// EncodeCursor mã hoá vị trí thành chuỗi opaque (client không cần —
// không nên diễn giải nội dung).
func EncodeCursor(after time.Time, afterID string) string {
	raw := after.UTC().Format(time.RFC3339Nano) + "|" + afterID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor giải mã cursor từ query param — chuỗi rỗng nghĩa là
// "từ đầu" (Cursor zero-value), không phải lỗi.
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor encoding")
	}
	after, afterID, ok := strings.Cut(string(raw), "|")
	if !ok || afterID == "" {
		return Cursor{}, fmt.Errorf("invalid cursor format")
	}
	t, err := time.Parse(time.RFC3339Nano, after)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor timestamp")
	}
	return Cursor{After: t, AfterID: afterID}, nil
}

// PageParams đọc "cursor" và "limit" từ query string, áp default/max.
func PageParams(r *http.Request) (Cursor, int, error) {
	cursor, err := DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return Cursor{}, 0, err
	}
	limit := DefaultPageLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Cursor{}, 0, fmt.Errorf("invalid limit")
		}
		limit = n
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	return cursor, limit, nil
}

// Paginate cắt items về tối đa limit, trả next_cursor dựa trên item
// cuối nếu items dài hơn limit — quy ước: caller LUÔN gọi repository
// List với limit+1 (Paginate tự phát hiện "còn trang sau" từ đó, không
// cần COUNT(*) riêng).
func Paginate[T any](items []T, limit int, createdAt func(T) time.Time, id func(T) string) (page []T, nextCursor *string) {
	if len(items) > limit {
		page = items[:limit]
		c := EncodeCursor(createdAt(page[limit-1]), id(page[limit-1]))
		return page, &c
	}
	return items, nil
}
