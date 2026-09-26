package analytics

import (
	"errors"
	"math"
	"sort"
	"time"

	"loomtale/api/internal/youtube"
)

// maxReasonLen caps a stored "not available" reason.
const maxReasonLen = 300

// unavailableReason turns an API rejection of a metric group into the
// reason stored beside the null values. ok is false for any other error,
// which must fail the sync instead of blanking data.
func unavailableReason(err error) (reason string, ok bool) {
	var ae *youtube.APIError
	if !errors.As(err, &ae) || ae.Kind != youtube.KindPermanent {
		return "", false
	}
	reason = ae.Reason
	if ae.Message != "" {
		reason += ": " + ae.Message
	}
	return truncateText(reason, maxReasonLen), true
}

// truncateText cuts s to at most max characters without splitting a
// UTF-8 sequence, matching Postgres length() on the stored text.
func truncateText(s string, max int) string {
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}

// dayMetrics are one day's reported values and the reasons some metrics
// are missing.
type dayMetrics struct {
	values      map[string]float64
	unavailable map[string]string
}

// dailyTable collects one video's or channel's metric groups by day
// ("YYYY-MM-DD").
type dailyTable map[string]*dayMetrics

func (d dailyTable) day(date string) *dayMetrics {
	m := d[date]
	if m == nil {
		m = &dayMetrics{values: map[string]float64{}, unavailable: map[string]string{}}
		d[date] = m
	}
	return m
}

// add merges a dimensions=day result table. Gaps (null cells) stay
// absent and are stored as null.
func (d dailyTable) add(t youtube.ResultTable, metrics []string) {
	dayCol := t.Column(DimensionDay)
	if dayCol < 0 {
		return
	}
	for _, row := range t.Rows {
		date := youtube.String(row, dayCol)
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			continue
		}
		m := d.day(date)
		for _, name := range metrics {
			if v, ok := youtube.Float(row, t.Column(name)); ok {
				m.values[name] = v
			}
		}
	}
}

// markUnavailable records reason for metrics on every day in the table,
// or on every day of w when no group returned data at all, so the gap is
// explicit rather than a missing row.
func (d dailyTable) markUnavailable(metrics []string, reason string, w Window) {
	if len(d) == 0 {
		for t := w.From; !t.After(w.To); t = t.AddDate(0, 0, 1) {
			d.day(t.Format(time.DateOnly))
		}
	}
	for _, m := range d {
		for _, name := range metrics {
			m.unavailable[name] = reason
		}
	}
}

func (d dailyTable) dates() []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *dayMetrics) float(name string) *float64 {
	if v, ok := m.values[name]; ok {
		return &v
	}
	return nil
}

func (m *dayMetrics) int(name string) *int64 {
	if v, ok := m.values[name]; ok {
		n := int64(math.Round(v))
		return &n
	}
	return nil
}

func (m *dayMetrics) reasons() map[string]string {
	if len(m.unavailable) == 0 {
		return nil
	}
	return m.unavailable
}

// videoDailyRow is one element of UpsertVideoAnalyticsDaily's rows.
type videoDailyRow struct {
	Video         string            `json:"video"`
	Date          string            `json:"date"`
	Views         *int64            `json:"views"`
	Minutes       *float64          `json:"minutes"`
	AvgDuration   *float64          `json:"avg_duration"`
	AvgPercentage *float64          `json:"avg_percentage"`
	SubsGained    *int64            `json:"subs_gained"`
	Unavailable   map[string]string `json:"unavailable,omitempty"`
}

func (d dailyTable) videoRows(video string) []videoDailyRow {
	rows := make([]videoDailyRow, 0, len(d))
	for _, date := range d.dates() {
		m := d[date]
		rows = append(rows, videoDailyRow{
			Video: video, Date: date,
			Views:         m.int(MetricViews),
			Minutes:       m.float(MetricMinutesWatched),
			AvgDuration:   m.float(MetricAverageViewDuration),
			AvgPercentage: m.float(MetricAverageViewPercentage),
			SubsGained:    m.int(MetricSubscribersGained),
			Unavailable:   m.reasons(),
		})
	}
	return rows
}

// channelDailyRow is one element of UpsertChannelMetricsDaily's rows.
type channelDailyRow struct {
	Date        string            `json:"date"`
	Views       *int64            `json:"views"`
	Minutes     *float64          `json:"minutes"`
	SubsGained  *int64            `json:"subs_gained"`
	SubsLost    *int64            `json:"subs_lost"`
	Unavailable map[string]string `json:"unavailable,omitempty"`
}

func (d dailyTable) channelRows() []channelDailyRow {
	rows := make([]channelDailyRow, 0, len(d))
	for _, date := range d.dates() {
		m := d[date]
		rows = append(rows, channelDailyRow{
			Date:        date,
			Views:       m.int(MetricViews),
			Minutes:     m.float(MetricMinutesWatched),
			SubsGained:  m.int(MetricSubscribersGained),
			SubsLost:    m.int(MetricSubscribersLost),
			Unavailable: m.reasons(),
		})
	}
	return rows
}

// retentionRow is one element of InsertVideoRetention's rows.
type retentionRow struct {
	Ratio    float64  `json:"ratio"`
	Watch    *float64 `json:"watch"`
	Relative *float64 `json:"relative"`
}

// retentionRows reads an elapsedVideoTimeRatio result table; rows
// without a ratio are dropped.
func retentionRows(t youtube.ResultTable) []retentionRow {
	ratioCol := t.Column(DimensionElapsedVideoTimeRatio)
	watchCol := t.Column(MetricAudienceWatchRatio)
	relCol := t.Column(MetricRelativeRetentionPerf)
	rows := make([]retentionRow, 0, len(t.Rows))
	for _, row := range t.Rows {
		ratio, ok := youtube.Float(row, ratioCol)
		if !ok {
			continue
		}
		r := retentionRow{Ratio: ratio}
		if v, ok := youtube.Float(row, watchCol); ok {
			r.Watch = &v
		}
		if v, ok := youtube.Float(row, relCol); ok {
			r.Relative = &v
		}
		rows = append(rows, r)
	}
	return rows
}
