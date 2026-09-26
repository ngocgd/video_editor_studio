package analytics

import (
	"math"
	"sort"
	"time"
)

// Rule names. A rule's Version is bumped whenever its thresholds or its
// evidence change meaning: a new version re-opens dismissed suggestions.
const (
	RuleLowCTR        = "low_ctr"
	RuleWeakHook      = "weak_hook"
	RuleSplitLong     = "split_long_video"
	RuleCadenceGap    = "upload_cadence_gap"
	ruleTitleLowCTR   = "Click-through rate below the channel median"
	ruleTitleWeakHook = "Many viewers leave in the first 30 seconds"
	ruleTitleSplit    = "Long video watched less than the channel median"
	ruleTitleCadence  = "Gap in the upload schedule"
)

// Thresholds of the rules (see each rule's doc).
const (
	// RuleWindowDays is the window of video totals the rules read.
	RuleWindowDays = 90
	// MinImpressionsForCTR keeps low-traffic videos out of the CTR rule.
	MinImpressionsForCTR = 1000
	// MinViewsForAverages keeps low-traffic videos out of view-percentage
	// medians, where a handful of views is noise.
	MinViewsForAverages = 100
	// MinVideosForMedian is the smallest population a channel median is
	// computed over.
	MinVideosForMedian = 3
	// HookSeconds and HookMaxDrop define a weak hook: more than HookMaxDrop
	// of the starting audience gone by HookSeconds.
	HookSeconds = 30
	HookMaxDrop = 0.35
	// LongVideoSeconds is where the split rule starts to apply.
	LongVideoSeconds = 8 * 60
	// CadenceGapDays is the longest acceptable gap between uploads.
	CadenceGapDays = 14
)

// RuleInfo is a rule's code-owned metadata, served with its suggestions.
type RuleInfo struct {
	Name    string
	Version int32
	Title   string
}

// Rules lists every rule with its current version.
var Rules = []RuleInfo{
	{Name: RuleLowCTR, Version: 1, Title: ruleTitleLowCTR},
	{Name: RuleWeakHook, Version: 1, Title: ruleTitleWeakHook},
	{Name: RuleSplitLong, Version: 1, Title: ruleTitleSplit},
	{Name: RuleCadenceGap, Version: 1, Title: ruleTitleCadence},
}

// RuleByName returns a rule's metadata.
func RuleByName(name string) (RuleInfo, bool) {
	for _, r := range Rules {
		if r.Name == name {
			return r, true
		}
	}
	return RuleInfo{}, false
}

// RuleVideo is one tracked video's totals over the rule window. A nil
// metric is not available from the API.
type RuleVideo struct {
	VideoID               string
	DurationSeconds       *int32
	PublishedAt           *time.Time
	Views                 *int64
	Impressions           *int64
	CTR                   *float64
	AverageViewPercentage *float64
	// Retention is the audience watch ratio curve, sorted by ratio.
	Retention []RetentionPoint
}

// RetentionPoint is one elapsedVideoTimeRatio bucket.
type RetentionPoint struct {
	Ratio, AudienceWatchRatio float64
}

// RuleInput is everything the rules read for one channel.
type RuleInput struct {
	Now    time.Time
	Window Window
	Videos []RuleVideo
}

// Finding is one fired rule; VideoID is "" for a channel-level finding.
// Evidence holds only aggregated numbers.
type Finding struct {
	VideoID  string
	Rule     string
	Version  int32
	Evidence map[string]any
}

// Key identifies a finding within its channel ("video/rule").
func (f Finding) Key() string { return f.VideoID + "/" + f.Rule }

// Evaluate runs every rule over in and returns the findings sorted by
// video then rule.
func Evaluate(in RuleInput) []Finding {
	var out []Finding
	out = append(out, lowCTR(in)...)
	out = append(out, weakHook(in)...)
	out = append(out, splitLong(in)...)
	out = append(out, cadenceGap(in)...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

func finding(video, rule string, evidence map[string]any) Finding {
	info, _ := RuleByName(rule)
	return Finding{VideoID: video, Rule: rule, Version: info.Version, Evidence: evidence}
}

func windowEvidence(w Window) map[string]any {
	return map[string]any{"windowFrom": w.From.Format(time.DateOnly), "windowTo": w.To.Format(time.DateOnly)}
}

// median of xs; ok is false below MinVideosForMedian values.
func median(xs []float64) (float64, bool) {
	if len(xs) < MinVideosForMedian {
		return 0, false
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2], true
	}
	return (s[n/2-1] + s[n/2]) / 2, true
}

// round keeps evidence readable (4 decimals).
func round(x float64) float64 { return math.Round(x*1e4) / 1e4 }
