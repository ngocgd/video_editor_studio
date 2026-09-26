package analytics

import (
	"time"

	"loomtale/api/internal/youtube"
)

const (
	// LookBackDays is how many days before the stored cursor are fetched
	// again: YouTube revises the most recent days after first reporting
	// them.
	LookBackDays = 3
	// ChannelBackfillDays covers the YouTube Partner Program's rolling
	// 12-month watch-hours window on a channel's first sync.
	ChannelBackfillDays = 365
)

// day is a calendar day as a UTC midnight, the form PTDate returns and
// the Analytics API date parameters expect.
func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// LastCompleteDay is the last Pacific-time day that has fully closed at
// now: Analytics data is reported in Pacific days.
func LastCompleteDay(now time.Time) time.Time {
	return youtube.PTDate(now).AddDate(0, 0, -1)
}

// Window is an inclusive range of calendar days.
type Window struct {
	From, To time.Time
}

// Days is the number of days in w (zero for an empty window).
func (w Window) Days() int {
	if w.To.Before(w.From) {
		return 0
	}
	return int(w.To.Sub(w.From).Hours()/24) + 1
}

// SyncWindow returns the days to fetch up to end. With a cursor (the
// last day already synced) it re-reads LookBackDays before it; without
// one it backfills from backfillFrom. The window never starts before
// backfillFrom. ok is false when there is nothing to fetch.
func SyncWindow(cursor *time.Time, backfillFrom, end time.Time) (w Window, ok bool) {
	from := day(backfillFrom)
	if cursor != nil {
		if c := day(*cursor).AddDate(0, 0, -LookBackDays); c.After(from) {
			from = c
		}
	}
	w = Window{From: from, To: day(end)}
	return w, w.Days() > 0
}

// ChannelWindow is the window of a channel's daily totals.
func ChannelWindow(cursor *time.Time, end time.Time) (Window, bool) {
	return SyncWindow(cursor, day(end).AddDate(0, 0, -(ChannelBackfillDays-1)), end)
}

// VideoWindow is the window of one tracked video's daily metrics. A video
// with no stored history yet (newly tracked) is backfilled from its
// publication day even when the channel has a cursor. An unknown
// publication day backfills ChannelBackfillDays.
func VideoWindow(channelCursor, publishedAt *time.Time, hasHistory bool, end time.Time) (Window, bool) {
	backfill := day(end).AddDate(0, 0, -(ChannelBackfillDays - 1))
	if publishedAt != nil {
		backfill = youtube.PTDate(*publishedAt)
	}
	cursor := channelCursor
	if !hasHistory {
		cursor = nil
	}
	return SyncWindow(cursor, backfill, end)
}
