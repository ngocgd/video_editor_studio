package analyticsapi

import (
	"errors"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"loomtale/api/internal/analytics"
)

func TestParseVideoID(t *testing.T) {
	const id = "dQw4w9WgXcQ"
	ok := []string{
		id,
		"  " + id + " ",
		"https://www.youtube.com/watch?v=" + id,
		"https://www.youtube.com/watch?v=" + id + "&t=42s&list=PL1",
		"youtube.com/watch?v=" + id,
		"https://m.youtube.com/watch?v=" + id,
		"https://youtu.be/" + id + "?si=abc",
		"https://www.youtube.com/shorts/" + id,
		"https://www.youtube.com/embed/" + id,
		"https://www.youtube.com/live/" + id + "?feature=share",
		"https://www.youtube-nocookie.com/embed/" + id,
		"https://YOUTU.BE/" + id,
	}
	for _, in := range ok {
		got, err := ParseVideoID(in)
		if err != nil || got != id {
			t.Errorf("ParseVideoID(%q) = %q, %v; want %q", in, got, err, id)
		}
	}
	bad := []string{
		"",
		"dQw4w9WgXc",   // 10 characters
		"dQw4w9WgXcQQ", // 12 characters
		"https://evil.example/watch?v=" + id,
		"https://www.youtube.com.evil.example/watch?v=" + id,
		"https://www.youtube.com/watch?x=" + id,
		"https://www.youtube.com/channel/UC1234567890",
		"https://www.youtube.com/shorts/",
		"https://www.youtube.com/watch?v=" + id + "%22%3E",
		"javascript:alert(1)",
	}
	for _, in := range bad {
		if got, err := ParseVideoID(in); !errors.Is(err, ErrNotAVideo) {
			t.Errorf("ParseVideoID(%q) = %q, %v; want ErrNotAVideo", in, got, err)
		}
	}
}

func TestWindowDefaultsAndBounds(t *testing.T) {
	through := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	h := &AnalyticsAPI{Aggregator: &analytics.Aggregator{Now: func() time.Time { return through.AddDate(0, 0, 5) }}}
	st := analytics.SyncStatus{AnalyticsThrough: &through}
	d := func(y int, m time.Month, day int) *openapi_types.Date {
		return &openapi_types.Date{Time: time.Date(y, m, day, 0, 0, 0, 0, time.UTC)}
	}

	w, err := h.window(st, nil, nil)
	if err != nil || !w.To.Equal(through) || w.Days() != DefaultWindowDays {
		t.Fatalf("default window = %v..%v (%d days), %v", w.From, w.To, w.Days(), err)
	}
	w, err = h.window(st, nil, d(2026, 8, 31))
	if err != nil || w.Days() != DefaultWindowDays || !w.To.Equal(d(2026, 8, 31).Time) {
		t.Fatalf("to-only window = %v..%v, %v", w.From, w.To, err)
	}
	w, err = h.window(st, d(2026, 9, 1), d(2026, 9, 1))
	if err != nil || w.Days() != 1 {
		t.Fatalf("one-day window = %v..%v, %v", w.From, w.To, err)
	}
	if _, err := h.window(st, d(2026, 9, 2), d(2026, 9, 1)); !errors.Is(err, errBadWindow) {
		t.Fatalf("reversed window: err = %v", err)
	}
	if _, err := h.window(st, d(2025, 9, 1), d(2026, 9, 20)); !errors.Is(err, errBadWindow) {
		t.Fatalf("over-long window: err = %v", err)
	}
	if _, err := h.window(st, d(2025, 9, 20), d(2026, 9, 20)); err != nil {
		t.Fatalf("366-day window refused: %v", err)
	}
}
