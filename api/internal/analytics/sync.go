package analytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/youtube"
)

// staleSyncAfter lets a new sync take over a channel whose previous sync
// is still marked running this long after it started (a crashed worker).
const staleSyncAfter = time.Hour

// ErrSyncRunning means another sync of the channel is in progress.
var ErrSyncRunning = errors.New("analytics: a sync of this channel is already running")

// ErrChannelNotConnected means the channel was disconnected, needs a
// reconnect or no longer exists, so there is nothing to sync.
var ErrChannelNotConnected = errors.New("analytics: the channel is not connected")

// maxSyncErrorLen is the analytics_sync_state.last_error CHECK bound.
const maxSyncErrorLen = 500

// Syncer pulls one channel's Analytics API metrics and reach reports into
// Postgres. Every write is an upsert keyed by day (or a claimed report
// id), so a retried or overlapping window never double counts.
type Syncer struct {
	Pool    *pgxpool.Pool
	Queries *dbgen.Queries
	Clients ClientFactory
	// Now defaults to time.Now.
	Now func() time.Time
	// Concurrency bounds parallel per-video queries (default 4).
	Concurrency int
}

func (s *Syncer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// channelRef is the tenant-scoped key of the channel being synced.
type channelRef struct {
	tenant, channel pgtype.UUID
}

// SyncChannel runs both sources for one channel and records the outcome
// in analytics_sync_state. A dead grant marks the channel
// reconnect_needed. Failures of the reach report or the channel counters
// are recorded as notes and do not fail the Analytics API sync.
func (s *Syncer) SyncChannel(ctx context.Context, tenantID, channelID uuid.UUID) error {
	ref := channelRef{tenant: idconv.ToPg(tenantID), channel: idconv.ToPg(channelID)}
	// A job queued before the user disconnected the channel must not
	// touch it: its grant is gone and the disconnect is deliberate.
	ch, err := s.Queries.GetYouTubeChannel(ctx, dbgen.GetYouTubeChannelParams{TenantID: ref.tenant, ID: ref.channel})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ch.Status != "connected") {
		return ErrChannelNotConnected
	}
	if err != nil {
		return fmt.Errorf("analytics: read channel: %w", err)
	}
	cursor, err := s.cursor(ctx, ref)
	if err != nil {
		return err
	}
	_, err = s.Queries.StartAnalyticsSync(ctx, dbgen.StartAnalyticsSyncParams{
		TenantID: ref.tenant, ChannelID: ref.channel,
		StaleAfter: pgtype.Interval{Microseconds: staleSyncAfter.Microseconds(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSyncRunning
	}
	if err != nil {
		return fmt.Errorf("analytics: start sync: %w", err)
	}

	clients, err := s.Clients.ForChannel(ctx, tenantID, channelID)
	var res syncResult
	if err == nil {
		res, err = s.run(ctx, ref, cursor, clients)
	}
	if err != nil {
		s.fail(ctx, ref, err)
		return err
	}
	finish := dbgen.FinishAnalyticsSyncParams{
		TenantID: ref.tenant, ChannelID: ref.channel,
		AnalyticsThrough: pgtype.Date{Time: res.through, Valid: true},
		LastError:        truncateText(strings.Join(res.notes, "; "), maxSyncErrorLen),
	}
	if res.subscribers != nil {
		finish.SubscriberCount = pgtype.Int8{Int64: *res.subscribers, Valid: true}
	}
	if t, err := s.Queries.ReachDataThrough(ctx, dbgen.ReachDataThroughParams{TenantID: ref.tenant, ChannelID: ref.channel}); err == nil {
		finish.ReachThrough = t
	}
	if err := s.Queries.FinishAnalyticsSync(ctx, finish); err != nil {
		// Never leave the state 'running': that would block every sync
		// until it goes stale.
		err = fmt.Errorf("analytics: finish sync: %w", err)
		s.fail(ctx, ref, err)
		return err
	}
	// Suggestions read the fresh totals; a failure keeps the previous
	// suggestions and is retried by the next sync.
	agg := &Aggregator{Pool: s.Pool, Queries: s.Queries, Now: s.Now}
	if _, err := agg.RecomputeSuggestions(ctx, tenantID, channelID); err != nil {
		slog.WarnContext(ctx, "analytics: recompute suggestions", "error", err)
	}
	return nil
}

// cursor is the last day a previous sync completed, nil before the first.
func (s *Syncer) cursor(ctx context.Context, ref channelRef) (*time.Time, error) {
	st, err := s.Queries.GetAnalyticsSyncState(ctx, dbgen.GetAnalyticsSyncStateParams{TenantID: ref.tenant, ChannelID: ref.channel})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !st.AnalyticsThrough.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: read sync state: %w", err)
	}
	t := st.AnalyticsThrough.Time
	return &t, nil
}

// fail records a failed sync; a dead grant also flags a still-connected
// channel (a disconnect that raced the sync is kept).
func (s *Syncer) fail(ctx context.Context, ref channelRef, cause error) {
	msg := cause.Error()
	if youtube.IsKind(cause, youtube.KindAuth) {
		msg = youtube.ReasonReconnectNeeded
		if _, err := s.Queries.FlagChannelReconnectNeeded(ctx, dbgen.FlagChannelReconnectNeededParams{
			TenantID: ref.tenant, ID: ref.channel,
		}); err != nil {
			slog.WarnContext(ctx, "analytics: flag channel for reconnect", "error", err)
		}
	}
	if err := s.Queries.FailAnalyticsSync(ctx, dbgen.FailAnalyticsSyncParams{
		TenantID: ref.tenant, ChannelID: ref.channel, LastError: truncateText(msg, maxReasonLen),
	}); err != nil {
		slog.WarnContext(ctx, "analytics: record failed sync", "error", err)
	}
}

// syncResult is what a successful run stores in the sync state.
type syncResult struct {
	through     time.Time
	subscribers *int64
	notes       []string
}

// run syncs channel totals, tracked videos, the subscriber counter and
// the reach report, in that order.
func (s *Syncer) run(ctx context.Context, ref channelRef, cursor *time.Time, c Clients) (syncResult, error) {
	end := LastCompleteDay(s.now())
	res := syncResult{through: end}
	if w, ok := ChannelWindow(cursor, end); ok {
		if err := s.syncChannelDaily(ctx, ref, c.Analytics, w); err != nil {
			return res, err
		}
	}
	if err := s.syncVideos(ctx, ref, c.Analytics, cursor, end); err != nil {
		return res, err
	}

	stats, err := c.Data.MyChannelStatistics(ctx)
	switch {
	case youtube.IsKind(err, youtube.KindAuth):
		return res, err
	case err != nil:
		res.notes = append(res.notes, "channel statistics: "+noteOf(err))
	case !stats.SubscribersHidden:
		res.subscribers = &stats.SubscriberCount
	}

	if err := s.syncReach(ctx, ref, c.Analytics); err != nil {
		if youtube.IsKind(err, youtube.KindAuth) {
			return res, err
		}
		res.notes = append(res.notes, "reach report: "+noteOf(err))
	}
	return res, nil
}

// noteOf shortens an error for the sync state's last_error.
func noteOf(err error) string {
	return truncateText(err.Error(), maxReasonLen)
}
