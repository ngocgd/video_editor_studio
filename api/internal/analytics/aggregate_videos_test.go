package analytics

import (
	"errors"
	"testing"
)

func ids(rows []VideoTotals) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.VideoID)
	}
	return out
}

func TestSortVideosPutsUnavailableLast(t *testing.T) {
	rows := []VideoTotals{
		{VideoID: "c", CTR: ptr(0.05)},
		{VideoID: "a"}, // CTR not available
		{VideoID: "b", CTR: ptr(0.09)},
		{VideoID: "d", CTR: ptr(0.05)},
	}
	SortVideos(rows, SortCTR, true)
	if got := ids(rows); !equalKeys(got, []string{"b", "c", "d", "a"}) {
		t.Fatalf("desc = %v", got)
	}
	SortVideos(rows, SortCTR, false)
	if got := ids(rows); !equalKeys(got, []string{"c", "d", "b", "a"}) {
		t.Fatalf("asc = %v", got)
	}
}

func TestPaginateWalksAllRows(t *testing.T) {
	rows := make([]VideoTotals, 5)
	for i := range rows {
		rows[i].VideoID = string(rune('a' + i))
	}
	var seen []string
	cursor := ""
	for range 10 {
		page, err := Paginate(rows, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 5 {
			t.Fatalf("total = %d", page.Total)
		}
		seen = append(seen, ids(page.Rows)...)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if !equalKeys(seen, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("seen = %v", seen)
	}
}

func TestPaginateRejectsForeignCursors(t *testing.T) {
	for _, c := range []string{"!!", "eA", "OTk"} { // not base64, "x", "99"
		if _, err := Paginate(make([]VideoTotals, 3), c, 2); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %q: err = %v", c, err)
		}
	}
}

func TestPaginateClampsLimit(t *testing.T) {
	page, err := Paginate(make([]VideoTotals, MaxPageSize+10), "", 10_000)
	if err != nil || len(page.Rows) != MaxPageSize || page.NextCursor == "" {
		t.Fatalf("rows = %d cursor = %q err = %v", len(page.Rows), page.NextCursor, err)
	}
}

func TestValidSort(t *testing.T) {
	for _, k := range []string{SortViews, SortWatchTime, SortCTR, SortAverageViewPercentage, SortPublished} {
		if !ValidSort(k) {
			t.Errorf("%s rejected", k)
		}
	}
	if ValidSort("title") {
		t.Error("title accepted")
	}
}
