package analytics

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// Video table sort keys.
const (
	SortViews                 = "views"
	SortWatchTime             = "watchTime"
	SortCTR                   = "ctr"
	SortAverageViewPercentage = "averageViewPercentage"
	SortPublished             = "published"
)

// Page size bounds of the video table.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// ErrBadCursor means the cursor is not one this server issued.
var ErrBadCursor = errors.New("analytics: invalid cursor")

// VideoPage is one page of the sorted video table.
type VideoPage struct {
	Rows       []VideoTotals
	Total      int
	NextCursor string
}

// sortValue is the sort key of v; ok is false when it is not available.
func sortValue(v VideoTotals, key string) (float64, bool) {
	switch key {
	case SortViews:
		return f64(v.Views)
	case SortWatchTime:
		return f64(v.WatchHours)
	case SortCTR:
		return f64(v.CTR)
	case SortAverageViewPercentage:
		return f64(v.AverageViewPercentage)
	case SortPublished:
		if v.PublishedAt == nil {
			return 0, false
		}
		return float64(v.PublishedAt.Unix()), true
	}
	return 0, false
}

func f64[T int64 | float64](p *T) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return float64(*p), true
}

// ValidSort reports whether key is a video table sort key.
func ValidSort(key string) bool {
	switch key {
	case SortViews, SortWatchTime, SortCTR, SortAverageViewPercentage, SortPublished:
		return true
	}
	return false
}

// SortVideos orders rows by key; unavailable values always sort last and
// ties break by video id, so pages are stable.
func SortVideos(rows []VideoTotals, key string, desc bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, aok := sortValue(rows[i], key)
		b, bok := sortValue(rows[j], key)
		switch {
		case aok != bok:
			return aok
		case aok && a != b:
			if desc {
				return a > b
			}
			return a < b
		}
		return rows[i].VideoID < rows[j].VideoID
	})
}

// Paginate returns the page of sorted rows starting at cursor (an opaque
// offset issued by a previous page; "" is the first page).
func Paginate(rows []VideoTotals, cursor string, limit int) (VideoPage, error) {
	if limit <= 0 {
		limit = DefaultPageSize
	}
	limit = min(limit, MaxPageSize)
	offset := 0
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return VideoPage{}, ErrBadCursor
		}
		n, err := strconv.Atoi(string(raw))
		if err != nil || n < 0 || n > len(rows) {
			return VideoPage{}, ErrBadCursor
		}
		offset = n
	}
	end := min(offset+limit, len(rows))
	page := VideoPage{Rows: rows[offset:end], Total: len(rows)}
	if end < len(rows) {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	return page, nil
}

// VideoDay is one day of a video's metrics; nil is not available.
type VideoDay struct {
	Date                  time.Time
	Views                 *int64
	WatchHours            *float64
	AverageViewDuration   *float64
	AverageViewPercentage *float64
	SubscribersGained     *int64
	Impressions           *int64
	CTR                   *float64
	Unavailable           map[string]string
}

// RetentionBucket is one point of a video's retention curve.
type RetentionBucket struct {
	ElapsedRatio                 float64
	AudienceWatchRatio           *float64
	RelativeRetentionPerformance *float64
}

// VideoDetail is the per-video view: the tracked video, its daily series
// over a window and its lifetime retention curve.
type VideoDetail struct {
	Video           dbgen.AnalyticsTrackedVideo
	Days            []VideoDay
	Retention       []RetentionBucket
	RetentionSynced *time.Time
}

// Video reads a tracked video's detail; ErrNotTracked when the tenant
// does not track it.
func (a *Aggregator) Video(ctx context.Context, tenantID uuid.UUID, videoID string, w Window) (VideoDetail, error) {
	tenant := idconv.ToPg(tenantID)
	tv, err := a.Queries.GetTrackedVideo(ctx, dbgen.GetTrackedVideoParams{TenantID: tenant, YoutubeVideoID: videoID})
	if errors.Is(err, pgx.ErrNoRows) {
		return VideoDetail{}, ErrNotTracked
	}
	if err != nil {
		return VideoDetail{}, fmt.Errorf("analytics: read tracked video: %w", err)
	}
	rows, err := a.Queries.VideoDailySeries(ctx, dbgen.VideoDailySeriesParams{
		TenantID: tenant, YoutubeVideoID: videoID, FromDate: pgDate(w.From), ToDate: pgDate(w.To),
	})
	if err != nil {
		return VideoDetail{}, fmt.Errorf("analytics: video series: %w", err)
	}
	out := VideoDetail{Video: tv, Days: make([]VideoDay, 0, len(rows))}
	for _, r := range rows {
		d := VideoDay{
			Date: r.Date.Time, Views: int8Ptr(r.Views),
			AverageViewDuration: float8Ptr(r.AverageViewDuration), AverageViewPercentage: float8Ptr(r.AverageViewPercentage),
			SubscribersGained: int8Ptr(r.SubscribersGained), Impressions: int8Ptr(r.Impressions), CTR: float8Ptr(r.Ctr),
			Unavailable: decodeReasons(r.Unavailable),
		}
		if m := float8Ptr(r.EstimatedMinutesWatched); m != nil {
			h := *m / 60
			d.WatchHours = &h
		}
		out.Days = append(out.Days, d)
	}
	curve, err := a.Queries.VideoRetentionCurve(ctx, dbgen.VideoRetentionCurveParams{TenantID: tenant, YoutubeVideoID: videoID})
	if err != nil {
		return VideoDetail{}, fmt.Errorf("analytics: retention: %w", err)
	}
	for _, c := range curve {
		out.Retention = append(out.Retention, RetentionBucket{
			ElapsedRatio: c.ElapsedRatio, AudienceWatchRatio: float8Ptr(c.AudienceWatchRatio),
			RelativeRetentionPerformance: float8Ptr(c.RelativeRetentionPerformance),
		})
		out.RetentionSynced = tsPtr(c.SyncedAt)
	}
	return out, nil
}

// pgDate is a calendar day as a pgtype.Date.
func pgDate(t time.Time) pgtype.Date { return pgtype.Date{Time: day(t), Valid: true} }
