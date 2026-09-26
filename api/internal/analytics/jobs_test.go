package analytics

import (
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"loomtale/api/internal/youtube"
)

func TestDailyScheduleRunsAtNineBangkok(t *testing.T) {
	cases := []struct{ now, want time.Time }{
		// 01:00 UTC = 08:00 ICT: today 09:00 ICT = 02:00 UTC.
		{time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC), time.Date(2026, 9, 25, 2, 0, 0, 0, time.UTC)},
		// Exactly 09:00 ICT: the next run is tomorrow.
		{time.Date(2026, 9, 25, 2, 0, 0, 0, time.UTC), time.Date(2026, 9, 26, 2, 0, 0, 0, time.UTC)},
		// 20:00 UTC = 03:00 ICT the next day.
		{time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC), time.Date(2026, 9, 26, 2, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := DailySchedule.Next(c.now); !got.Equal(c.want) {
			t.Errorf("Next(%v) = %v, want %v", c.now, got.UTC(), c.want)
		}
	}
}

func TestJobOutcome(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if err := jobOutcome(nil, now); err != nil {
		t.Fatalf("success: %v", err)
	}
	if err := jobOutcome(ErrSyncRunning, now); err != nil {
		t.Fatalf("already running: %v", err)
	}
	if err := jobOutcome(ErrChannelNotConnected, now); err != nil {
		t.Fatalf("channel not connected: %v", err)
	}

	var snooze *river.JobSnoozeError
	quota := &youtube.APIError{Kind: youtube.KindQuota, Status: 403, Reason: "quotaExceeded"}
	if err := jobOutcome(quota, now); !errors.As(err, &snooze) {
		t.Fatalf("quota: %v, want snooze", err)
	} else if want := youtube.NextReset(now).Sub(now) + quotaSnoozeSlack; snooze.Duration != want {
		t.Fatalf("snooze %v, want %v", snooze.Duration, want)
	}

	var cancel *river.JobCancelError
	auth := &youtube.APIError{Kind: youtube.KindAuth, Reason: youtube.ReasonReconnectNeeded}
	if err := jobOutcome(auth, now); !errors.As(err, &cancel) {
		t.Fatalf("auth: %v, want cancel", err)
	}

	transient := &youtube.APIError{Kind: youtube.KindTransient, Status: 503}
	if err := jobOutcome(transient, now); !errors.Is(err, transient) || errors.As(err, &snooze) {
		t.Fatalf("transient: %v, want the error for a retry", err)
	}
}

func TestSyncArgsUniquePerChannel(t *testing.T) {
	opts := SyncArgs{}.InsertOpts()
	if opts.Queue != QueueAnalytics || !opts.UniqueOpts.ByArgs {
		t.Fatalf("opts = %+v", opts)
	}
	if len(PeriodicJobs()) != 1 {
		t.Fatal("want one periodic job")
	}
}

func TestPickReachReportType(t *testing.T) {
	cases := []struct {
		types []ReportTypeCandidate
		want  string
		ok    bool
	}{
		{[]ReportTypeCandidate{{ID: "channel_basic_a2"}, {ID: ReachReportTypeID}}, ReachReportTypeID, true},
		{[]ReportTypeCandidate{{ID: ReachReportTypeID, Deprecated: true}, {ID: "channel_reach_basic_a2"}}, "channel_reach_basic_a2", true},
		{[]ReportTypeCandidate{{ID: "channel_basic_a2"}}, "", false},
	}
	for _, c := range cases {
		got, ok := PickReachReportType(c.types)
		if got != c.want || ok != c.ok {
			t.Errorf("PickReachReportType(%v) = %q %v", c.types, got, ok)
		}
	}
}
