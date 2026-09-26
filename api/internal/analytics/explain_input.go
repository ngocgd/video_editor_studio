package analytics

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

// ExplainWindowDays is the window the Explain step summarises.
const ExplainWindowDays = 28

// explainMaxVideos caps the videos sent to the LLM (the most viewed).
const explainMaxVideos = 20

// ErrNothingToExplain means the channel has no synced data yet.
var ErrNothingToExplain = errors.New("analytics: the channel has no synced data to explain")

// ExplainInput is everything the Explain step sends to the LLM: only
// aggregated numbers, rule names and video ids. Titles and any other
// text that came from YouTube stay out, so channel content cannot steer
// the model.
type ExplainInput struct {
	Window   ExplainWindow    `json:"window"`
	Channel  ExplainTotals    `json:"channel"`
	Previous ExplainTotals    `json:"previousWindow"`
	YPP      ExplainYPP       `json:"ypp"`
	Videos   []ExplainVideo   `json:"videos"`
	Findings []ExplainFinding `json:"findings"`
}

// ExplainWindow is a date range as ISO dates.
type ExplainWindow struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ExplainTotals are a channel's totals over a window; nil is not
// available from the API.
type ExplainTotals struct {
	Views             *int64   `json:"views"`
	WatchHours        *float64 `json:"watchHours"`
	SubscribersGained *int64   `json:"subscribersGained"`
	SubscribersLost   *int64   `json:"subscribersLost"`
}

// ExplainYPP is the Partner Program progress.
type ExplainYPP struct {
	WatchHours        float64 `json:"watchHours"`
	WatchHoursTarget  int     `json:"watchHoursTarget"`
	Subscribers       *int64  `json:"subscribers"`
	SubscribersTarget int     `json:"subscribersTarget"`
}

// ExplainVideo is one video's totals over the window.
type ExplainVideo struct {
	VideoID               string   `json:"videoId"`
	DurationSeconds       *int32   `json:"durationSeconds"`
	PublishedDate         string   `json:"publishedDate,omitempty"`
	Views                 *int64   `json:"views"`
	WatchHours            *float64 `json:"watchHours"`
	AverageViewPercentage *float64 `json:"averageViewPercentage"`
	Impressions           *int64   `json:"impressions"`
	CTR                   *float64 `json:"ctr"`
}

// ExplainFinding is one active suggestion with its evidence numbers.
type ExplainFinding struct {
	VideoID  string         `json:"videoId,omitempty"`
	Rule     string         `json:"rule"`
	Evidence map[string]any `json:"evidence"`
}

// ExplainInput gathers the aggregates of the ExplainWindowDays ending at
// the newest synced day, the window before it, and the active findings.
func (a *Aggregator) ExplainInput(ctx context.Context, tenantID, channelID uuid.UUID) (ExplainInput, error) {
	st, err := a.SyncStatus(ctx, tenantID, channelID)
	if err != nil {
		return ExplainInput{}, err
	}
	if st.AnalyticsThrough == nil {
		return ExplainInput{}, ErrNothingToExplain
	}
	w := a.DefaultWindow(st, ExplainWindowDays)
	ov, err := a.Overview(ctx, tenantID, channelID, st, w)
	if err != nil {
		return ExplainInput{}, err
	}
	prevWin := Window{From: w.From.AddDate(0, 0, -ExplainWindowDays), To: w.From.AddDate(0, 0, -1)}
	prev, err := a.Overview(ctx, tenantID, channelID, st, prevWin)
	if err != nil {
		return ExplainInput{}, err
	}
	videos, err := a.VideoTotals(ctx, tenantID, channelID, w)
	if err != nil {
		return ExplainInput{}, err
	}
	sugg, err := a.Suggestions(ctx, tenantID, channelID, nil, false)
	if err != nil {
		return ExplainInput{}, err
	}

	in := ExplainInput{
		Window:   ExplainWindow{From: isoDate(w.From), To: isoDate(w.To)},
		Channel:  sumDays(ov.Days),
		Previous: sumDays(prev.Days),
		YPP: ExplainYPP{
			WatchHours: ov.YPP.WatchHours, WatchHoursTarget: ov.YPP.WatchHoursTarget,
			Subscribers: ov.YPP.Subscribers, SubscribersTarget: ov.YPP.SubscribersTarget,
		},
		Videos:   make([]ExplainVideo, 0, min(len(videos), explainMaxVideos)),
		Findings: make([]ExplainFinding, 0, len(sugg)),
	}
	SortVideos(videos, SortViews, true)
	for _, v := range videos[:min(len(videos), explainMaxVideos)] {
		ev := ExplainVideo{
			VideoID: v.VideoID, DurationSeconds: v.DurationSeconds, Views: v.Views, WatchHours: v.WatchHours,
			AverageViewPercentage: v.AverageViewPercentage, Impressions: v.Impressions, CTR: v.CTR,
		}
		if v.PublishedAt != nil {
			ev.PublishedDate = isoDate(*v.PublishedAt)
		}
		in.Videos = append(in.Videos, ev)
	}
	for _, s := range sugg {
		in.Findings = append(in.Findings, ExplainFinding{VideoID: s.VideoID, Rule: s.Rule, Evidence: s.Evidence})
	}
	sort.SliceStable(in.Findings, func(i, j int) bool { return in.Findings[i].Rule < in.Findings[j].Rule })
	return in, nil
}

// sumDays totals a daily series; a metric missing on every day stays nil.
func sumDays(days []ChannelDay) ExplainTotals {
	var t ExplainTotals
	for _, d := range days {
		t.Views = addInt(t.Views, d.Views)
		t.SubscribersGained = addInt(t.SubscribersGained, d.SubscribersGained)
		t.SubscribersLost = addInt(t.SubscribersLost, d.SubscribersLost)
		if d.WatchHours != nil {
			h := *d.WatchHours
			if t.WatchHours != nil {
				h += *t.WatchHours
			}
			t.WatchHours = &h
		}
	}
	return t
}

func addInt(acc, v *int64) *int64 {
	if v == nil {
		return acc
	}
	s := *v
	if acc != nil {
		s += *acc
	}
	return &s
}

func isoDate(t time.Time) string { return t.Format(time.DateOnly) }
