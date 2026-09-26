package analytics

import (
	"context"
	"encoding/json"
	"fmt"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/youtube"
)

// fetchRetention reads a video's audience retention curve over w (about
// 100 elapsedVideoTimeRatio buckets). When the API rejects the relative
// performance metric, the curve is fetched without it. ok is false when
// the curve itself is not available, so the stored one is kept.
func fetchRetention(ctx context.Context, ac *youtube.AnalyticsClient, video string, w Window) (rows []retentionRow, ok bool, err error) {
	q := youtube.ReportQuery{
		Start: w.From, End: w.To,
		Dimensions: []string{DimensionElapsedVideoTimeRatio},
		Filters:    "video==" + video,
		Sort:       DimensionElapsedVideoTimeRatio,
	}
	for _, metrics := range [][]string{RetentionMetrics, {MetricAudienceWatchRatio}} {
		q.Metrics = metrics
		t, err := ac.QueryAll(ctx, q, queryPageSize)
		if err == nil {
			return retentionRows(t), true, nil
		}
		if _, gap := unavailableReason(err); !gap {
			return nil, false, err
		}
	}
	return nil, false, nil
}

// syncRetention replaces the stored curve with the fetched one in one
// transaction. An empty answer (no views yet) keeps the stored curve.
func (s *Syncer) syncRetention(ctx context.Context, ref channelRef, ac *youtube.AnalyticsClient, video string, w Window) error {
	rows, ok, err := fetchRetention(ctx, ac, video, w)
	if err != nil || !ok || len(rows) == 0 {
		return err
	}
	body, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("analytics: begin retention: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)
	if err := q.DeleteVideoRetention(ctx, dbgen.DeleteVideoRetentionParams{TenantID: ref.tenant, YoutubeVideoID: video}); err != nil {
		return fmt.Errorf("analytics: clear retention: %w", err)
	}
	if err := q.InsertVideoRetention(ctx, dbgen.InsertVideoRetentionParams{
		TenantID: ref.tenant, ChannelID: ref.channel, YoutubeVideoID: video, Rows: body,
	}); err != nil {
		return fmt.Errorf("analytics: store retention: %w", err)
	}
	return tx.Commit(ctx)
}
