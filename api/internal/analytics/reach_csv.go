package analytics

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxReachField caps one CSV field: every column the parser reads is a
// short id, date or number, so anything longer is a malformed row.
const maxReachField = 256

// reachDateLayout is the Reporting API's date column format.
const reachDateLayout = "20060102"

// videoIDPattern is a YouTube video id; rows naming anything else are
// skipped rather than stored.
var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ErrReachHeader means a report lacks a column the parser needs.
var ErrReachHeader = errors.New("analytics: reach report header is missing a required column")

// ReachRow is one video and day of a reach report, aggregated across the
// report's other dimensions (traffic source, subscribed status, ...).
// CTR is nil when the day has no impressions.
type ReachRow struct {
	Video       string   `json:"video"`
	Date        string   `json:"date"`
	Impressions int64    `json:"impressions"`
	CTR         *float64 `json:"ctr"`
}

// ReachParse is a parsed report.
type ReachParse struct {
	Rows []ReachRow
	// Lines is the number of data lines read; Skipped of them were
	// malformed (bad id, date or number, or an over-long field).
	Lines, Skipped int
}

type reachKey struct{ video, date string }

type reachAgg struct {
	impressions int64
	// clicks is impressions weighted by each line's CTR, so the day's
	// CTR is clicks / impressions across all lines.
	clicks float64
}

// ParseReachCSV streams a reach report, reading it as data only (no
// formula evaluation) and never holding more than one line plus the
// per-video-day totals. Columns are found by header name; unknown
// columns are ignored. An empty body is an empty report.
func ParseReachCSV(r io.Reader) (ReachParse, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return ReachParse{}, nil
	}
	if err != nil {
		return ReachParse{}, fmt.Errorf("analytics: read reach header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF"))] = i
	}
	need := []string{ColumnDate, ColumnVideoID, ColumnImpressions, ColumnCTR}
	idx := make([]int, len(need))
	for i, name := range need {
		c, ok := col[name]
		if !ok {
			return ReachParse{}, fmt.Errorf("%w: %s", ErrReachHeader, name)
		}
		idx[i] = c
	}

	aggs := map[reachKey]*reachAgg{}
	var out ReachParse
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) && !errors.Is(pe.Err, csv.ErrQuote) && !errors.Is(pe.Err, csv.ErrBareQuote) {
				return ReachParse{}, fmt.Errorf("analytics: read reach report: %w", err)
			}
			out.Lines++
			out.Skipped++
			continue
		}
		out.Lines++
		k, impressions, ctr, ok := reachLine(rec, idx)
		if !ok {
			out.Skipped++
			continue
		}
		a := aggs[k]
		if a == nil {
			a = &reachAgg{}
			aggs[k] = a
		}
		a.impressions += impressions
		a.clicks += float64(impressions) * ctr
	}

	out.Rows = make([]ReachRow, 0, len(aggs))
	for k, a := range aggs {
		row := ReachRow{Video: k.video, Date: k.date, Impressions: a.impressions}
		if a.impressions > 0 {
			ctr := a.clicks / float64(a.impressions)
			row.CTR = &ctr
		}
		out.Rows = append(out.Rows, row)
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		if out.Rows[i].Date != out.Rows[j].Date {
			return out.Rows[i].Date < out.Rows[j].Date
		}
		return out.Rows[i].Video < out.Rows[j].Video
	})
	return out, nil
}

// reachLine validates one data line. idx holds the date, video id,
// impressions and CTR column positions.
func reachLine(rec []string, idx []int) (k reachKey, impressions int64, ctr float64, ok bool) {
	field := func(i int) (string, bool) {
		if idx[i] >= len(rec) || len(rec[idx[i]]) > maxReachField {
			return "", false
		}
		return strings.TrimSpace(rec[idx[i]]), true
	}
	date, ok1 := field(0)
	video, ok2 := field(1)
	imp, ok3 := field(2)
	rate, ok4 := field(3)
	if !ok1 || !ok2 || !ok3 || !ok4 || !videoIDPattern.MatchString(video) {
		return k, 0, 0, false
	}
	d, err := time.Parse(reachDateLayout, date)
	if err != nil {
		return k, 0, 0, false
	}
	n, err := strconv.ParseFloat(imp, 64)
	if err != nil || n < 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		return k, 0, 0, false
	}
	ctr = 0
	if rate != "" {
		ctr, err = strconv.ParseFloat(rate, 64)
		if err != nil || ctr < 0 || math.IsInf(ctr, 0) || math.IsNaN(ctr) {
			return k, 0, 0, false
		}
	}
	return reachKey{video: video, date: d.Format(time.DateOnly)}, int64(math.Round(n)), ctr, true
}
