package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Official Analytics and Reporting API bases. Tests point them at an
// httptest double.
const (
	DefaultAnalyticsBase = "https://youtubeanalytics.googleapis.com/v2"
	DefaultReportingBase = "https://youtubereporting.googleapis.com/v1"
)

// Analytics and Reporting API operations. They draw on their own Google
// quotas, not the Data API pool, so they are never charged to the Ledger.
const (
	OpReportsQuery    Op = "analytics.reports.query"
	OpReportTypesList Op = "reporting.reportTypes.list"
	OpJobsList        Op = "reporting.jobs.list"
	OpJobsCreate      Op = "reporting.jobs.create"
	OpJobsDelete      Op = "reporting.jobs.delete"
	OpReportsList     Op = "reporting.jobs.reports.list"
	OpReportDownload  Op = "reporting.media.download"
)

// AnalyticsClient calls the YouTube Analytics API (reports.query) and
// the YouTube Reporting API (report types, jobs, reports, downloads) for
// one channel. Like Client, HTTP must already authorize requests as that
// channel (the yt-analytics.readonly scope covers both APIs).
type AnalyticsClient struct {
	HTTP *http.Client
	// AnalyticsBase and ReportingBase default to the official endpoints.
	AnalyticsBase string
	ReportingBase string
	// MaxReportBytes caps one report download (default 50 MiB).
	MaxReportBytes int64
}

func (c *AnalyticsClient) analyticsBase() string {
	if c.AnalyticsBase != "" {
		return strings.TrimRight(c.AnalyticsBase, "/")
	}
	return DefaultAnalyticsBase
}

func (c *AnalyticsClient) reportingBase() string {
	if c.ReportingBase != "" {
		return strings.TrimRight(c.ReportingBase, "/")
	}
	return DefaultReportingBase
}

// scrubURL drops the request URL from a transport error: report download
// URLs are capability URLs and must not reach logs or step results.
func scrubURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// do sends one request with an optional JSON body and decodes a 2xx JSON
// answer into out (nil skips decoding). Failures are classified like the
// Data API's, without touching the Data API quota ledger.
func (c *AnalyticsClient) do(ctx context.Context, op Op, method, rawURL string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return transportError(op, scrubURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return classify(resp.StatusCode, b)
	}
	if out == nil {
		return nil
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("youtube: decode %s: %w", op, err)
	}
	return nil
}

// ReportQuery is one reports.query request for the authorized channel
// (ids=channel==MINE). Dates are inclusive calendar days.
type ReportQuery struct {
	Start, End time.Time
	Metrics    []string
	Dimensions []string
	// Filters is the raw filters parameter, e.g. "video==a,b".
	Filters string
	Sort    string
	// MaxResults and StartIndex (1-based) page through large tables; zero
	// leaves them to the API's defaults.
	MaxResults int
	StartIndex int
}

// ColumnHeader describes one column of a result table.
type ColumnHeader struct {
	Name       string `json:"name"`
	ColumnType string `json:"columnType"`
	DataType   string `json:"dataType"`
}

// ResultTable is a reports.query answer. Cells are json.Number for
// numeric columns and string for dimension values.
type ResultTable struct {
	ColumnHeaders []ColumnHeader `json:"columnHeaders"`
	Rows          [][]any        `json:"rows"`
}

// Column returns the index of the named column, or -1.
func (t ResultTable) Column(name string) int {
	for i, h := range t.ColumnHeaders {
		if h.Name == name {
			return i
		}
	}
	return -1
}

// Float reads a numeric cell; ok is false for a missing or non-numeric
// cell, so a gap is never mistaken for zero.
func Float(row []any, col int) (v float64, ok bool) {
	if col < 0 || col >= len(row) {
		return 0, false
	}
	switch x := row[col].(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// String reads a dimension cell ("" when missing).
func String(row []any, col int) string {
	if col < 0 || col >= len(row) {
		return ""
	}
	if s, ok := row[col].(string); ok {
		return s
	}
	return fmt.Sprint(row[col])
}

// Query runs one reports.query page. A metric or dimension combination
// the API rejects comes back as a KindPermanent *APIError (HTTP 400), so
// the caller can store the gap with Google's reason instead of zeros.
func (c *AnalyticsClient) Query(ctx context.Context, q ReportQuery) (ResultTable, error) {
	if len(q.Metrics) == 0 {
		return ResultTable{}, errors.New("youtube: reports.query needs at least one metric")
	}
	v := url.Values{
		"ids":       {"channel==MINE"},
		"startDate": {q.Start.Format(analyticsDateLayout)},
		"endDate":   {q.End.Format(analyticsDateLayout)},
		"metrics":   {strings.Join(q.Metrics, ",")},
	}
	if len(q.Dimensions) > 0 {
		v.Set("dimensions", strings.Join(q.Dimensions, ","))
	}
	if q.Filters != "" {
		v.Set("filters", q.Filters)
	}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	if q.MaxResults > 0 {
		v.Set("maxResults", strconv.Itoa(q.MaxResults))
	}
	if q.StartIndex > 0 {
		v.Set("startIndex", strconv.Itoa(q.StartIndex))
	}
	var t ResultTable
	err := c.do(ctx, OpReportsQuery, http.MethodGet, c.analyticsBase()+"/reports?"+v.Encode(), nil, &t)
	return t, err
}

// analyticsDateLayout is the Analytics API date parameter format.
const analyticsDateLayout = "2006-01-02"

// maxQueryPages bounds QueryAll so a misbehaving answer cannot loop.
const maxQueryPages = 200

// QueryAll pages through a query pageSize rows at a time and returns the
// concatenated table.
func (c *AnalyticsClient) QueryAll(ctx context.Context, q ReportQuery, pageSize int) (ResultTable, error) {
	if pageSize <= 0 {
		pageSize = 200
	}
	q.MaxResults = pageSize
	var all ResultTable
	for page := range maxQueryPages {
		q.StartIndex = page*pageSize + 1
		t, err := c.Query(ctx, q)
		if err != nil {
			return ResultTable{}, err
		}
		if page == 0 {
			all.ColumnHeaders = t.ColumnHeaders
		}
		all.Rows = append(all.Rows, t.Rows...)
		if len(t.Rows) < pageSize {
			return all, nil
		}
	}
	return ResultTable{}, fmt.Errorf("youtube: reports.query exceeded %d pages", maxQueryPages)
}
