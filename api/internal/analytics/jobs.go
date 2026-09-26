package analytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/youtube"
)

// QueueAnalytics is the River queue the sync jobs run on. It is its own
// queue so a process without the analytics workers never claims them.
const QueueAnalytics = "analytics"

// Job kinds.
const (
	KindSync    = "analytics.sync"
	KindSyncAll = "analytics.sync_all"
)

const (
	// syncTimeout bounds one channel's sync (a first backfill of a few
	// hundred videos plus months of reach reports).
	syncTimeout = 20 * time.Minute
	// quotaSnoozeSlack waits a little past the Pacific quota reset.
	quotaSnoozeSlack = 5 * time.Minute
)

// SyncArgs syncs one channel. The ids are re-checked against the
// database by the sync itself (tenant-scoped queries), never trusted to
// grant access.
type SyncArgs struct {
	TenantID  uuid.UUID `json:"tenant_id"`
	ChannelID uuid.UUID `json:"channel_id"`
}

// Kind implements river.JobArgs.
func (SyncArgs) Kind() string { return KindSync }

// InsertOpts puts the job on the analytics queue and keeps at most one
// waiting or running sync per channel ("Sync now" twice is one sync).
func (SyncArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueAnalytics,
		MaxAttempts: 5,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
				rivertype.JobStateRetryable, rivertype.JobStateScheduled,
			},
		},
	}
}

// SyncAllArgs fans the daily sync out to every connected channel.
type SyncAllArgs struct{}

// Kind implements river.JobArgs.
func (SyncAllArgs) Kind() string { return KindSyncAll }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (SyncAllArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAnalytics, MaxAttempts: 3}
}

// SyncWorker runs SyncArgs.
type SyncWorker struct {
	river.WorkerDefaults[SyncArgs]
	Syncer *Syncer
}

// Timeout implements river.Worker.
func (w *SyncWorker) Timeout(*river.Job[SyncArgs]) time.Duration { return syncTimeout }

// Work implements river.Worker. Quota exhaustion snoozes until the
// Pacific reset, a dead grant cancels (the channel is flagged
// reconnect_needed), anything else is retried with River's backoff.
func (w *SyncWorker) Work(ctx context.Context, job *river.Job[SyncArgs]) error {
	err := w.Syncer.SyncChannel(ctx, job.Args.TenantID, job.Args.ChannelID)
	return jobOutcome(err, w.Syncer.now())
}

// jobOutcome maps a sync error to what River should do with the job.
func jobOutcome(err error, now time.Time) error {
	switch {
	case err == nil, errors.Is(err, ErrSyncRunning):
		return nil
	case youtube.IsKind(err, youtube.KindQuota):
		return river.JobSnooze(youtube.NextReset(now).Sub(now) + quotaSnoozeSlack)
	case youtube.IsKind(err, youtube.KindAuth):
		return river.JobCancel(err)
	}
	return err
}

// SyncAllWorker enqueues one SyncArgs job per connected channel.
type SyncAllWorker struct {
	river.WorkerDefaults[SyncAllArgs]
	Queries *dbgen.Queries
}

// Work implements river.Worker.
func (w *SyncAllWorker) Work(ctx context.Context, _ *river.Job[SyncAllArgs]) error {
	channels, err := w.Queries.ListChannelsForAnalyticsSync(ctx)
	if err != nil {
		return fmt.Errorf("analytics: list channels: %w", err)
	}
	if len(channels) == 0 {
		return nil
	}
	params := make([]river.InsertManyParams, len(channels))
	for i, c := range channels {
		params[i] = river.InsertManyParams{Args: SyncArgs{TenantID: idconv.FromPg(c.TenantID), ChannelID: idconv.FromPg(c.ID)}}
	}
	client := river.ClientFromContext[pgx.Tx](ctx)
	if _, err := client.InsertMany(ctx, params); err != nil {
		return fmt.Errorf("analytics: enqueue channel syncs: %w", err)
	}
	slog.InfoContext(ctx, "analytics: daily sync enqueued", "channels", len(channels))
	return nil
}

// dailyAt fires once a day at a wall-clock hour in loc.
type dailyAt struct {
	hour int
	loc  *time.Location
}

// Next implements river.PeriodicSchedule.
func (d dailyAt) Next(t time.Time) time.Time {
	lt := t.In(d.loc)
	next := time.Date(lt.Year(), lt.Month(), lt.Day(), d.hour, 0, 0, 0, d.loc)
	if !next.After(t) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// bangkok is Asia/Bangkok (UTC+7, no daylight saving), fixed so the
// schedule needs no tz database.
var bangkok = time.FixedZone("ICT", 7*60*60)

// DailySchedule runs at 09:00 Asia/Bangkok (18:00 or 19:00 of the
// previous evening in Pacific time), when the Pacific day before that has
// long closed and its first figures are in.
var DailySchedule river.PeriodicSchedule = dailyAt{hour: 9, loc: bangkok}

// PeriodicJobs are the analytics jobs to register with the River client.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(DailySchedule, func() (river.JobArgs, *river.InsertOpts) {
			return SyncAllArgs{}, nil
		}, &river.PeriodicJobOpts{ID: KindSyncAll}),
	}
}

// AddWorkers registers the analytics workers.
func AddWorkers(workers *river.Workers, s *Syncer) {
	river.AddWorker(workers, &SyncWorker{Syncer: s})
	river.AddWorker(workers, &SyncAllWorker{Queries: s.Queries})
}
