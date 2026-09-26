package analytics

import (
	"testing"
	"time"
)

var ruleNow = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func ruleWindow() Window {
	to := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	return Window{From: to.AddDate(0, 0, -(RuleWindowDays - 1)), To: to}
}

// recentPub keeps the cadence rule quiet in tests of other rules.
func recentPub() *time.Time { return ptr(ruleNow.AddDate(0, 0, -2)) }

func ctrVideo(id string, impressions int64, ctr float64) RuleVideo {
	return RuleVideo{VideoID: id, Impressions: &impressions, CTR: &ctr, PublishedAt: recentPub()}
}

func keys(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Key())
	}
	return out
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEvaluateRules(t *testing.T) {
	longVideo := func(id string, avp float64, dur int32) RuleVideo {
		return RuleVideo{VideoID: id, Views: ptr(int64(500)), AverageViewPercentage: &avp, DurationSeconds: &dur, PublishedAt: recentPub()}
	}
	hookVideo := func(id string, at30 float64) RuleVideo {
		// 300s video: 30s is ratio 0.1.
		return RuleVideo{
			VideoID: id, DurationSeconds: ptr(int32(300)), Views: ptr(int64(1000)), PublishedAt: recentPub(),
			Retention: []RetentionPoint{{0.01, 1.0}, {0.1, at30}, {1, 0.1}},
		}
	}
	cases := []struct {
		name   string
		videos []RuleVideo
		want   []string
	}{
		{"no videos", nil, nil},
		{
			"ctr below median fires only above the impression floor",
			[]RuleVideo{
				ctrVideo("a", 5000, 0.02), ctrVideo("b", 5000, 0.05), ctrVideo("c", 5000, 0.08),
				ctrVideo("d", 999, 0.001), // below the floor: neither compared nor flagged
			},
			[]string{"a/" + RuleLowCTR},
		},
		{
			"ctr needs three comparable videos",
			[]RuleVideo{ctrVideo("a", 5000, 0.02), ctrVideo("b", 5000, 0.05), ctrVideo("c", 10, 0.08)},
			nil,
		},
		{
			"ctr unavailable is not a zero",
			[]RuleVideo{ctrVideo("a", 5000, 0.04), ctrVideo("b", 5000, 0.05), ctrVideo("c", 5000, 0.06), {VideoID: "d", Impressions: ptr(int64(5000)), PublishedAt: recentPub()}},
			[]string{"a/" + RuleLowCTR},
		},
		{
			"weak hook above 35 percent drop",
			[]RuleVideo{hookVideo("h", 0.60), hookVideo("ok", 0.70)},
			[]string{"h/" + RuleWeakHook},
		},
		{
			"hook ignores videos of 30 seconds or less",
			[]RuleVideo{{VideoID: "s", DurationSeconds: ptr(int32(30)), Views: ptr(int64(1000)), PublishedAt: recentPub(), Retention: []RetentionPoint{{0.01, 1}, {1, 0.1}}}},
			nil,
		},
		{
			"split only long videos below the median",
			[]RuleVideo{longVideo("long", 20, 900), longVideo("short", 10, 120), longVideo("mid", 40, 900), longVideo("top", 60, 900)},
			[]string{"long/" + RuleSplitLong},
		},
		{
			"cadence gap since the last upload",
			[]RuleVideo{{VideoID: "old", PublishedAt: ptr(ruleNow.AddDate(0, 0, -20))}},
			[]string{"/" + RuleCadenceGap},
		},
		{
			"cadence gap between uploads in the window",
			[]RuleVideo{
				{VideoID: "a", PublishedAt: ptr(ruleNow.AddDate(0, 0, -40))},
				{VideoID: "b", PublishedAt: ptr(ruleNow.AddDate(0, 0, -20))},
				{VideoID: "c", PublishedAt: ptr(ruleNow.AddDate(0, 0, -3))},
			},
			[]string{"/" + RuleCadenceGap},
		},
		{
			"regular cadence is quiet",
			[]RuleVideo{
				{VideoID: "a", PublishedAt: ptr(ruleNow.AddDate(0, 0, -21))},
				{VideoID: "b", PublishedAt: ptr(ruleNow.AddDate(0, 0, -10))},
				{VideoID: "c", PublishedAt: ptr(ruleNow.AddDate(0, 0, -1))},
			},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keys(Evaluate(RuleInput{Now: ruleNow, Window: ruleWindow(), Videos: tc.videos}))
			if !equalKeys(got, tc.want) {
				t.Fatalf("findings = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindingsCarryVersionAndEvidence(t *testing.T) {
	fs := Evaluate(RuleInput{Now: ruleNow, Window: ruleWindow(), Videos: []RuleVideo{
		ctrVideo("a", 5000, 0.02), ctrVideo("b", 5000, 0.05), ctrVideo("c", 5000, 0.08),
	}})
	if len(fs) != 1 {
		t.Fatalf("findings = %v", keys(fs))
	}
	f := fs[0]
	if f.Version < 1 {
		t.Fatalf("version = %d", f.Version)
	}
	if f.Evidence["channelMedianCtr"] != 0.05 || f.Evidence["ctr"] != 0.02 || f.Evidence["impressions"] != int64(5000) {
		t.Fatalf("evidence = %v", f.Evidence)
	}
	if f.Evidence["windowTo"] != "2026-09-18" {
		t.Fatalf("window = %v", f.Evidence["windowTo"])
	}
}

func TestRetentionAtInterpolates(t *testing.T) {
	curve := []RetentionPoint{{0.1, 1}, {0.2, 0.5}, {1, 0.1}}
	for _, tc := range []struct{ r, want float64 }{{0, 1}, {0.15, 0.75}, {0.2, 0.5}, {2, 0.1}} {
		if got := retentionAt(curve, tc.r); got < tc.want-1e-9 || got > tc.want+1e-9 {
			t.Errorf("retentionAt(%v) = %v, want %v", tc.r, got, tc.want)
		}
	}
}

func TestMedian(t *testing.T) {
	if _, ok := median([]float64{1, 2}); ok {
		t.Fatal("median of two values should be refused")
	}
	if m, _ := median([]float64{4, 1, 3, 2}); m != 2.5 {
		t.Fatalf("even median = %v", m)
	}
	if m, _ := median([]float64{5, 1, 3}); m != 3 {
		t.Fatalf("odd median = %v", m)
	}
}

func TestEveryRuleIsRegistered(t *testing.T) {
	for _, name := range []string{RuleLowCTR, RuleWeakHook, RuleSplitLong, RuleCadenceGap} {
		if info, ok := RuleByName(name); !ok || info.Version < 1 || info.Title == "" {
			t.Errorf("rule %s: %+v %v", name, info, ok)
		}
	}
}
