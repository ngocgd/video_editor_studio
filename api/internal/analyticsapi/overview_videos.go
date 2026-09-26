package analyticsapi

import (
	"context"
	"errors"
	"fmt"

	"loomtale/api/internal/analytics"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/tenant"
)

// GetAnalyticsOverview implements gen.StrictServerInterface.
func (h *AnalyticsAPI) GetAnalyticsOverview(ctx context.Context, request gen.GetAnalyticsOverviewRequestObject) (gen.GetAnalyticsOverviewResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, ok, err := h.channel(ctx, info.ID, request.Id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return gen.GetAnalyticsOverview404ApplicationProblemPlusJSONResponse{AnalyticsChannelNotFoundApplicationProblemPlusJSONResponse: channelNotFound()}, nil
	}
	st, err := h.Aggregator.SyncStatus(ctx, info.ID, request.Id)
	if err != nil {
		return nil, err
	}
	w, err := h.window(st, request.Params.From, request.Params.To)
	if err != nil {
		return gen.GetAnalyticsOverview400ApplicationProblemPlusJSONResponse{AnalyticsBadRequestApplicationProblemPlusJSONResponse: badRequest(err.Error())}, nil
	}
	ov, err := h.Aggregator.Overview(ctx, info.ID, request.Id, st, w)
	if err != nil {
		return nil, err
	}
	days := make([]gen.AnalyticsChannelDay, 0, len(ov.Days))
	for _, d := range ov.Days {
		days = append(days, gen.AnalyticsChannelDay{
			Date: date(d.Date), Views: d.Views, WatchHours: d.WatchHours,
			SubscribersGained: d.SubscribersGained, SubscribersLost: d.SubscribersLost,
			Unavailable: unavailable(d.Unavailable),
		})
	}
	return gen.GetAnalyticsOverview200JSONResponse(gen.AnalyticsOverview{
		Sync:   toSyncState(ov.Sync),
		Window: toWindow(ov.Window),
		Days:   days,
		Ypp: gen.YPPProgress{
			WatchHours: ov.YPP.WatchHours, WatchHoursTarget: ov.YPP.WatchHoursTarget,
			WindowFrom: date(ov.YPP.WindowFrom), WindowTo: date(ov.YPP.WindowTo),
			DaysMissing: ov.YPP.DaysMissing, Subscribers: ov.YPP.Subscribers,
			SubscribersTarget: ov.YPP.SubscribersTarget,
		},
	}), nil
}

// SyncAnalyticsChannel implements gen.StrictServerInterface: it queues a
// sync job; River's uniqueness keeps it to one waiting or running sync
// per channel.
func (h *AnalyticsAPI) SyncAnalyticsChannel(ctx context.Context, request gen.SyncAnalyticsChannelRequestObject) (gen.SyncAnalyticsChannelResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	ch, ok, err := h.channel(ctx, info.ID, request.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.SyncAnalyticsChannel404ApplicationProblemPlusJSONResponse{AnalyticsChannelNotFoundApplicationProblemPlusJSONResponse: channelNotFound()}, nil
	}
	if ch.Status != "connected" {
		return gen.SyncAnalyticsChannel409ApplicationProblemPlusJSONResponse(problem(409, "channel not connected",
			"connect the channel again in Settings > YouTube before syncing")), nil
	}
	res, err := h.Jobs.Insert(ctx, analytics.SyncArgs{TenantID: info.ID, ChannelID: request.Id}, nil)
	if err != nil {
		return nil, fmt.Errorf("analyticsapi: queue sync: %w", err)
	}
	return gen.SyncAnalyticsChannel202JSONResponse(gen.AnalyticsSyncQueued{Queued: !res.UniqueSkippedAsDuplicate}), nil
}

// ListAnalyticsVideos implements gen.StrictServerInterface.
func (h *AnalyticsAPI) ListAnalyticsVideos(ctx context.Context, request gen.ListAnalyticsVideosRequestObject) (gen.ListAnalyticsVideosResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	p := request.Params
	if _, ok, err := h.channel(ctx, info.ID, p.ChannelId); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return gen.ListAnalyticsVideos404ApplicationProblemPlusJSONResponse{AnalyticsChannelNotFoundApplicationProblemPlusJSONResponse: channelNotFound()}, nil
	}
	bad := func(detail string) gen.ListAnalyticsVideosResponseObject {
		return gen.ListAnalyticsVideos400ApplicationProblemPlusJSONResponse{AnalyticsBadRequestApplicationProblemPlusJSONResponse: badRequest(detail)}
	}
	sortKey, desc := analytics.SortViews, true
	if p.Sort != nil {
		sortKey = string(*p.Sort)
	}
	if !analytics.ValidSort(sortKey) {
		return bad("unknown sort key"), nil
	}
	if p.Order != nil {
		desc = *p.Order == gen.Desc
	}
	limit, cursor := 0, ""
	if p.Limit != nil {
		limit = *p.Limit
	}
	if p.Cursor != nil {
		cursor = *p.Cursor
	}

	st, err := h.Aggregator.SyncStatus(ctx, info.ID, p.ChannelId)
	if err != nil {
		return nil, err
	}
	w, err := h.window(st, p.From, p.To)
	if err != nil {
		return bad(err.Error()), nil
	}
	rows, err := h.Aggregator.VideoTotals(ctx, info.ID, p.ChannelId, w)
	if err != nil {
		return nil, err
	}
	analytics.SortVideos(rows, sortKey, desc)
	page, err := analytics.Paginate(rows, cursor, limit)
	if errors.Is(err, analytics.ErrBadCursor) {
		return bad("invalid cursor"), nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]gen.AnalyticsVideoRow, 0, len(page.Rows))
	for _, v := range page.Rows {
		items = append(items, gen.AnalyticsVideoRow{
			VideoId: v.VideoID, Title: v.Title, Source: gen.AnalyticsVideoRowSource(v.Source),
			DurationSeconds: intPtr(v.DurationSeconds), PublishedAt: v.PublishedAt,
			Views: v.Views, WatchHours: v.WatchHours, AverageViewPercentage: v.AverageViewPercentage,
			AverageViewDuration: v.AverageViewDuration, SubscribersGained: v.SubscribersGained,
			Impressions: v.Impressions, Ctr: v.CTR,
		})
	}
	out := gen.AnalyticsVideoPage{Items: items, Total: page.Total, Window: toWindow(w), Sync: toSyncState(st)}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return gen.ListAnalyticsVideos200JSONResponse(out), nil
}

// GetAnalyticsVideo implements gen.StrictServerInterface.
func (h *AnalyticsAPI) GetAnalyticsVideo(ctx context.Context, request gen.GetAnalyticsVideoRequestObject) (gen.GetAnalyticsVideoResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	notFound := gen.GetAnalyticsVideo404ApplicationProblemPlusJSONResponse{AnalyticsVideoNotFoundApplicationProblemPlusJSONResponse: videoNotFound()}
	tv, ok, err := h.trackedVideo(ctx, info.ID, request.VideoId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return notFound, nil
	}
	channelID := idconv.FromPg(tv.ChannelID)
	st, err := h.Aggregator.SyncStatus(ctx, info.ID, channelID)
	if err != nil {
		return nil, err
	}
	w, err := h.window(st, request.Params.From, request.Params.To)
	if err != nil {
		return gen.GetAnalyticsVideo400ApplicationProblemPlusJSONResponse{AnalyticsBadRequestApplicationProblemPlusJSONResponse: badRequest(err.Error())}, nil
	}
	detail, err := h.Aggregator.Video(ctx, info.ID, request.VideoId, w)
	if errors.Is(err, analytics.ErrNotTracked) {
		return notFound, nil
	}
	if err != nil {
		return nil, err
	}
	days := make([]gen.AnalyticsVideoDay, 0, len(detail.Days))
	for _, d := range detail.Days {
		days = append(days, gen.AnalyticsVideoDay{
			Date: date(d.Date), Views: d.Views, WatchHours: d.WatchHours,
			AverageViewDuration: d.AverageViewDuration, AverageViewPercentage: d.AverageViewPercentage,
			SubscribersGained: d.SubscribersGained, Impressions: d.Impressions, Ctr: d.CTR,
			Unavailable: unavailable(d.Unavailable),
		})
	}
	retention := make([]gen.RetentionPoint, 0, len(detail.Retention))
	for _, r := range detail.Retention {
		retention = append(retention, gen.RetentionPoint{
			ElapsedRatio: r.ElapsedRatio, AudienceWatchRatio: r.AudienceWatchRatio,
			RelativeRetentionPerformance: r.RelativeRetentionPerformance,
		})
	}
	return gen.GetAnalyticsVideo200JSONResponse(gen.AnalyticsVideoDetail{
		Video: toTrackedVideo(detail.Video), Window: toWindow(w), Sync: toSyncState(st),
		Days: days, Retention: retention, RetentionSyncedAt: detail.RetentionSynced,
	}), nil
}
