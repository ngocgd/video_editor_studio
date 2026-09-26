package analytics

import "strings"

// Reach report of the YouTube Reporting API: thumbnail impressions and
// their click-through rate per video and day. The type id and the column
// names follow https://developers.google.com/youtube/reporting/v1/reports/channel_reports
// and still need a live reportTypes.list check on a real channel; the
// parser maps columns by header name, so a reordered or extended report
// still parses.
const (
	ReachReportTypeID = "channel_reach_basic_a1"
	// reachReportTypePrefix finds a newer version of the same report
	// (channel_reach_basic_a2, ...) when Google retires the a1 one.
	reachReportTypePrefix = "channel_reach_basic_"
	// ReachJobName is the name the reporting job is created with.
	ReachJobName = "loomtale-reach"
)

// Reach report CSV columns the parser needs.
const (
	ColumnDate        = "date"
	ColumnVideoID     = "video_id"
	ColumnImpressions = "video_thumbnail_impressions"
	ColumnCTR         = "video_thumbnail_impressions_ctr"
)

// ReportTypeCandidate is the part of a report type PickReachReportType
// reads.
type ReportTypeCandidate struct {
	ID         string
	Deprecated bool
}

// PickReachReportType returns the reach report type to schedule: the
// documented id when offered and not deprecated, else the highest
// non-deprecated id of the same family. ok is false when the channel is
// offered none (the report is then "not available from API").
func PickReachReportType(types []ReportTypeCandidate) (id string, ok bool) {
	for _, t := range types {
		if t.ID == ReachReportTypeID && !t.Deprecated {
			return t.ID, true
		}
	}
	for _, t := range types {
		if strings.HasPrefix(t.ID, reachReportTypePrefix) && !t.Deprecated && t.ID > id {
			id = t.ID
		}
	}
	return id, id != ""
}
