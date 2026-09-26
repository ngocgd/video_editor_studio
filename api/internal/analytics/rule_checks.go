package analytics

import (
	"sort"
	"time"
)

// lowCTR fires for a video whose impression click-through rate is below
// the channel median, among videos with at least MinImpressionsForCTR
// impressions in the window: test a new thumbnail or title.
func lowCTR(in RuleInput) []Finding {
	var pop []RuleVideo
	var ctrs []float64
	for _, v := range in.Videos {
		if v.CTR != nil && v.Impressions != nil && *v.Impressions >= MinImpressionsForCTR {
			pop = append(pop, v)
			ctrs = append(ctrs, *v.CTR)
		}
	}
	med, ok := median(ctrs)
	if !ok {
		return nil
	}
	var out []Finding
	for _, v := range pop {
		if *v.CTR >= med {
			continue
		}
		ev := windowEvidence(in.Window)
		ev["ctr"] = round(*v.CTR)
		ev["channelMedianCtr"] = round(med)
		ev["impressions"] = *v.Impressions
		ev["videosCompared"] = len(pop)
		out = append(out, finding(v.VideoID, RuleLowCTR, ev))
	}
	return out
}

// weakHook fires when more than HookMaxDrop of the audience present at
// the start of a video has left by HookSeconds: strengthen the hook.
func weakHook(in RuleInput) []Finding {
	var out []Finding
	for _, v := range in.Videos {
		if v.DurationSeconds == nil || *v.DurationSeconds <= HookSeconds || len(v.Retention) < 2 {
			continue
		}
		if v.Views == nil || *v.Views < MinViewsForAverages {
			continue
		}
		start := v.Retention[0].AudienceWatchRatio
		if start <= 0 {
			continue
		}
		at := retentionAt(v.Retention, float64(HookSeconds)/float64(*v.DurationSeconds))
		drop := (start - at) / start
		if drop <= HookMaxDrop {
			continue
		}
		out = append(out, finding(v.VideoID, RuleWeakHook, map[string]any{
			"startAudienceRatio": round(start),
			"audienceRatioAt30s": round(at),
			"drop":               round(drop),
			"maxDrop":            HookMaxDrop,
			"durationSeconds":    *v.DurationSeconds,
		}))
	}
	return out
}

// retentionAt linearly interpolates the curve at ratio r (clamped to the
// curve's ends).
func retentionAt(curve []RetentionPoint, r float64) float64 {
	if r <= curve[0].Ratio {
		return curve[0].AudienceWatchRatio
	}
	for i := 1; i < len(curve); i++ {
		a, b := curve[i-1], curve[i]
		if r <= b.Ratio {
			if b.Ratio == a.Ratio {
				return b.AudienceWatchRatio
			}
			t := (r - a.Ratio) / (b.Ratio - a.Ratio)
			return a.AudienceWatchRatio + t*(b.AudienceWatchRatio-a.AudienceWatchRatio)
		}
	}
	return curve[len(curve)-1].AudienceWatchRatio
}

// splitLong fires for a video of at least LongVideoSeconds whose average
// view percentage is below the channel median: consider splitting it.
func splitLong(in RuleInput) []Finding {
	var pop []RuleVideo
	var pcts []float64
	for _, v := range in.Videos {
		if v.AverageViewPercentage != nil && v.Views != nil && *v.Views >= MinViewsForAverages {
			pop = append(pop, v)
			pcts = append(pcts, *v.AverageViewPercentage)
		}
	}
	med, ok := median(pcts)
	if !ok {
		return nil
	}
	var out []Finding
	for _, v := range pop {
		if v.DurationSeconds == nil || *v.DurationSeconds < LongVideoSeconds || *v.AverageViewPercentage >= med {
			continue
		}
		ev := windowEvidence(in.Window)
		ev["averageViewPercentage"] = round(*v.AverageViewPercentage)
		ev["channelMedianAverageViewPercentage"] = round(med)
		ev["durationSeconds"] = *v.DurationSeconds
		ev["views"] = *v.Views
		ev["videosCompared"] = len(pop)
		out = append(out, finding(v.VideoID, RuleSplitLong, ev))
	}
	return out
}

// cadenceGap is a channel-level finding: the last tracked upload is more
// than CadenceGapDays old, or two consecutive uploads in the last
// RuleWindowDays were further apart than that.
func cadenceGap(in RuleInput) []Finding {
	var pubs []time.Time
	for _, v := range in.Videos {
		if v.PublishedAt != nil && !v.PublishedAt.After(in.Now) {
			pubs = append(pubs, *v.PublishedAt)
		}
	}
	if len(pubs) == 0 {
		return nil
	}
	sort.Slice(pubs, func(i, j int) bool { return pubs[i].Before(pubs[j]) })
	last := pubs[len(pubs)-1]
	since := wholeDays(in.Now.Sub(last))
	windowStart := in.Now.AddDate(0, 0, -RuleWindowDays)
	longest, inWindow := 0, 0
	for i, p := range pubs {
		if p.Before(windowStart) {
			continue
		}
		inWindow++
		if i > 0 {
			longest = max(longest, wholeDays(p.Sub(pubs[i-1])))
		}
	}
	if since <= CadenceGapDays && longest <= CadenceGapDays {
		return nil
	}
	return []Finding{finding("", RuleCadenceGap, map[string]any{
		"lastPublishedAt":     last.UTC().Format(time.RFC3339),
		"daysSinceLastUpload": since,
		"longestGapDays":      longest,
		"uploadsInWindow":     inWindow,
		"windowDays":          RuleWindowDays,
		"maxGapDays":          CadenceGapDays,
	})}
}

func wholeDays(d time.Duration) int { return int(d / (24 * time.Hour)) }
