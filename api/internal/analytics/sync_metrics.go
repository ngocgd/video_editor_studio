package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/youtube"
)

// queryPageSize pages a daily query; a year of days fits in two pages.
const queryPageSize = 200

// fetchDaily runs each metric group over w with dimensions=day and the
// given filter. A group the API rejects is stored as null with Google's
// reason; any other error aborts.
func fetchDaily(ctx context.Context, ac *youtube.AnalyticsClient, groups []MetricGroup, filters string, w Window) (dailyTable, error) {
	d := dailyTable{}
	type gap struct {
		metrics []string
		reason  string
	}
	var gaps []gap
	for _, g := range groups {
		t, err := ac.QueryAll(ctx, youtube.ReportQuery{
			Start: w.From, End: w.To, Metrics: g.Metrics,
			Dimensions: []string{DimensionDay}, Filters: filters, Sort: DimensionDay,
		}, queryPageSize)
		if err != nil {
			reason, ok := unavailableReason(err)
			if !ok {
				return nil, err
			}
			gaps = append(gaps, gap{g.Metrics, reason})
			continue
		}
		d.add(t, g.Metrics)
	}
	for _, g := range gaps {
		d.markUnavailable(g.metrics, g.reason, w)
	}
	return d, nil
}

func (s *Syncer) syncChannelDaily(ctx context.Context, ref channelRef, ac *youtube.AnalyticsClient, w Window) error {
	d, err := fetchDaily(ctx, ac, ChannelDailyGroups, "", w)
	if err != nil {
		return err
	}
	rows, err := json.Marshal(d.channelRows())
	if err != nil {
		return err
	}
	if err := s.Queries.UpsertChannelMetricsDaily(ctx, dbgen.UpsertChannelMetricsDailyParams{
		TenantID: ref.tenant, ChannelID: ref.channel, Rows: rows,
	}); err != nil {
		return fmt.Errorf("analytics: store channel metrics: %w", err)
	}
	return nil
}

// syncVideos syncs every tracked video of the channel, a few at a time.
// The first error stops new videos from starting and is returned.
func (s *Syncer) syncVideos(ctx context.Context, ref channelRef, ac *youtube.AnalyticsClient, cursor *time.Time, end time.Time) error {
	videos, err := s.Queries.ListTrackedVideos(ctx, dbgen.ListTrackedVideosParams{TenantID: ref.tenant, ChannelID: ref.channel})
	if err != nil {
		return fmt.Errorf("analytics: list tracked videos: %w", err)
	}
	synced, err := s.Queries.ListSyncedVideoIDs(ctx, dbgen.ListSyncedVideoIDsParams{TenantID: ref.tenant, ChannelID: ref.channel})
	if err != nil {
		return fmt.Errorf("analytics: list synced videos: %w", err)
	}
	hasHistory := make(map[string]bool, len(synced))
	for _, id := range synced {
		hasHistory[id] = true
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	limit := s.Concurrency
	if limit <= 0 {
		limit = 4
	}
	sem := make(chan struct{}, limit)
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	for _, v := range videos {
		var published *time.Time
		if v.PublishedAt.Valid {
			published = &v.PublishedAt.Time
		}
		w, ok := VideoWindow(cursor, published, hasHistory[v.YoutubeVideoID], end)
		if !ok {
			continue
		}
		lifetime, _ := VideoWindow(nil, published, false, end)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(video string, w, lifetime Window) {
			defer func() { <-sem; wg.Done() }()
			if err := s.syncVideo(ctx, ref, ac, video, w, lifetime); err != nil {
				once.Do(func() { firstErr = err; cancel() })
			}
		}(v.YoutubeVideoID, w, lifetime)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// syncVideo stores one video's daily metrics over w and refreshes its
// retention curve over the video's lifetime window.
func (s *Syncer) syncVideo(ctx context.Context, ref channelRef, ac *youtube.AnalyticsClient, video string, w, lifetime Window) error {
	filter := "video==" + video
	d, err := fetchDaily(ctx, ac, VideoDailyGroups, filter, w)
	if err != nil {
		return err
	}
	rows, err := json.Marshal(d.videoRows(video))
	if err != nil {
		return err
	}
	if err := s.Queries.UpsertVideoAnalyticsDaily(ctx, dbgen.UpsertVideoAnalyticsDailyParams{
		TenantID: ref.tenant, ChannelID: ref.channel, Rows: rows, ClearKeys: groupKeys(VideoDailyGroups),
	}); err != nil {
		return fmt.Errorf("analytics: store video metrics: %w", err)
	}
	return s.syncRetention(ctx, ref, ac, video, lifetime)
}
