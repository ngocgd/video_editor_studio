package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// YouTube Partner Program thresholds (long-form path): public watch hours
// over the last 12 months and subscribers.
const (
	YPPWatchHours  = 4000
	YPPSubscribers = 1000
)

// ErrNotTracked means the video is not tracked by the tenant.
var ErrNotTracked = errors.New("analytics: video is not tracked")

// Aggregator serves the read models of the Analytics page from the
// pre-aggregated daily tables and recomputes suggestions. Every read is
// scoped to a tenant; callers check the channel belongs to it.
type Aggregator struct {
	Pool    *pgxpool.Pool
	Queries *dbgen.Queries
	// Now defaults to time.Now.
	Now func() time.Time
}

func (a *Aggregator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// SyncStatus is a channel's sync state; the through dates are the
// "data through <date>" stamps of each source (nil before any data).
type SyncStatus struct {
	AnalyticsThrough *time.Time
	ReachThrough     *time.Time
	Status           string
	LastStartedAt    *time.Time
	LastFinishedAt   *time.Time
	LastError        string
	SubscriberCount  *int64
}

// SyncStatus reads a channel's sync state; a never-synced channel is idle.
func (a *Aggregator) SyncStatus(ctx context.Context, tenantID, channelID uuid.UUID) (SyncStatus, error) {
	st, err := a.Queries.GetAnalyticsSyncState(ctx, dbgen.GetAnalyticsSyncStateParams{
		TenantID: idconv.ToPg(tenantID), ChannelID: idconv.ToPg(channelID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncStatus{Status: "idle"}, nil
	}
	if err != nil {
		return SyncStatus{}, fmt.Errorf("analytics: read sync state: %w", err)
	}
	return SyncStatus{
		AnalyticsThrough: datePtr(st.AnalyticsThrough),
		ReachThrough:     datePtr(st.ReachThrough),
		Status:           st.Status,
		LastStartedAt:    tsPtr(st.LastStartedAt),
		LastFinishedAt:   tsPtr(st.LastFinishedAt),
		LastError:        st.LastError,
		SubscriberCount:  int8Ptr(st.SubscriberCount),
	}, nil
}

// DefaultWindow is the last days days up to the newest synced day (or
// the last complete Pacific day before the first sync).
func (a *Aggregator) DefaultWindow(st SyncStatus, days int) Window {
	end := LastCompleteDay(a.now())
	if st.AnalyticsThrough != nil {
		end = day(*st.AnalyticsThrough)
	}
	return Window{From: end.AddDate(0, 0, -(days - 1)), To: end}
}

// ChannelDay is one day of channel totals; nil is not available.
type ChannelDay struct {
	Date              time.Time
	Views             *int64
	WatchHours        *float64
	SubscribersGained *int64
	SubscribersLost   *int64
	Unavailable       map[string]string
}

// YPPProgress is the channel's progress towards the Partner Program.
type YPPProgress struct {
	WatchHours        float64
	WatchHoursTarget  int
	WindowFrom        time.Time
	WindowTo          time.Time
	DaysMissing       int
	Subscribers       *int64
	SubscribersTarget int
}

// Overview is the channel view of the Analytics page and the Dashboard.
type Overview struct {
	Sync   SyncStatus
	Window Window
	Days   []ChannelDay
	YPP    YPPProgress
}

// Overview reads a channel's daily series over w and its YPP progress
// over the 12 months ending at the newest synced day.
func (a *Aggregator) Overview(ctx context.Context, tenantID, channelID uuid.UUID, st SyncStatus, w Window) (Overview, error) {
	tenant, channel := idconv.ToPg(tenantID), idconv.ToPg(channelID)
	rows, err := a.Queries.ChannelDailySeries(ctx, dbgen.ChannelDailySeriesParams{
		TenantID: tenant, ChannelID: channel, FromDate: pgDate(w.From), ToDate: pgDate(w.To),
	})
	if err != nil {
		return Overview{}, fmt.Errorf("analytics: channel series: %w", err)
	}
	out := Overview{Sync: st, Window: w, Days: make([]ChannelDay, 0, len(rows))}
	for _, r := range rows {
		d := ChannelDay{
			Date: r.Date.Time, Views: int8Ptr(r.Views), SubscribersGained: int8Ptr(r.SubscribersGained),
			SubscribersLost: int8Ptr(r.SubscribersLost), Unavailable: decodeReasons(r.Unavailable),
		}
		if m := float8Ptr(r.EstimatedMinutesWatched); m != nil {
			h := *m / 60
			d.WatchHours = &h
		}
		out.Days = append(out.Days, d)
	}

	ypp := a.DefaultWindow(st, ChannelBackfillDays)
	wh, err := a.Queries.ChannelWatchHoursWindow(ctx, dbgen.ChannelWatchHoursWindowParams{
		TenantID: tenant, ChannelID: channel, FromDate: pgDate(ypp.From), ToDate: pgDate(ypp.To),
	})
	if err != nil {
		return Overview{}, fmt.Errorf("analytics: watch hours: %w", err)
	}
	out.YPP = YPPProgress{
		WatchHours: wh.WatchHours, WatchHoursTarget: YPPWatchHours,
		WindowFrom: ypp.From, WindowTo: ypp.To, DaysMissing: int(wh.DaysMissing),
		Subscribers: st.SubscriberCount, SubscribersTarget: YPPSubscribers,
	}
	return out, nil
}

// VideoTotals is one tracked video's totals over a window; a nil metric
// was not available from the API on any day of the window.
type VideoTotals struct {
	VideoID               string
	Title                 string
	Source                string
	DurationSeconds       *int32
	PublishedAt           *time.Time
	Views                 *int64
	WatchHours            *float64
	AverageViewPercentage *float64
	AverageViewDuration   *float64
	SubscribersGained     *int64
	Impressions           *int64
	CTR                   *float64
}

// VideoTotals lists the channel's tracked videos with totals over w.
func (a *Aggregator) VideoTotals(ctx context.Context, tenantID, channelID uuid.UUID, w Window) ([]VideoTotals, error) {
	rows, err := a.Queries.ListVideoTotals(ctx, dbgen.ListVideoTotalsParams{
		TenantID: idconv.ToPg(tenantID), ChannelID: idconv.ToPg(channelID),
		FromDate: pgDate(w.From), ToDate: pgDate(w.To),
	})
	if err != nil {
		return nil, fmt.Errorf("analytics: video totals: %w", err)
	}
	out := make([]VideoTotals, 0, len(rows))
	for _, r := range rows {
		out = append(out, VideoTotals{
			VideoID: r.YoutubeVideoID, Title: r.Title, Source: r.Source,
			DurationSeconds:       int4Ptr(r.DurationSeconds),
			PublishedAt:           tsPtr(r.PublishedAt),
			Views:                 when(r.ViewsDays, r.Views),
			WatchHours:            when(r.MinutesDays, r.MinutesWatched/60),
			AverageViewPercentage: when(r.AverageViewPercentageDays, r.AverageViewPercentage),
			AverageViewDuration:   when(r.AverageViewDurationDays, r.AverageViewDuration),
			SubscribersGained:     when(r.SubscribersDays, r.SubscribersGained),
			Impressions:           when(r.ImpressionsDays, r.Impressions),
			CTR:                   when(r.CtrDays, r.Ctr),
		})
	}
	return out, nil
}
