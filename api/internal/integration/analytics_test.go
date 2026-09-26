//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"loomtale/api/internal/analytics"
	"loomtale/api/internal/analyticsapi"
	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	httpgen "loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/youtube"
)

// Video ids of the fake channel. Titles never reach the explain input.
const (
	anaVideoOwn     = "vidAnalyt01"
	anaVideoOther   = "vidOtherCh1"
	anaVideoMissing = "vidMissing1"
	anaVideoTitle   = "Secret Draft Title"
	anaChannelYTID  = "UCanalyticsfake"
)

// anaNow pins the clock: the last complete Pacific day is 2026-09-25.
var anaNow = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// fakeGoogleAnalytics doubles the Data API (channels, videos), the
// Analytics API reports.query and the Reporting API (report types, jobs,
// reports, download) for one channel.
type fakeGoogleAnalytics struct {
	srv *httptest.Server

	mu     sync.Mutex
	calls  map[string]int
	reach  string // CSV served for report-1
	reject map[string]bool
	// failSide, when set, fails channels.list and reportTypes.list with
	// 400 and this message: both become notes of an otherwise good sync.
	failSide string
}

// sideFailure writes the failSide error and reports whether it did.
func (f *fakeGoogleAnalytics) sideFailure(w http.ResponseWriter) bool {
	f.mu.Lock()
	msg := f.failSide
	f.mu.Unlock()
	if msg == "" {
		return false
	}
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code": 400, "message": msg, "errors": []map[string]string{{"reason": "badRequest"}},
	}})
	return true
}

func newFakeGoogleAnalytics(t *testing.T) *fakeGoogleAnalytics {
	f := &fakeGoogleAnalytics{calls: map[string]int{}, reject: map[string]bool{analytics.MetricSubscribersLost: true}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		f.count("channels")
		if f.sideFailure(w) {
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"` + anaChannelYTID + `","statistics":{"viewCount":"9000","subscriberCount":"1500","hiddenSubscriberCount":false,"videoCount":"3"}}]}`))
	})
	mux.HandleFunc("GET /youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		f.count("videos")
		var items []string
		for _, id := range strings.Split(r.URL.Query().Get("id"), ",") {
			switch id {
			case anaVideoOwn:
				items = append(items, `{"id":"`+id+`","snippet":{"channelId":"`+anaChannelYTID+`","title":"`+anaVideoTitle+`","publishedAt":"2026-09-01T10:00:00Z"},"contentDetails":{"duration":"PT10M30S"}}`)
			case anaVideoOther:
				items = append(items, `{"id":"`+id+`","snippet":{"channelId":"UCsomeoneelse","title":"x","publishedAt":"2026-09-01T10:00:00Z"},"contentDetails":{"duration":"PT1M"}}`)
			}
		}
		_, _ = w.Write([]byte(`{"items":[` + strings.Join(items, ",") + `]}`))
	})
	mux.HandleFunc("GET /v2/reports", f.reports)
	mux.HandleFunc("GET /v1/reportTypes", func(w http.ResponseWriter, _ *http.Request) {
		if f.sideFailure(w) {
			return
		}
		_, _ = w.Write([]byte(`{"reportTypes":[{"id":"channel_basic_a2","name":"basic"},{"id":"` + analytics.ReachReportTypeID + `","name":"reach"}]}`))
	})
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jobs":[]}`))
	})
	mux.HandleFunc("POST /v1/jobs", func(w http.ResponseWriter, _ *http.Request) {
		f.count("jobs.create")
		_, _ = w.Write([]byte(`{"id":"job-1","reportTypeId":"` + analytics.ReachReportTypeID + `","name":"` + analytics.ReachJobName + `","createTime":"2026-09-20T00:00:00Z"}`))
	})
	mux.HandleFunc("GET /v1/jobs/job-1/reports", func(w http.ResponseWriter, _ *http.Request) {
		// createdAfter is ignored on purpose: a report listed again must
		// not be counted twice.
		_, _ = fmt.Fprintf(w, `{"reports":[{"id":"report-1","jobId":"job-1","startTime":"2026-09-24T07:00:00Z","endTime":"2026-09-25T07:00:00Z","createTime":"2026-09-25T20:00:00Z","downloadUrl":%q}]}`,
			f.srv.URL+"/v1/media/report-1")
	})
	mux.HandleFunc("GET /v1/media/report-1", func(w http.ResponseWriter, _ *http.Request) {
		f.count("download")
		f.mu.Lock()
		body := f.reach
		f.mu.Unlock()
		_, _ = w.Write([]byte(body))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.reach = "date,channel_id,video_id,traffic_source_type,video_thumbnail_impressions,video_thumbnail_impressions_ctr\n" +
		"20260924," + anaChannelYTID + "," + anaVideoOwn + ",1,1000,0.05\n" +
		"20260924," + anaChannelYTID + "," + anaVideoOwn + ",2,3000,0.01\n"
	return f
}

func (f *fakeGoogleAnalytics) count(key string) {
	f.mu.Lock()
	f.calls[key]++
	f.mu.Unlock()
}

func (f *fakeGoogleAnalytics) callCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

// reports answers reports.query: a retention curve for
// elapsedVideoTimeRatio, else one row per day of the window with fixed
// values per metric. Rejected metrics fail the whole query with 400.
// Like the real API it honours startIndex (1-based) and maxResults, so
// windows longer than one page are fetched page by page.
func (f *fakeGoogleAnalytics) reports(w http.ResponseWriter, r *http.Request) {
	f.count("reports.query")
	q := r.URL.Query()
	metrics := strings.Split(q.Get("metrics"), ",")
	f.mu.Lock()
	for _, m := range metrics {
		if f.reject[m] {
			f.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"The query is not supported.","errors":[{"reason":"badRequest"}]}}`))
			return
		}
	}
	f.mu.Unlock()
	dim := q.Get("dimensions")
	headers := []map[string]string{{"name": dim, "columnType": "DIMENSION"}}
	for _, m := range metrics {
		headers = append(headers, map[string]string{"name": m, "columnType": "METRIC"})
	}
	var rows [][]any
	if dim == analytics.DimensionElapsedVideoTimeRatio {
		for i := 1; i <= 100; i++ {
			row := []any{float64(i) / 100}
			for range metrics {
				row = append(row, 1-float64(i)/200)
			}
			rows = append(rows, row)
		}
	} else {
		start, _ := time.Parse(time.DateOnly, q.Get("startDate"))
		end, _ := time.Parse(time.DateOnly, q.Get("endDate"))
		values := map[string]any{
			analytics.MetricViews: 100, analytics.MetricMinutesWatched: 60, analytics.MetricAverageViewDuration: 90,
			analytics.MetricAverageViewPercentage: 45.5, analytics.MetricSubscribersGained: 3,
		}
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			row := []any{d.Format(time.DateOnly)}
			for _, m := range metrics {
				row = append(row, values[m])
			}
			rows = append(rows, row)
		}
	}
	rows = pageOf(rows, q.Get("startIndex"), q.Get("maxResults"))
	_ = json.NewEncoder(w).Encode(map[string]any{"columnHeaders": headers, "rows": rows})
}

// pageOf slices rows to the page a reports.query asks for: startIndex is
// 1-based and defaults to 1; a missing maxResults returns every row from
// startIndex on.
func pageOf(rows [][]any, startIndex, maxResults string) [][]any {
	from := 0
	if n, err := strconv.Atoi(startIndex); err == nil && n > 1 {
		from = n - 1
	}
	if from >= len(rows) {
		return [][]any{}
	}
	rows = rows[from:]
	if n, err := strconv.Atoi(maxResults); err == nil && n > 0 && n < len(rows) {
		rows = rows[:n]
	}
	return rows
}

// fakeAnalyticsClients hands out clients pointed at the fake; err
// replaces them when set (a dead grant).
type fakeAnalyticsClients struct {
	google *fakeGoogleAnalytics
	ledger *youtube.Ledger
	err    error
	// before runs first on every ForChannel call (e.g. a disconnect that
	// races an in-flight sync).
	before func()
}

func (c *fakeAnalyticsClients) ForChannel(context.Context, uuid.UUID, uuid.UUID) (analytics.Clients, error) {
	if c.before != nil {
		c.before()
	}
	if c.err != nil {
		return analytics.Clients{}, c.err
	}
	httpc := c.google.srv.Client()
	return analytics.Clients{
		Data: &youtube.Client{HTTP: httpc, Ledger: c.ledger, APIBase: c.google.srv.URL + "/youtube/v3"},
		Analytics: &youtube.AnalyticsClient{
			HTTP: httpc, AnalyticsBase: c.google.srv.URL + "/v2", ReportingBase: c.google.srv.URL + "/v1",
		},
	}, nil
}

// recordingJobs stands in for the api's insert-only River client.
type recordingJobs struct {
	mu   sync.Mutex
	args []river.JobArgs
}

func (j *recordingJobs) Insert(_ context.Context, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	j.mu.Lock()
	j.args = append(j.args, args)
	j.mu.Unlock()
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{Kind: args.Kind()}}, nil
}

// recordingEngine stands in for the pipeline engine.
type recordingEngine struct {
	specs []pipeline.RunSpec
}

func (e *recordingEngine) Enqueue(_ context.Context, _ uuid.UUID, spec pipeline.RunSpec) (uuid.UUID, error) {
	e.specs = append(e.specs, spec)
	return spec.ID, nil
}

type analyticsFixture struct {
	api     *analyticsapi.AnalyticsAPI
	syncer  *analytics.Syncer
	google  *fakeGoogleAnalytics
	clients *fakeAnalyticsClients
	jobs    *recordingJobs
	engine  *recordingEngine
	owner   *pgxpool.Pool
	channel uuid.UUID
	tenant  uuid.UUID
	ctx     context.Context // owner of the channel's tenant
	ctxB    context.Context // owner of another tenant
}

func newAnalyticsFixture(t *testing.T) *analyticsFixture {
	t.Helper()
	owner := ownerPool(t)
	app := appPool(t)
	oq := gen.New(owner)
	aq := gen.New(app)
	user := createFixtureUser(t, oq, "ana-"+uuid.NewString()[:8], uniqueEmail("ana"), "owner")
	other := createFixtureUser(t, oq, "ana-b-"+uuid.NewString()[:8], uniqueEmail("anab"), "owner")

	ch, err := aq.UpsertYouTubeChannel(context.Background(), gen.UpsertYouTubeChannelParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(user.TenantID),
		YoutubeChannelID: anaChannelYTID, Title: "Night Tales", Scopes: []string{"yt-analytics.readonly"},
		LongUploadsStatus: "eligible", ConnectedBy: idconv.ToPg(user.UserID),
	})
	if err != nil {
		t.Fatalf("store channel: %v", err)
	}
	project := "it-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DELETE FROM quota_ledger WHERE project = $1", project)
	})
	google := newFakeGoogleAnalytics(t)
	clients := &fakeAnalyticsClients{google: google, ledger: &youtube.Ledger{Store: aq, Config: youtube.QuotaConfig{
		Project: project, DailyLimit: 10000, InsertCost: 1600, WriteCost: 50, ReadCost: 1,
	}}}
	now := func() time.Time { return anaNow }
	agg := &analytics.Aggregator{Pool: app, Queries: aq, Now: now}
	fx := &analyticsFixture{
		google: google, clients: clients, jobs: &recordingJobs{}, engine: &recordingEngine{},
		owner: owner, channel: idconv.FromPg(ch.ID), tenant: user.TenantID,
		ctx: sessionCtx(t, oq, user), ctxB: sessionCtx(t, oq, other),
	}
	fx.api = &analyticsapi.AnalyticsAPI{Queries: aq, Aggregator: agg, Clients: clients, Jobs: fx.jobs, Engine: fx.engine}
	fx.syncer = &analytics.Syncer{Pool: app, Queries: aq, Clients: clients, Now: now}
	return fx
}

func (fx *analyticsFixture) track(t *testing.T, ctx context.Context, video string) httpgen.TrackAnalyticsVideoResponseObject {
	t.Helper()
	resp, err := fx.api.TrackAnalyticsVideo(ctx, httpgen.TrackAnalyticsVideoRequestObject{
		Body: &httpgen.TrackAnalyticsVideoJSONRequestBody{ChannelId: fx.channel, Video: video},
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (fx *analyticsFixture) videoRow(t *testing.T) httpgen.AnalyticsVideoRow {
	t.Helper()
	resp, err := fx.api.ListAnalyticsVideos(fx.ctx, httpgen.ListAnalyticsVideosRequestObject{
		Params: httpgen.ListAnalyticsVideosParams{ChannelId: fx.channel},
	})
	if err != nil {
		t.Fatal(err)
	}
	page, ok := resp.(httpgen.ListAnalyticsVideos200JSONResponse)
	if !ok || len(page.Items) != 1 {
		t.Fatalf("video list: %#v", resp)
	}
	return page.Items[0]
}

func (fx *analyticsFixture) scalar(t *testing.T, sql string) int {
	t.Helper()
	var n int
	if err := fx.owner.QueryRow(context.Background(), sql, fx.channel).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// TestAnalyticsTrackSyncAndReadBack tracks a video through the API, syncs
// the channel against the fake Google APIs and reads every view back.
func TestAnalyticsTrackSyncAndReadBack(t *testing.T) {
	fx := newAnalyticsFixture(t)

	for video, want := range map[string]string{
		"not a video":   "400",
		anaVideoOther:   "422",
		anaVideoMissing: "422",
	} {
		resp := fx.track(t, fx.ctx, video)
		got := fmt.Sprintf("%T", resp)
		if !strings.Contains(got, want) {
			t.Errorf("track %q: %s, want %s", video, got, want)
		}
	}
	resp := fx.track(t, fx.ctx, "https://youtu.be/"+anaVideoOwn+"?t=10")
	tracked, ok := resp.(httpgen.TrackAnalyticsVideo201JSONResponse)
	if !ok || tracked.VideoId != anaVideoOwn || tracked.Source != "manual" || tracked.DurationSeconds == nil || *tracked.DurationSeconds != 630 {
		t.Fatalf("track own video: %#v", resp)
	}
	if len(fx.jobs.args) != 1 || fx.jobs.args[0].Kind() != analytics.KindSync {
		t.Fatalf("tracking must queue a sync, got %v", fx.jobs.args)
	}

	// Nothing to explain before the first sync.
	if r, err := fx.api.ExplainAnalyticsChannel(fx.ctx, httpgen.ExplainAnalyticsChannelRequestObject{Id: fx.channel}); err != nil ||
		!strings.Contains(fmt.Sprintf("%T", r), "409") {
		t.Fatalf("explain before sync: %T %v", r, err)
	}

	if err := fx.syncer.SyncChannel(fx.ctx, fx.tenant, fx.channel); err != nil {
		t.Fatalf("sync: %v", err)
	}

	ov, err := fx.api.GetAnalyticsOverview(fx.ctx, httpgen.GetAnalyticsOverviewRequestObject{Id: fx.channel})
	if err != nil {
		t.Fatal(err)
	}
	overview, ok := ov.(httpgen.GetAnalyticsOverview200JSONResponse)
	if !ok {
		t.Fatalf("overview: %#v", ov)
	}
	st := overview.Sync
	if st.AnalyticsThrough == nil || st.AnalyticsThrough.Format(time.DateOnly) != "2026-09-25" {
		t.Fatalf("analytics through: %+v", st)
	}
	if st.ReachThrough == nil || st.ReachThrough.Format(time.DateOnly) != "2026-09-24" {
		t.Errorf("reach through: %+v", st.ReachThrough)
	}
	if st.SubscriberCount == nil || *st.SubscriberCount != 1500 || st.LastError != "" {
		t.Errorf("sync state: %+v", st)
	}
	if len(overview.Days) != 28 {
		t.Fatalf("default window must hold 28 days, got %d", len(overview.Days))
	}
	day := overview.Days[len(overview.Days)-1]
	if day.Views == nil || *day.Views != 100 {
		t.Errorf("channel day views: %+v", day)
	}
	// The rejected subscribers group is absent with a reason, never 0.
	if day.SubscribersLost != nil || day.SubscribersGained != nil || day.Unavailable[analytics.MetricSubscribersLost] == "" {
		t.Errorf("rejected metric must be unavailable: %+v", day)
	}

	row := fx.videoRow(t)
	if row.VideoId != anaVideoOwn || row.Views == nil || *row.Views != 100*25 { // published 2026-09-01: 25 days of the window
		t.Errorf("video row views: %+v", row)
	}
	if row.Impressions == nil || *row.Impressions != 4000 || row.Ctr == nil || *row.Ctr < 0.0199 || *row.Ctr > 0.0201 {
		t.Errorf("video reach: impressions %v ctr %v", row.Impressions, row.Ctr)
	}

	vd, err := fx.api.GetAnalyticsVideo(fx.ctx, httpgen.GetAnalyticsVideoRequestObject{VideoId: anaVideoOwn})
	if err != nil {
		t.Fatal(err)
	}
	detail, ok := vd.(httpgen.GetAnalyticsVideo200JSONResponse)
	if !ok || len(detail.Retention) != 100 || len(detail.Days) == 0 || detail.RetentionSyncedAt == nil {
		t.Fatalf("video detail: %T retention %d", vd, len(detail.Retention))
	}

	// A second sync lists the same report again: it is downloaded but not
	// counted twice, and the reporting job is reused.
	if err := fx.syncer.SyncChannel(fx.ctx, fx.tenant, fx.channel); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if row := fx.videoRow(t); row.Impressions == nil || *row.Impressions != 4000 {
		t.Errorf("reach after re-listing: %v", row.Impressions)
	}
	if n := fx.google.callCount("jobs.create"); n != 1 {
		t.Errorf("reporting job created %d times", n)
	}
	if n := fx.scalar(t, "SELECT count(*) FROM analytics_reporting_reports WHERE channel_id = $1"); n != 1 {
		t.Errorf("claimed reports: %d", n)
	}
	// Daily rows are upserts: the look-back window overlaps without
	// duplicating days.
	if n := fx.scalar(t, "SELECT count(*) FROM video_metrics_daily WHERE channel_id = $1 AND date = DATE '2026-09-25'"); n != 1 {
		t.Errorf("video rows for one day: %d", n)
	}

	ex, err := fx.api.ExplainAnalyticsChannel(fx.ctx, httpgen.ExplainAnalyticsChannelRequestObject{Id: fx.channel})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ex.(httpgen.ExplainAnalyticsChannel202JSONResponse); !ok || len(fx.engine.specs) != 1 {
		t.Fatalf("explain: %#v", ex)
	}
	input := string(fx.engine.specs[0].Steps[0].Input)
	if fx.engine.specs[0].Steps[0].Kind != analytics.KindExplain || strings.Contains(input, anaVideoTitle) || !strings.Contains(input, anaVideoOwn) {
		t.Errorf("explain input must carry ids and numbers only: %s", input)
	}
}

// TestAnalyticsIsTenantScoped: another tenant sees none of the channel's
// analytics and cannot act on it.
func TestAnalyticsIsTenantScoped(t *testing.T) {
	fx := newAnalyticsFixture(t)
	if _, ok := fx.track(t, fx.ctx, anaVideoOwn).(httpgen.TrackAnalyticsVideo201JSONResponse); !ok {
		t.Fatal("track own video")
	}
	if err := fx.syncer.SyncChannel(fx.ctx, fx.tenant, fx.channel); err != nil {
		t.Fatalf("sync: %v", err)
	}
	ctx := fx.ctxB
	check := func(name string, resp any, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := fmt.Sprintf("%T", resp); !strings.Contains(got, "404") {
			t.Errorf("%s from another tenant: %s, want 404", name, got)
		}
	}
	r1, err := fx.api.GetAnalyticsOverview(ctx, httpgen.GetAnalyticsOverviewRequestObject{Id: fx.channel})
	check("overview", r1, err)
	r2, err := fx.api.ListAnalyticsVideos(ctx, httpgen.ListAnalyticsVideosRequestObject{Params: httpgen.ListAnalyticsVideosParams{ChannelId: fx.channel}})
	check("videos", r2, err)
	r3, err := fx.api.GetAnalyticsVideo(ctx, httpgen.GetAnalyticsVideoRequestObject{VideoId: anaVideoOwn})
	check("video", r3, err)
	r4, err := fx.api.ListAnalyticsSuggestions(ctx, httpgen.ListAnalyticsSuggestionsRequestObject{Id: fx.channel})
	check("suggestions", r4, err)
	r5, err := fx.api.SyncAnalyticsChannel(ctx, httpgen.SyncAnalyticsChannelRequestObject{Id: fx.channel})
	check("sync", r5, err)
	r6, err := fx.api.ExplainAnalyticsChannel(ctx, httpgen.ExplainAnalyticsChannelRequestObject{Id: fx.channel})
	check("explain", r6, err)
	r7, err := fx.api.UntrackAnalyticsVideo(ctx, httpgen.UntrackAnalyticsVideoRequestObject{VideoId: anaVideoOwn})
	check("untrack", r7, err)
	check("track", fx.track(t, ctx, anaVideoOwn), nil)
	if len(fx.engine.specs) != 0 {
		t.Errorf("another tenant queued an explain")
	}
}

// TestAnalyticsDeadGrantMarksReconnectNeeded: a revoked grant fails the
// sync, records the error and flags the channel for reconnection.
func TestAnalyticsDeadGrantMarksReconnectNeeded(t *testing.T) {
	fx := newAnalyticsFixture(t)
	fx.clients.err = &youtube.APIError{Kind: youtube.KindAuth, Reason: youtube.ReasonReconnectNeeded, Message: "token revoked"}
	if err := fx.syncer.SyncChannel(fx.ctx, fx.tenant, fx.channel); !youtube.IsKind(err, youtube.KindAuth) {
		t.Fatalf("sync with a dead grant: %v", err)
	}
	var status string
	if err := fx.owner.QueryRow(context.Background(), "SELECT status FROM youtube_channels WHERE id = $1", fx.channel).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "reconnect_needed" {
		t.Errorf("channel status %q", status)
	}
	if n := fx.scalar(t, "SELECT count(*) FROM analytics_sync_state WHERE channel_id = $1 AND status = 'failed' AND last_error <> ''"); n != 1 {
		t.Errorf("failed sync state rows: %d", n)
	}
	r, err := fx.api.SyncAnalyticsChannel(fx.ctx, httpgen.SyncAnalyticsChannelRequestObject{Id: fx.channel})
	if err != nil || !strings.Contains(fmt.Sprintf("%T", r), "409") {
		t.Errorf("sync now on a disconnected channel: %T %v", r, err)
	}
}

func (fx *analyticsFixture) channelStatus(t *testing.T) string {
	t.Helper()
	var status string
	if err := fx.owner.QueryRow(context.Background(), "SELECT status FROM youtube_channels WHERE id = $1", fx.channel).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// TestAnalyticsSyncSkipsADisconnectedChannel: a sync queued before the
// user disconnected the channel does nothing and keeps the disconnect.
func TestAnalyticsSyncSkipsADisconnectedChannel(t *testing.T) {
	fx := newAnalyticsFixture(t)
	if _, err := fx.owner.Exec(context.Background(), "UPDATE youtube_channels SET status = 'disconnected' WHERE id = $1", fx.channel); err != nil {
		t.Fatal(err)
	}
	fx.clients.err = &youtube.APIError{Kind: youtube.KindAuth, Reason: youtube.ReasonReconnectNeeded, Message: "token deleted"}
	if err := fx.syncer.SyncChannel(fx.ctx, fx.tenant, fx.channel); !errors.Is(err, analytics.ErrChannelNotConnected) {
		t.Fatalf("sync of a disconnected channel: %v", err)
	}
	if s := fx.channelStatus(t); s != "disconnected" {
		t.Errorf("channel status %q, want disconnected", s)
	}
	if n := fx.scalar(t, "SELECT count(*) FROM analytics_sync_state WHERE channel_id = $1"); n != 0 {
		t.Errorf("sync state rows for a skipped sync: %d", n)
	}
	if n := fx.google.callCount("reports.query"); n != 0 {
		t.Errorf("reports.query calls for a skipped sync: %d", n)
	}
}

// TestAnalyticsDisconnectDuringSyncIsKept: a disconnect that lands while
// a sync is running wins over the sync's dead-grant failure.
func TestAnalyticsDisconnectDuringSyncIsKept(t *testing.T) {
	fx := newAnalyticsFixture(t)
	fx.clients.before = func() {
		if _, err := fx.owner.Exec(context.Background(), "UPDATE youtube_channels SET status = 'disconnected' WHERE id = $1", fx.channel); err != nil {
			t.Error(err)
		}
	}
	fx.clients.err = &youtube.APIError{Kind: youtube.KindAuth, Reason: youtube.ReasonReconnectNeeded, Message: "token deleted"}
	if err := fx.syncer.SyncChannel(fx.ctx, fx.tenant, fx.channel); !youtube.IsKind(err, youtube.KindAuth) {
		t.Fatalf("sync with a grant deleted mid-run: %v", err)
	}
	if s := fx.channelStatus(t); s != "disconnected" {
		t.Errorf("channel status %q, want disconnected", s)
	}
	if n := fx.scalar(t, "SELECT count(*) FROM analytics_sync_state WHERE channel_id = $1 AND status = 'failed'"); n != 1 {
		t.Errorf("failed sync state rows: %d", n)
	}
}

// TestAnalyticsLongNotesFitTheSyncState: two long side failures still
// finish the sync (idle, through dates advanced) with a bounded note.
func TestAnalyticsLongNotesFitTheSyncState(t *testing.T) {
	fx := newAnalyticsFixture(t)
	fx.google.failSide = strings.Repeat("ặ", 400)
	if err := fx.syncer.SyncChannel(fx.ctx, fx.tenant, fx.channel); err != nil {
		t.Fatalf("sync with failing side sources: %v", err)
	}
	var status, lastErr string
	var through *time.Time
	if err := fx.owner.QueryRow(context.Background(),
		"SELECT status, last_error, analytics_through FROM analytics_sync_state WHERE channel_id = $1", fx.channel,
	).Scan(&status, &lastErr, &through); err != nil {
		t.Fatal(err)
	}
	if status != "idle" || through == nil || !utf8.ValidString(lastErr) {
		t.Errorf("sync state: status %q, through %v, valid utf8 %v", status, through, utf8.ValidString(lastErr))
	}
	if n := utf8.RuneCountInString(lastErr); n != 500 || !strings.HasPrefix(lastErr, "channel statistics: ") || !strings.Contains(lastErr, "; reach report: ") {
		t.Errorf("last_error has %d characters: %.80q", n, lastErr)
	}
}
