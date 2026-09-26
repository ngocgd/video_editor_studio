package analyticsapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"loomtale/api/internal/analytics"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/tenant"
	"loomtale/api/internal/youtube"
)

var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// youTubeHosts are the hosts a pasted video URL may use.
var youTubeHosts = map[string]bool{
	"youtube.com": true, "www.youtube.com": true, "m.youtube.com": true, "music.youtube.com": true,
	"youtu.be": true, "www.youtube-nocookie.com": true, "youtube-nocookie.com": true,
}

// ErrNotAVideo means the input is neither a video id nor a YouTube video
// URL.
var ErrNotAVideo = errors.New("not a YouTube video URL or id")

// ParseVideoID extracts the video id from an 11-character id or a
// YouTube URL: watch?v=, youtu.be/, /shorts/, /embed/, /live/ or /v/.
func ParseVideoID(input string) (string, error) {
	s := strings.TrimSpace(input)
	if videoIDPattern.MatchString(s) {
		return s, nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || !youTubeHosts[strings.ToLower(u.Hostname())] {
		return "", ErrNotAVideo
	}
	var id string
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case strings.EqualFold(u.Hostname(), "youtu.be"):
		id = parts[0]
	case len(parts) == 1 && parts[0] == "watch":
		id = u.Query().Get("v")
	case len(parts) >= 2 && (parts[0] == "shorts" || parts[0] == "embed" || parts[0] == "live" || parts[0] == "v"):
		id = parts[1]
	}
	if !videoIDPattern.MatchString(id) {
		return "", ErrNotAVideo
	}
	return id, nil
}

// trackedVideo reads a tracked video of the tenant.
func (h *AnalyticsAPI) trackedVideo(ctx context.Context, tenantID uuid.UUID, videoID string) (dbgen.AnalyticsTrackedVideo, bool, error) {
	tv, err := h.Queries.GetTrackedVideo(ctx, dbgen.GetTrackedVideoParams{TenantID: idconv.ToPg(tenantID), YoutubeVideoID: videoID})
	if errors.Is(err, pgx.ErrNoRows) {
		return tv, false, nil
	}
	if err != nil {
		return tv, false, fmt.Errorf("analyticsapi: read tracked video: %w", err)
	}
	return tv, true, nil
}

// TrackAnalyticsVideo implements gen.StrictServerInterface: it looks the
// video up with the Data API, checks it belongs to the channel, tracks it
// and queues a sync so its metrics arrive soon.
func (h *AnalyticsAPI) TrackAnalyticsVideo(ctx context.Context, request gen.TrackAnalyticsVideoRequestObject) (gen.TrackAnalyticsVideoResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	body := request.Body
	videoID, err := ParseVideoID(body.Video)
	if err != nil {
		return gen.TrackAnalyticsVideo400ApplicationProblemPlusJSONResponse{AnalyticsBadRequestApplicationProblemPlusJSONResponse: badRequest(err.Error())}, nil
	}
	ch, ok, err := h.channel(ctx, info.ID, body.ChannelId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.TrackAnalyticsVideo404ApplicationProblemPlusJSONResponse{AnalyticsChannelNotFoundApplicationProblemPlusJSONResponse: channelNotFound()}, nil
	}
	reconnect := gen.TrackAnalyticsVideo409ApplicationProblemPlusJSONResponse(problem(409, "channel not connected",
		"connect the channel again in Settings > YouTube"))
	if ch.Status != "connected" {
		return reconnect, nil
	}
	clients, err := h.Clients.ForChannel(ctx, info.ID, body.ChannelId)
	if youtube.IsKind(err, youtube.KindAuth) {
		return reconnect, nil
	}
	if err != nil {
		return nil, err
	}
	found, err := clients.Data.Videos(ctx, []string{videoID})
	switch {
	case youtube.IsKind(err, youtube.KindAuth):
		return reconnect, nil
	case err != nil:
		slog.WarnContext(ctx, "analyticsapi: video lookup failed", "video", videoID, "error", err)
		return gen.TrackAnalyticsVideo502ApplicationProblemPlusJSONResponse(problem(502, "YouTube lookup failed", lookupDetail(err))), nil
	case len(found) == 0:
		return gen.TrackAnalyticsVideo422ApplicationProblemPlusJSONResponse(problem(422, "video not found",
			"YouTube has no public or unlisted video with this id")), nil
	case found[0].ChannelID != ch.YoutubeChannelID:
		return gen.TrackAnalyticsVideo422ApplicationProblemPlusJSONResponse(problem(422, "video of another channel",
			"the video belongs to another YouTube channel; only the channel's own videos can be tracked")), nil
	}
	v := found[0]
	params := dbgen.UpsertTrackedVideoParams{
		TenantID: idconv.ToPg(info.ID), ChannelID: idconv.ToPg(body.ChannelId), YoutubeVideoID: videoID,
		Source: "manual", Title: v.Title,
		DurationSeconds: pgtype.Int4{Int32: int32(v.DurationSeconds), Valid: v.DurationSeconds > 0},
		PublishedAt:     pgtype.Timestamptz{Time: v.PublishedAt, Valid: !v.PublishedAt.IsZero()},
	}
	if u := sessionUser(ctx); u != nil {
		params.AddedBy = idconv.ToPg(*u)
	}
	row, err := h.Queries.UpsertTrackedVideo(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.TrackAnalyticsVideo409ApplicationProblemPlusJSONResponse(problem(409, "tracked on another channel",
			"this video is already tracked on another channel of this workspace")), nil
	}
	if err != nil {
		return nil, fmt.Errorf("analyticsapi: track video: %w", err)
	}
	if h.Jobs != nil {
		if _, err := h.Jobs.Insert(ctx, analytics.SyncArgs{TenantID: info.ID, ChannelID: body.ChannelId}, nil); err != nil {
			slog.WarnContext(ctx, "analyticsapi: queue sync after tracking", "error", err)
		}
	}
	return gen.TrackAnalyticsVideo201JSONResponse(toTrackedVideo(row)), nil
}

// lookupDetail is the user-facing reason of a failed lookup.
func lookupDetail(err error) string {
	if youtube.IsKind(err, youtube.KindQuota) {
		return "today's YouTube Data API quota is spent; try again after midnight Pacific time"
	}
	return "YouTube did not answer the video lookup; try again later"
}

// UntrackAnalyticsVideo implements gen.StrictServerInterface. Videos
// published by this app are always tracked.
func (h *AnalyticsAPI) UntrackAnalyticsVideo(ctx context.Context, request gen.UntrackAnalyticsVideoRequestObject) (gen.UntrackAnalyticsVideoResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tv, ok, err := h.trackedVideo(ctx, info.ID, request.VideoId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.UntrackAnalyticsVideo404ApplicationProblemPlusJSONResponse{AnalyticsVideoNotFoundApplicationProblemPlusJSONResponse: videoNotFound()}, nil
	}
	if tv.Source != "manual" {
		return gen.UntrackAnalyticsVideo409ApplicationProblemPlusJSONResponse(problem(409, "published by this app",
			"videos published by this app are always tracked")), nil
	}
	if _, err := h.Queries.DeleteTrackedVideo(ctx, dbgen.DeleteTrackedVideoParams{TenantID: idconv.ToPg(info.ID), YoutubeVideoID: request.VideoId}); err != nil {
		return nil, fmt.Errorf("analyticsapi: untrack video: %w", err)
	}
	return gen.UntrackAnalyticsVideo204Response{}, nil
}

func toTrackedVideo(r dbgen.AnalyticsTrackedVideo) gen.TrackedVideo {
	out := gen.TrackedVideo{
		VideoId: r.YoutubeVideoID, ChannelId: idconv.FromPg(r.ChannelID), Title: r.Title,
		Source: gen.TrackedVideoSource(r.Source), CreatedAt: idconv.FromPgTimestamptz(r.CreatedAt),
	}
	if r.DurationSeconds.Valid {
		d := int(r.DurationSeconds.Int32)
		out.DurationSeconds = &d
	}
	if r.PublishedAt.Valid {
		t := r.PublishedAt.Time
		out.PublishedAt = &t
	}
	return out
}
