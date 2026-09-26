// Package analytics syncs per-video and per-channel metrics from the
// YouTube Analytics API (reports.query) and the YouTube Reporting API
// reach report into Postgres, and derives suggestions from them.
package analytics

// MetricGroup is one set of metrics requested in a single reports.query
// call. Metrics are grouped so that a combination the API rejects only
// blanks that group ("not available from API") instead of the whole day.
//
// The accepted combinations follow the channel and video report tables
// in https://developers.google.com/youtube/analytics/channel_reports and
// the metric definitions in https://developers.google.com/youtube/analytics/metrics.
// They still need a live check on a real channel; a rejected group is
// stored as null with Google's reason, never as zero.
type MetricGroup struct {
	Name    string
	Metrics []string
}

// Analytics API metric names. They are also the keys of the unavailable
// jsonb object on the daily rows, so the UI can name the missing metric.
const (
	MetricViews                    = "views"
	MetricMinutesWatched           = "estimatedMinutesWatched"
	MetricAverageViewDuration      = "averageViewDuration"
	MetricAverageViewPercentage    = "averageViewPercentage"
	MetricSubscribersGained        = "subscribersGained"
	MetricSubscribersLost          = "subscribersLost"
	MetricAudienceWatchRatio       = "audienceWatchRatio"
	MetricRelativeRetentionPerf    = "relativeRetentionPerformance"
	DimensionDay                   = "day"
	DimensionElapsedVideoTimeRatio = "elapsedVideoTimeRatio"
)

// Unavailable keys for the reach report columns.
const (
	KeyImpressions = "impressions"
	KeyCTR         = "ctr"
)

// VideoDailyGroups are queried per video with dimensions=day and
// filters=video==<id> (the "video" filter of the basic user activity
// report accepts one video with the day dimension).
var VideoDailyGroups = []MetricGroup{
	{Name: "watch", Metrics: []string{MetricViews, MetricMinutesWatched, MetricAverageViewDuration, MetricAverageViewPercentage}},
	{Name: "subscribers", Metrics: []string{MetricSubscribersGained}},
}

// ChannelDailyGroups are queried for the whole channel with dimensions=day.
var ChannelDailyGroups = []MetricGroup{
	{Name: "watch", Metrics: []string{MetricViews, MetricMinutesWatched}},
	{Name: "subscribers", Metrics: []string{MetricSubscribersGained, MetricSubscribersLost}},
}

// RetentionMetrics are queried per video with
// dimensions=elapsedVideoTimeRatio (the audience retention report, which
// requires a single-video filter). relativeRetentionPerformance is
// dropped on its own if the API rejects it.
var RetentionMetrics = []string{MetricAudienceWatchRatio, MetricRelativeRetentionPerf}

// groupKeys returns every metric of groups, used as clear_keys so a
// metric that is available again loses its stale "unavailable" reason.
func groupKeys(groups []MetricGroup) []string {
	var keys []string
	for _, g := range groups {
		keys = append(keys, g.Metrics...)
	}
	return keys
}
