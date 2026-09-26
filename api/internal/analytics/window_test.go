package analytics

import (
	"testing"
	"time"
)

func date(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

func TestSyncWindowLookBackAndBackfill(t *testing.T) {
	end := date("2026-09-24")
	cases := []struct {
		name     string
		cursor   *time.Time
		backfill time.Time
		want     Window
		ok       bool
	}{
		{"first sync backfills", nil, date("2026-09-01"), Window{date("2026-09-01"), end}, true},
		{"cursor re-reads three days", ptr(date("2026-09-20")), date("2026-01-01"), Window{date("2026-09-17"), end}, true},
		{"look-back never before backfill start", ptr(date("2026-09-02")), date("2026-09-01"), Window{date("2026-09-01"), end}, true},
		{"cursor at end still re-reads", ptr(end), date("2026-01-01"), Window{date("2026-09-21"), end}, true},
		{"backfill after end is empty", nil, date("2026-09-25"), Window{date("2026-09-25"), end}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := SyncWindow(c.cursor, c.backfill, end)
			if ok != c.ok || (ok && got != c.want) {
				t.Fatalf("SyncWindow = %v %v, want %v %v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestWindowDays(t *testing.T) {
	if n := (Window{date("2026-09-01"), date("2026-09-30")}).Days(); n != 30 {
		t.Fatalf("Days = %d, want 30", n)
	}
	if n := (Window{date("2026-09-02"), date("2026-09-01")}).Days(); n != 0 {
		t.Fatalf("empty Days = %d", n)
	}
}

func TestLastCompleteDayIsPacific(t *testing.T) {
	// 2026-09-25 03:00 UTC is still 2026-09-24 in Pacific time, so the
	// last complete Pacific day is the 23rd.
	got := LastCompleteDay(time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC))
	if !got.Equal(date("2026-09-23")) {
		t.Fatalf("LastCompleteDay = %v", got)
	}
}

func TestChannelWindowCoversYPPYear(t *testing.T) {
	end := date("2026-09-24")
	w, ok := ChannelWindow(nil, end)
	if !ok || w.Days() != ChannelBackfillDays || !w.To.Equal(end) {
		t.Fatalf("ChannelWindow = %v (%d days)", w, w.Days())
	}
}

func TestVideoWindow(t *testing.T) {
	end := date("2026-09-24")
	cursor := ptr(date("2026-09-20"))
	// Published 2026-09-10 17:00 PDT = 2026-09-11 00:00 UTC: Pacific day 10th.
	published := ptr(time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC))

	w, _ := VideoWindow(cursor, published, true, end)
	if !w.From.Equal(date("2026-09-17")) {
		t.Fatalf("video with history: from %v, want look-back from cursor", w.From)
	}
	w, _ = VideoWindow(cursor, published, false, end)
	if !w.From.Equal(date("2026-09-10")) {
		t.Fatalf("new video: from %v, want its Pacific publication day", w.From)
	}
	w, _ = VideoWindow(nil, nil, false, end)
	if w.Days() != ChannelBackfillDays {
		t.Fatalf("unknown publication: %d days", w.Days())
	}
}
