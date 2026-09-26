package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAnalytics is an httptest double of the Analytics API, the
// Reporting API and its media downloads.
type fakeAnalytics struct {
	srv *httptest.Server

	mu         sync.Mutex
	lastQuery  map[string]string
	queryRows  int // rows the reports endpoint serves in total
	createdJob map[string]string
	deleted    []string
	reportsQ   string
	report     string // CSV body served by the media endpoint
	chunked    bool   // omit Content-Length on downloads
}

func newFakeAnalytics(t *testing.T) *fakeAnalytics {
	f := &fakeAnalytics{queryRows: 3, report: "date,video_id,impressions\n20260920,abcdefghijk,100\n"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/reports", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		f.lastQuery = map[string]string{}
		for k := range q {
			f.lastQuery[k] = q.Get(k)
		}
		total := f.queryRows
		f.mu.Unlock()
		if strings.Contains(q.Get("metrics"), "bogus") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":400,"message":"Unknown identifier (bogus) given in field parameters.metrics.","errors":[{"reason":"badRequest"}]}}`)
			return
		}
		start, _ := strconv.Atoi(q.Get("startIndex"))
		size, _ := strconv.Atoi(q.Get("maxResults"))
		if start == 0 {
			start = 1
		}
		if size == 0 {
			size = total
		}
		rows := []string{}
		for i := start; i <= total && i < start+size; i++ {
			rows = append(rows, fmt.Sprintf(`["2026-09-%02d",%d,12.5]`, i, i*10))
		}
		_, _ = io.WriteString(w, `{"columnHeaders":[{"name":"day","columnType":"DIMENSION","dataType":"STRING"},{"name":"views","columnType":"METRIC","dataType":"INTEGER"},{"name":"averageViewPercentage","columnType":"METRIC","dataType":"FLOAT"}],"rows":[`+strings.Join(rows, ",")+`]}`)
	})
	mux.HandleFunc("GET /v1/reportTypes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = io.WriteString(w, `{"reportTypes":[{"id":"channel_basic_a2","name":"User activity"}],"nextPageToken":"p2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"reportTypes":[{"id":"channel_reach_basic_a1","name":"Reach"}]}`)
	})
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"jobs":[{"id":"job-1","reportTypeId":"channel_reach_basic_a1","name":"loomtale","createTime":"2026-09-20T01:02:03Z"}]}`)
	})
	mux.HandleFunc("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]string{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.createdJob = body
		f.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"id":"job-2","reportTypeId":%q,"name":%q,"createTime":"2026-09-26T00:00:00Z"}`, body["reportTypeId"], body["name"])
	})
	mux.HandleFunc("DELETE /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.deleted = append(f.deleted, r.PathValue("id"))
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("GET /v1/jobs/{id}/reports", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reportsQ = r.URL.Query().Get("createdAfter")
		f.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"reports":[{"id":"rep-1","jobId":%q,"startTime":"2026-09-20T07:00:00Z","endTime":"2026-09-21T07:00:00Z","createTime":"2026-09-22T10:00:00Z","downloadUrl":"%s/v1/media/rep-1?alt=media"}]}`, r.PathValue("id"), f.srv.URL)
	})
	mux.HandleFunc("GET /v1/media/{id}", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		body, chunked := f.report, f.chunked
		f.mu.Unlock()
		if chunked {
			w.(http.Flusher).Flush() // forces chunked encoding, no Content-Length
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("GET /youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("part") != "statistics" {
			http.Error(w, "bad part", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"items":[{"statistics":{"viewCount":"123456","subscriberCount":"987","hiddenSubscriberCount":false,"videoCount":"42"}}]}`)
	})
	mux.HandleFunc("GET /youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("part") != "snippet,contentDetails" {
			http.Error(w, "bad part", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"items":[{"id":"abcdefghijk","snippet":{"channelId":"UC123","title":"Moon fox","publishedAt":"2026-09-01T12:00:00Z"},"contentDetails":{"duration":"PT12M5S"}}]}`)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAnalytics) client() *AnalyticsClient {
	return &AnalyticsClient{HTTP: f.srv.Client(), AnalyticsBase: f.srv.URL + "/v2", ReportingBase: f.srv.URL + "/v1"}
}

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestQuerySendsParametersAndReadsCells(t *testing.T) {
	f := newFakeAnalytics(t)
	tbl, err := f.client().Query(context.Background(), ReportQuery{
		Start: day("2026-09-01"), End: day("2026-09-03"),
		Metrics: []string{"views", "averageViewPercentage"}, Dimensions: []string{"day"},
		Filters: "video==abcdefghijk", Sort: "day",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ids": "channel==MINE", "startDate": "2026-09-01", "endDate": "2026-09-03", "metrics": "views,averageViewPercentage", "dimensions": "day", "filters": "video==abcdefghijk", "sort": "day"}
	for k, v := range want {
		if f.lastQuery[k] != v {
			t.Errorf("param %s = %q, want %q", k, f.lastQuery[k], v)
		}
	}
	if len(tbl.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(tbl.Rows))
	}
	vc, pc := tbl.Column("views"), tbl.Column("averageViewPercentage")
	if v, ok := Float(tbl.Rows[1], vc); !ok || v != 20 {
		t.Errorf("views = %v %v, want 20", v, ok)
	}
	if v, ok := Float(tbl.Rows[1], pc); !ok || v != 12.5 {
		t.Errorf("avg%% = %v %v, want 12.5", v, ok)
	}
	if s := String(tbl.Rows[0], tbl.Column("day")); s != "2026-09-01" {
		t.Errorf("day = %q", s)
	}
	if _, ok := Float(tbl.Rows[0], tbl.Column("missing")); ok {
		t.Error("a missing column must read as not available, not zero")
	}
}

func TestQueryRejectedCombinationIsPermanent(t *testing.T) {
	f := newFakeAnalytics(t)
	_, err := f.client().Query(context.Background(), ReportQuery{Start: day("2026-09-01"), End: day("2026-09-01"), Metrics: []string{"bogus"}})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Kind != KindPermanent || ae.Reason != "badRequest" || !strings.Contains(ae.Message, "bogus") {
		t.Fatalf("got %v, want permanent badRequest with Google's message", err)
	}
	if _, err := f.client().Query(context.Background(), ReportQuery{}); err == nil {
		t.Fatal("a query without metrics must fail locally")
	}
}

func TestQueryAllPages(t *testing.T) {
	f := newFakeAnalytics(t)
	f.queryRows = 7
	tbl, err := f.client().QueryAll(context.Background(), ReportQuery{Start: day("2026-09-01"), End: day("2026-09-07"), Metrics: []string{"views"}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tbl.Rows) != 7 || len(tbl.ColumnHeaders) != 3 {
		t.Fatalf("rows=%d headers=%d, want 7 and 3", len(tbl.Rows), len(tbl.ColumnHeaders))
	}
	if v, _ := Float(tbl.Rows[6], 1); v != 70 {
		t.Errorf("last row views = %v, want 70", v)
	}
}

func TestReportingJobsAndReports(t *testing.T) {
	f := newFakeAnalytics(t)
	c := f.client()
	ctx := context.Background()
	types, err := c.ReportTypes(ctx)
	if err != nil || len(types) != 2 || types[1].ID != "channel_reach_basic_a1" {
		t.Fatalf("report types across pages = %+v, %v", types, err)
	}
	jobs, err := c.Jobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].ID != "job-1" || jobs[0].CreateTime.IsZero() {
		t.Fatalf("jobs = %+v, %v", jobs, err)
	}
	j, err := c.CreateJob(ctx, "channel_reach_basic_a1", "loomtale reach")
	if err != nil || j.ID != "job-2" || f.createdJob["reportTypeId"] != "channel_reach_basic_a1" || f.createdJob["name"] != "loomtale reach" {
		t.Fatalf("create job = %+v, body %v, %v", j, f.createdJob, err)
	}
	after := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	reps, err := c.Reports(ctx, "job-1", after)
	if err != nil || len(reps) != 1 || reps[0].ID != "rep-1" || reps[0].JobID != "job-1" {
		t.Fatalf("reports = %+v, %v", reps, err)
	}
	if f.reportsQ != "2026-09-21T00:00:00Z" {
		t.Errorf("createdAfter = %q", f.reportsQ)
	}
	if err := c.DeleteJob(ctx, "job-1"); err != nil || len(f.deleted) != 1 || f.deleted[0] != "job-1" {
		t.Fatalf("delete job: %v %v", f.deleted, err)
	}

	rc, err := c.Download(ctx, reps[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || string(b) != f.report {
		t.Fatalf("download = %q, %v", b, err)
	}
}

func TestDownloadCapAndHostGuard(t *testing.T) {
	f := newFakeAnalytics(t)
	c := f.client()
	ctx := context.Background()
	rep := BulkReport{ID: "rep-1", DownloadURL: f.srv.URL + "/v1/media/rep-1?alt=media"}

	c.MaxReportBytes = int64(len(f.report))
	rc, err := c.Download(ctx, rep)
	if err != nil {
		t.Fatalf("a body exactly at the cap must download: %v", err)
	}
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatalf("exactly-full body: %v", err)
	}
	_ = rc.Close()

	c.MaxReportBytes = 10
	if _, err := c.Download(ctx, rep); !errors.Is(err, ErrReportTooLarge) {
		t.Fatalf("declared length over cap: got %v", err)
	}
	f.chunked = true
	rc, err = c.Download(ctx, rep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); !errors.Is(err, ErrReportTooLarge) {
		t.Fatalf("streamed body over cap: got %v", err)
	}
	_ = rc.Close()

	for _, u := range []string{"https://evil.example/v1/media/x", "http://youtubereporting.googleapis.com/v1/media/x", "https://googleapis.com.evil.example/x", "::bad"} {
		if _, err := c.Download(ctx, BulkReport{DownloadURL: u}); !errors.Is(err, ErrUntrustedDownloadURL) {
			t.Errorf("%s: got %v, want untrusted", u, err)
		}
	}
	google := &AnalyticsClient{}
	for _, u := range []string{"https://youtubereporting.googleapis.com/v1/media/x", "https://googleapis.com/x"} {
		pu, _ := url.Parse(u)
		if !google.trustedDownload(pu) {
			t.Errorf("%s must be trusted", u)
		}
	}
}

func TestDownloadTransportErrorHidesCapabilityURL(t *testing.T) {
	c := &AnalyticsClient{HTTP: &http.Client{Transport: failingTransport{errors.New("connection reset")}}, ReportingBase: "https://youtubereporting.googleapis.com/v1"}
	secret := "https://youtubereporting.googleapis.com/v1/media/CH_secret_token?alt=media"
	_, err := c.Download(context.Background(), BulkReport{DownloadURL: secret})
	if !IsKind(err, KindTransient) || strings.Contains(err.Error(), "secret_token") {
		t.Fatalf("got %v, want a transient error without the URL", err)
	}
}

func TestChannelStatisticsAndVideos(t *testing.T) {
	f := newFakeAnalytics(t)
	l := testLedger()
	c := &Client{HTTP: f.srv.Client(), Ledger: l, APIBase: f.srv.URL + "/youtube/v3"}
	st, err := c.MyChannelStatistics(context.Background())
	if err != nil || st != (ChannelStatistics{ViewCount: 123456, SubscriberCount: 987, VideoCount: 42}) {
		t.Fatalf("statistics = %+v, %v", st, err)
	}
	vids, err := c.Videos(context.Background(), []string{"abcdefghijk"})
	if err != nil || len(vids) != 1 {
		t.Fatalf("videos = %+v, %v", vids, err)
	}
	v := vids[0]
	if v.ChannelID != "UC123" || v.Title != "Moon fox" || v.DurationSeconds != 725 || !v.PublishedAt.Equal(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("video = %+v", v)
	}
	if u, _ := l.Usage(context.Background()); u.Used != 2 {
		t.Errorf("two reads charged %d units, want 2", u.Used)
	}
	if _, err := c.Videos(context.Background(), make([]string, 51)); err == nil {
		t.Error("more than 50 ids must fail locally")
	}
}

func TestParseISODuration(t *testing.T) {
	for in, want := range map[string]int{"PT1H2M3S": 3723, "PT45S": 45, "PT10M": 600, "P1DT1S": 86401, "P0D": 0, "": 0, "garbage": 0} {
		if got := ParseISODuration(in); got != want {
			t.Errorf("ParseISODuration(%q) = %d, want %d", in, got, want)
		}
	}
}
