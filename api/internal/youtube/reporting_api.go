package youtube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultMaxReportBytes caps one bulk report download.
const DefaultMaxReportBytes = 50 << 20

// maxListPages bounds every pageToken loop.
const maxListPages = 100

// ErrReportTooLarge means a report exceeded the download cap. It is
// permanent: the same report will be just as large next time.
var ErrReportTooLarge = &APIError{Kind: KindPermanent, Reason: "report_too_large", Message: "bulk report exceeds the download size cap"}

// ErrUntrustedDownloadURL means a report's downloadUrl does not point at
// a Google API host, so the channel's credentials are not sent there.
var ErrUntrustedDownloadURL = &APIError{Kind: KindPermanent, Reason: "untrusted_download_url", Message: "report download URL is not a Google API host"}

// ReportType is one bulk report type the Reporting API offers.
type ReportType struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DeprecateTime string `json:"deprecateTime,omitempty"`
	SystemManaged bool   `json:"systemManaged,omitempty"`
}

// ReportingJob asks Google to generate one report type daily.
type ReportingJob struct {
	ID            string    `json:"id"`
	ReportTypeID  string    `json:"reportTypeId"`
	Name          string    `json:"name"`
	CreateTime    time.Time `json:"createTime"`
	SystemManaged bool      `json:"systemManaged,omitempty"`
}

// BulkReport is one generated report of a job, covering [StartTime,
// EndTime). DownloadURL is a capability URL: never log it.
type BulkReport struct {
	ID          string    `json:"id"`
	JobID       string    `json:"jobId"`
	StartTime   time.Time `json:"startTime"`
	EndTime     time.Time `json:"endTime"`
	CreateTime  time.Time `json:"createTime"`
	DownloadURL string    `json:"downloadUrl"`
}

// listAll follows nextPageToken, handing each page to collect.
func (c *AnalyticsClient) listAll(ctx context.Context, op Op, path string, q url.Values, page func() any, collect func(any) string) error {
	for range maxListPages {
		out := page()
		if err := c.do(ctx, op, http.MethodGet, c.reportingBase()+path+"?"+q.Encode(), nil, out); err != nil {
			return err
		}
		next := collect(out)
		if next == "" {
			return nil
		}
		q.Set("pageToken", next)
	}
	return fmt.Errorf("youtube: %s exceeded %d pages", op, maxListPages)
}

// ReportTypes lists every report type available to the channel.
func (c *AnalyticsClient) ReportTypes(ctx context.Context) ([]ReportType, error) {
	type pageT struct {
		ReportTypes   []ReportType `json:"reportTypes"`
		NextPageToken string       `json:"nextPageToken"`
	}
	var all []ReportType
	err := c.listAll(ctx, OpReportTypesList, "/reportTypes", url.Values{}, func() any { return &pageT{} }, func(p any) string {
		pg := p.(*pageT)
		all = append(all, pg.ReportTypes...)
		return pg.NextPageToken
	})
	return all, err
}

// Jobs lists the channel's reporting jobs (not system-managed ones).
func (c *AnalyticsClient) Jobs(ctx context.Context) ([]ReportingJob, error) {
	type pageT struct {
		Jobs          []ReportingJob `json:"jobs"`
		NextPageToken string         `json:"nextPageToken"`
	}
	var all []ReportingJob
	err := c.listAll(ctx, OpJobsList, "/jobs", url.Values{}, func() any { return &pageT{} }, func(p any) string {
		pg := p.(*pageT)
		all = append(all, pg.Jobs...)
		return pg.NextPageToken
	})
	return all, err
}

// CreateJob starts daily generation of reportTypeID.
func (c *AnalyticsClient) CreateJob(ctx context.Context, reportTypeID, name string) (ReportingJob, error) {
	var j ReportingJob
	body := map[string]string{"reportTypeId": reportTypeID, "name": name}
	err := c.do(ctx, OpJobsCreate, http.MethodPost, c.reportingBase()+"/jobs", body, &j)
	return j, err
}

// DeleteJob stops a job (rollback of the analytics feature).
func (c *AnalyticsClient) DeleteJob(ctx context.Context, jobID string) error {
	return c.do(ctx, OpJobsDelete, http.MethodDelete, c.reportingBase()+"/jobs/"+url.PathEscape(jobID), nil, nil)
}

// Reports lists a job's reports created after createdAfter (zero lists
// all that Google still keeps).
func (c *AnalyticsClient) Reports(ctx context.Context, jobID string, createdAfter time.Time) ([]BulkReport, error) {
	type pageT struct {
		Reports       []BulkReport `json:"reports"`
		NextPageToken string       `json:"nextPageToken"`
	}
	q := url.Values{}
	if !createdAfter.IsZero() {
		q.Set("createdAfter", createdAfter.UTC().Format(time.RFC3339Nano))
	}
	var all []BulkReport
	err := c.listAll(ctx, OpReportsList, "/jobs/"+url.PathEscape(jobID)+"/reports", q, func() any { return &pageT{} }, func(p any) string {
		pg := p.(*pageT)
		all = append(all, pg.Reports...)
		return pg.NextPageToken
	})
	return all, err
}

// trustedDownload reports whether u may receive the channel's
// credentials: an https Google API host, or the configured Reporting
// base host (the test double).
func (c *AnalyticsClient) trustedDownload(u *url.URL) bool {
	if base, err := url.Parse(c.reportingBase()); err == nil && u.Scheme == base.Scheme && u.Host == base.Host {
		return true
	}
	host := u.Hostname()
	return u.Scheme == "https" && (host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com"))
}

// Download opens a report's CSV as a stream. The body is capped at
// MaxReportBytes: reading past the cap fails with ErrReportTooLarge
// rather than silently truncating. The caller closes the reader.
func (c *AnalyticsClient) Download(ctx context.Context, r BulkReport) (io.ReadCloser, error) {
	u, err := url.Parse(r.DownloadURL)
	if err != nil || !c.trustedDownload(u) {
		return nil, ErrUntrustedDownloadURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrUntrustedDownloadURL
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, transportError(OpReportDownload, scrubURL(err))
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, classify(resp.StatusCode, b)
	}
	limit := c.MaxReportBytes
	if limit <= 0 {
		limit = DefaultMaxReportBytes
	}
	if resp.ContentLength > limit {
		_ = resp.Body.Close()
		return nil, ErrReportTooLarge
	}
	return &cappedBody{rc: resp.Body, left: limit}, nil
}

// cappedBody fails once more than left bytes arrive.
type cappedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrReportTooLarge
	}
	// Read one byte past the cap so an exactly-full body still ends in EOF.
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return 0, ErrReportTooLarge
	}
	if err != nil && !errors.Is(err, io.EOF) {
		err = scrubURL(err)
	}
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }
