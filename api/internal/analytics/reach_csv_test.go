package analytics

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

const vidA = "aaaaaaaaaaa"
const vidB = "bbbbbbbbbb_"

func TestParseReachCSVAggregatesPerVideoDay(t *testing.T) {
	csv := "date,channel_id,video_id,traffic_source_type,video_thumbnail_impressions,video_thumbnail_impressions_ctr\n" +
		"20260920,UCx," + vidA + ",1,1000,0.05\n" +
		"20260920,UCx," + vidA + ",2,3000,0.01\n" +
		"20260920,UCx," + vidB + ",1,0,0\n" +
		"20260921,UCx," + vidA + ",1,10,0.1\n"
	p, err := ParseReachCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if p.Lines != 4 || p.Skipped != 0 || len(p.Rows) != 3 {
		t.Fatalf("parse = %+v", p)
	}
	a := p.Rows[0]
	if a.Video != vidA || a.Date != "2026-09-20" || a.Impressions != 4000 {
		t.Fatalf("first row = %+v", a)
	}
	// (1000*0.05 + 3000*0.01) / 4000 = 0.02
	if a.CTR == nil || math.Abs(*a.CTR-0.02) > 1e-12 {
		t.Fatalf("aggregated ctr = %v", a.CTR)
	}
	if b := p.Rows[1]; b.Video != vidB || b.Impressions != 0 || b.CTR != nil {
		t.Fatalf("zero-impression row = %+v, want nil ctr", b)
	}
}

func TestParseReachCSVMapsColumnsByNameAndIgnoresUnknown(t *testing.T) {
	csv := "\uFEFFvideo_thumbnail_impressions_ctr,new_column,video_thumbnail_impressions,video_id,date\n" +
		"0.5,\"=HYPERLINK(\"\"x\"\")\",2," + vidA + ",20260901\n"
	p, err := ParseReachCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || p.Rows[0].Impressions != 2 || *p.Rows[0].CTR != 0.5 || p.Rows[0].Date != "2026-09-01" {
		t.Fatalf("rows = %+v", p.Rows)
	}
}

func TestParseReachCSVHeaderDrift(t *testing.T) {
	_, err := ParseReachCSV(strings.NewReader("date,video_id,impressions,ctr\n"))
	if !errors.Is(err, ErrReachHeader) {
		t.Fatalf("err = %v, want ErrReachHeader", err)
	}
}

func TestParseReachCSVEmptyReport(t *testing.T) {
	for _, body := range []string{"", "date,video_id,video_thumbnail_impressions,video_thumbnail_impressions_ctr\n"} {
		p, err := ParseReachCSV(strings.NewReader(body))
		if err != nil || len(p.Rows) != 0 || p.Lines != 0 {
			t.Fatalf("empty report %q: %+v %v", body, p, err)
		}
	}
}

func TestParseReachCSVSkipsMalformedLines(t *testing.T) {
	long := strings.Repeat("9", maxReachField+1)
	csv := "date,video_id,video_thumbnail_impressions,video_thumbnail_impressions_ctr\n" +
		"20260920,short,1,0.1\n" + // bad video id
		"2026-09-20," + vidA + ",1,0.1\n" + // bad date format
		"20260920," + vidA + ",-5,0.1\n" + // negative count
		"20260920," + vidA + ",NaN,0.1\n" +
		"20260920," + vidA + "," + long + ",0.1\n" + // over-long field
		"20260920," + vidA + "\n" + // short line
		"20260920," + vidA + ",7,\n" // empty ctr reads as 0
	p, err := ParseReachCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if p.Lines != 7 || p.Skipped != 6 || len(p.Rows) != 1 || p.Rows[0].Impressions != 7 || *p.Rows[0].CTR != 0 {
		t.Fatalf("parse = %+v rows %+v", p, p.Rows)
	}
}

// A 100k-line report must parse well inside the performance budget.
func TestParseReachCSVLargeReport(t *testing.T) {
	var b strings.Builder
	b.WriteString("date,channel_id,video_id,video_thumbnail_impressions,video_thumbnail_impressions_ctr\n")
	for i := range 100_000 {
		fmt.Fprintf(&b, "202609%02d,UCx,%s%05d,%d,0.03\n", 1+i%28, "vid___", i%500, i%97)
	}
	start := time.Now()
	p, err := ParseReachCSV(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if p.Lines != 100_000 || p.Skipped != 0 {
		t.Fatalf("lines %d skipped %d", p.Lines, p.Skipped)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("parse took %v", d)
	}
}
