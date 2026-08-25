package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestCursor_EncodeDecodeRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 30, 0, 123456789, time.UTC)
	encoded := EncodeCursor(now, "inst-1")

	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor() error: %v", err)
	}
	if !decoded.After.Equal(now) {
		t.Errorf("After = %v, want %v", decoded.After, now)
	}
	if decoded.AfterID != "inst-1" {
		t.Errorf("AfterID = %q, want inst-1", decoded.AfterID)
	}
}

func TestDecodeCursor_EmptyStringIsZeroCursor(t *testing.T) {
	c, err := DecodeCursor("")
	if err != nil {
		t.Fatalf("DecodeCursor(\"\") error: %v", err)
	}
	if !c.After.IsZero() || c.AfterID != "" {
		t.Errorf("cursor = %+v, want zero value", c)
	}
}

func TestDecodeCursor_InvalidInputsReturnError(t *testing.T) {
	cases := []string{"not-base64!!!", "aGVsbG8", "aW52YWxpZC10aW1lc3RhbXB8aWQ"} // "hello", "invalid-timestamp|id"
	for _, c := range cases {
		if _, err := DecodeCursor(c); err == nil {
			t.Errorf("DecodeCursor(%q) error = nil, want error", c)
		}
	}
}

func TestPageParams_Defaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/instances", nil)
	cursor, limit, err := PageParams(r)
	if err != nil {
		t.Fatalf("PageParams() error: %v", err)
	}
	if !cursor.After.IsZero() {
		t.Errorf("cursor = %+v, want zero", cursor)
	}
	if limit != DefaultPageLimit {
		t.Errorf("limit = %d, want %d", limit, DefaultPageLimit)
	}
}

func TestPageParams_LimitClampedToMax(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/instances?limit=99999", nil)
	_, limit, err := PageParams(r)
	if err != nil {
		t.Fatalf("PageParams() error: %v", err)
	}
	if limit != MaxPageLimit {
		t.Errorf("limit = %d, want clamped to %d", limit, MaxPageLimit)
	}
}

func TestPageParams_InvalidLimitReturnsError(t *testing.T) {
	for _, raw := range []string{"0", "-5", "abc"} {
		r := httptest.NewRequest("GET", "/v1/instances?limit="+raw, nil)
		if _, _, err := PageParams(r); err == nil {
			t.Errorf("PageParams() limit=%q error = nil, want error", raw)
		}
	}
}

type pageItem struct {
	id        string
	createdAt time.Time
}

func TestPaginate_NoMoreWhenUnderLimit(t *testing.T) {
	base := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	items := []pageItem{{"a", base}, {"b", base}}
	page, next := Paginate(items, 5, func(p pageItem) time.Time { return p.createdAt }, func(p pageItem) string { return p.id })
	if len(page) != 2 {
		t.Fatalf("len(page) = %d, want 2", len(page))
	}
	if next != nil {
		t.Errorf("next = %v, want nil (khong con trang sau)", *next)
	}
}

func TestPaginate_TrimsAndReturnsCursorWhenOverLimit(t *testing.T) {
	base := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	items := []pageItem{{"a", base}, {"b", base}, {"c", base}} // limit+1 = 3, limit = 2
	page, next := Paginate(items, 2, func(p pageItem) time.Time { return p.createdAt }, func(p pageItem) string { return p.id })
	if len(page) != 2 || page[0].id != "a" || page[1].id != "b" {
		t.Fatalf("page = %+v, want [a, b]", page)
	}
	if next == nil {
		t.Fatal("next = nil, want cursor (con trang sau)")
	}
	decoded, err := DecodeCursor(*next)
	if err != nil {
		t.Fatalf("DecodeCursor(next) error: %v", err)
	}
	if decoded.AfterID != "b" {
		t.Errorf("cursor AfterID = %q, want b (item cuoi TRONG page, khong phai item bi cat)", decoded.AfterID)
	}
}
