package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"loomtale/api/internal/youtube"
)

// fakeAnalytics answers reports.query like the Analytics API: metrics in
// reject come back as a 400 badRequest, everything else as a table.
func fakeAnalytics(t *testing.T, reject map[string]bool, table func(q map[string]string) map[string]any) *youtube.AnalyticsClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := map[string]string{}
		for k, v := range r.URL.Query() {
			q[k] = v[0]
		}
		for _, m := range strings.Split(q["metrics"], ",") {
			if reject[m] {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":400,"message":"The query is not supported.","errors":[{"reason":"badRequest"}]}}`))
				return
			}
		}
		_ = json.NewEncoder(w).Encode(table(q))
	}))
	t.Cleanup(srv.Close)
	return &youtube.AnalyticsClient{HTTP: srv.Client(), AnalyticsBase: srv.URL}
}

// dayTable echoes the requested metrics for two days, 10 per metric
// index, with the second day's last metric left null (a gap).
func dayTable(q map[string]string) map[string]any {
	metrics := strings.Split(q["metrics"], ",")
	headers := []map[string]string{{"name": "day", "columnType": "DIMENSION"}}
	row1, row2 := []any{"2026-09-20"}, []any{"2026-09-21"}
	for i, m := range metrics {
		headers = append(headers, map[string]string{"name": m, "columnType": "METRIC"})
		row1 = append(row1, 10*(i+1))
		if i == len(metrics)-1 {
			row2 = append(row2, nil)
		} else {
			row2 = append(row2, 10*(i+1))
		}
	}
	return map[string]any{"columnHeaders": headers, "rows": [][]any{row1, row2}}
}

func TestFetchDailyStoresRejectedGroupAsUnavailable(t *testing.T) {
	ac := fakeAnalytics(t, map[string]bool{MetricSubscribersGained: true}, dayTable)
	w := Window{date("2026-09-20"), date("2026-09-21")}
	d, err := fetchDaily(context.Background(), ac, VideoDailyGroups, "video=="+vidA, w)
	if err != nil {
		t.Fatal(err)
	}
	rows := d.videoRows(vidA)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	if *r.Views != 10 || *r.Minutes != 20 || *r.AvgDuration != 30 || *r.AvgPercentage != 40 {
		t.Fatalf("values = %+v", r)
	}
	if r.SubsGained != nil || !strings.HasPrefix(r.Unavailable[MetricSubscribersGained], "badRequest") {
		t.Fatalf("rejected metric must be null with a reason, got %v %v", r.SubsGained, r.Unavailable)
	}
	if rows[1].AvgPercentage != nil {
		t.Fatal("a null cell must stay null, not zero")
	}
}

func TestFetchDailyAllRejectedStillWritesGapRows(t *testing.T) {
	ac := fakeAnalytics(t, map[string]bool{MetricViews: true, MetricSubscribersGained: true, MetricSubscribersLost: true}, dayTable)
	w := Window{date("2026-09-20"), date("2026-09-22")}
	d, err := fetchDaily(context.Background(), ac, ChannelDailyGroups, "", w)
	if err != nil {
		t.Fatal(err)
	}
	rows := d.channelRows()
	if len(rows) != 3 || rows[0].Views != nil || len(rows[2].Unavailable) != 4 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestFetchDailyPropagatesOtherErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"quota","errors":[{"reason":"quotaExceeded"}]}}`))
	}))
	defer srv.Close()
	ac := &youtube.AnalyticsClient{HTTP: srv.Client(), AnalyticsBase: srv.URL}
	_, err := fetchDaily(context.Background(), ac, VideoDailyGroups, "", Window{date("2026-09-20"), date("2026-09-20")})
	if !youtube.IsKind(err, youtube.KindQuota) {
		t.Fatalf("err = %v, want quota", err)
	}
}

func retentionTable(q map[string]string) map[string]any {
	metrics := strings.Split(q["metrics"], ",")
	headers := []map[string]string{{"name": DimensionElapsedVideoTimeRatio}}
	for _, m := range metrics {
		headers = append(headers, map[string]string{"name": m})
	}
	var rows [][]any
	for i := 1; i <= 100; i++ {
		row := []any{float64(i) / 100, 1 - float64(i)/200}
		if len(metrics) == 2 {
			row = append(row, 0.5)
		}
		rows = append(rows, row)
	}
	return map[string]any{"columnHeaders": headers, "rows": rows}
}

func TestFetchRetentionFallsBackWithoutRelativePerformance(t *testing.T) {
	ac := fakeAnalytics(t, map[string]bool{MetricRelativeRetentionPerf: true}, retentionTable)
	rows, ok, err := fetchRetention(context.Background(), ac, vidA, Window{date("2026-01-01"), date("2026-09-20")})
	if err != nil || !ok || len(rows) != 100 {
		t.Fatalf("rows %d ok %v err %v", len(rows), ok, err)
	}
	if rows[0].Ratio != 0.01 || rows[0].Watch == nil || rows[0].Relative != nil {
		t.Fatalf("first bucket = %+v", rows[0])
	}

	ac = fakeAnalytics(t, map[string]bool{MetricAudienceWatchRatio: true}, retentionTable)
	if _, ok, err := fetchRetention(context.Background(), ac, vidA, Window{date("2026-01-01"), date("2026-09-20")}); ok || err != nil {
		t.Fatalf("unavailable curve: ok %v err %v", ok, err)
	}
}
