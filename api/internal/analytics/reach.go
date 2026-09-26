package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/youtube"
)

// reachBatchRows bounds one upsert statement's jsonb payload.
const reachBatchRows = 5000

// ErrNoReachReportType means the channel is offered no reach report type.
var ErrNoReachReportType = errors.New("analytics: the reporting api offers no reach report type for this channel")

// syncReach makes sure the reach reporting job exists, then ingests every
// report generated since the last one seen, oldest first.
func (s *Syncer) syncReach(ctx context.Context, ref channelRef, ac *youtube.AnalyticsClient) error {
	job, err := s.ensureReachJob(ctx, ref, ac)
	if err != nil {
		return err
	}
	reports, err := ac.Reports(ctx, job.JobID, job.LastReportCreatedAt.Time)
	if err != nil {
		return err
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].CreateTime.Before(reports[j].CreateTime) })
	for _, r := range reports {
		if err := s.ingestReachReport(ctx, ref, ac, job.JobID, r); err != nil {
			return fmt.Errorf("report %s: %w", r.ID, err)
		}
		if err := s.Queries.AdvanceReportingJobCursor(ctx, dbgen.AdvanceReportingJobCursorParams{
			CreatedAt: pgtype.Timestamptz{Time: r.CreateTime, Valid: true},
			TenantID:  ref.tenant, ChannelID: ref.channel, ReportTypeID: job.ReportTypeID,
		}); err != nil {
			return fmt.Errorf("analytics: advance reach cursor: %w", err)
		}
	}
	return nil
}

// ensureReachJob returns the stored reach job, creating it on Google's
// side (or adopting one that already exists there) on first use.
func (s *Syncer) ensureReachJob(ctx context.Context, ref channelRef, ac *youtube.AnalyticsClient) (dbgen.AnalyticsReportingJob, error) {
	types, err := ac.ReportTypes(ctx)
	if err != nil {
		return dbgen.AnalyticsReportingJob{}, err
	}
	cands := make([]ReportTypeCandidate, len(types))
	for i, t := range types {
		cands[i] = ReportTypeCandidate{ID: t.ID, Deprecated: t.DeprecateTime != ""}
	}
	typeID, ok := PickReachReportType(cands)
	if !ok {
		return dbgen.AnalyticsReportingJob{}, ErrNoReachReportType
	}
	job, err := s.Queries.GetReportingJob(ctx, dbgen.GetReportingJobParams{TenantID: ref.tenant, ChannelID: ref.channel, ReportTypeID: typeID})
	if err == nil {
		return job, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return job, fmt.Errorf("analytics: read reach job: %w", err)
	}

	jobs, err := ac.Jobs(ctx)
	if err != nil {
		return job, err
	}
	var remote youtube.ReportingJob
	for _, j := range jobs {
		if j.ReportTypeID == typeID {
			remote = j
			break
		}
	}
	if remote.ID == "" {
		if remote, err = ac.CreateJob(ctx, typeID, ReachJobName); err != nil {
			return job, err
		}
	}
	job, err = s.Queries.InsertReportingJob(ctx, dbgen.InsertReportingJobParams{
		TenantID: ref.tenant, ChannelID: ref.channel, ReportTypeID: typeID, JobID: remote.ID,
	})
	if err != nil {
		return job, fmt.Errorf("analytics: store reach job: %w", err)
	}
	return job, nil
}

// ingestReachReport downloads, parses and stores one report. Claiming
// the report id and writing its rows share a transaction, so a report
// listed again is skipped and a failed write can be retried.
func (s *Syncer) ingestReachReport(ctx context.Context, ref channelRef, ac *youtube.AnalyticsClient, jobID string, r youtube.BulkReport) error {
	body, err := ac.Download(ctx, r)
	if err != nil {
		return err
	}
	parsed, err := ParseReachCSV(body)
	closeErr := body.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("analytics: close reach report: %w", closeErr)
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("analytics: begin reach report: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)
	_, err = q.ClaimReportingReport(ctx, dbgen.ClaimReportingReportParams{
		TenantID: ref.tenant, ChannelID: ref.channel, JobID: jobID, ReportID: r.ID,
		StartTime:    pgtype.Timestamptz{Time: r.StartTime, Valid: true},
		EndTime:      pgtype.Timestamptz{Time: r.EndTime, Valid: true},
		RowsIngested: int32(min(len(parsed.Rows), 1<<31-1)), //nolint:gosec // bounded above
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("analytics: claim reach report: %w", err)
	}
	for start := 0; start < len(parsed.Rows); start += reachBatchRows {
		rows, err := json.Marshal(parsed.Rows[start:min(start+reachBatchRows, len(parsed.Rows))])
		if err != nil {
			return err
		}
		if err := q.UpsertVideoReachDaily(ctx, dbgen.UpsertVideoReachDailyParams{
			TenantID: ref.tenant, ChannelID: ref.channel, Rows: rows,
		}); err != nil {
			return fmt.Errorf("analytics: store reach rows: %w", err)
		}
	}
	return tx.Commit(ctx)
}
