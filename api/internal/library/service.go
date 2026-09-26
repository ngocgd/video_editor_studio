// Package library lists a tenant's stored assets and storage use, and
// deletes what nothing needs any more: cached render segments unused for
// the segment TTL and unselected takes older than the take TTL, either
// daily (library.ttl_cleanup) or on demand after a dry run
// (library.cleanup). Nothing referenced by a pinned manifest, a render,
// a selected take or a character is ever a candidate.
package library

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"loomtale/api/internal/audit"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// Default retention, used until a tenant saves its own.
const (
	DefaultSegmentTTLDays = 14
	DefaultTakeTTLDays    = 30
)

// Page sizes.
const (
	DefaultPageSize = 100
	MaxPageSize     = 500
	// PreviewLimit caps each candidate list of one dry run; a bigger
	// backlog is cleaned over several runs (or by the daily cleanup).
	PreviewLimit = 1000
)

// ErrPreviewChanged means the candidates changed since the dry run the
// caller confirmed; it must preview again.
var ErrPreviewChanged = errors.New("library: cleanup candidates changed since the preview")

// Service reads the library and starts cleanups.
type Service struct {
	Queries *dbgen.Queries
	Engine  *pipeline.Engine
}

// Settings is a tenant's retention.
type Settings struct {
	SegmentTTLDays int
	TakeTTLDays    int
	LastCleanupAt  *time.Time
}

// Settings returns the tenant's retention, or the defaults.
func (s *Service) Settings(ctx context.Context, tenantID uuid.UUID) (Settings, error) {
	return loadSettings(ctx, s.Queries, tenantID)
}

func loadSettings(ctx context.Context, q *dbgen.Queries, tenantID uuid.UUID) (Settings, error) {
	row, err := q.GetLibrarySettings(ctx, idconv.ToPg(tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{SegmentTTLDays: DefaultSegmentTTLDays, TakeTTLDays: DefaultTakeTTLDays}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	return settingsFromRow(row), nil
}

func settingsFromRow(row dbgen.LibrarySetting) Settings {
	s := Settings{SegmentTTLDays: int(row.SegmentTtlDays), TakeTTLDays: int(row.TakeTtlDays)}
	if row.LastCleanupAt.Valid {
		t := row.LastCleanupAt.Time
		s.LastCleanupAt = &t
	}
	return s
}

// UpdateSettings saves the tenant's retention (1..365 days each).
func (s *Service) UpdateSettings(ctx context.Context, tenantID uuid.UUID, segmentDays, takeDays int) (Settings, error) {
	if segmentDays < 1 || segmentDays > 365 || takeDays < 1 || takeDays > 365 {
		return Settings{}, fmt.Errorf("%w: retention must be between 1 and 365 days", pipeline.ErrValidation)
	}
	row, err := s.Queries.UpsertLibrarySettings(ctx, dbgen.UpsertLibrarySettingsParams{
		TenantID: idconv.ToPg(tenantID), SegmentTtlDays: int32(segmentDays), TakeTtlDays: int32(takeDays), //nolint:gosec // range-checked above
	})
	if err != nil {
		return Settings{}, err
	}
	return settingsFromRow(row), nil
}

// ListFilter narrows and pages the asset table.
type ListFilter struct {
	Kind     string
	SeriesID *uuid.UUID
	// Before is the cursor: the last id of the previous page.
	Before *uuid.UUID
	Limit  int
}

// Page is one page of assets and the cursor of the next (nil at the end).
type Page struct {
	Items []dbgen.ListLibraryAssetsRow
	Next  *uuid.UUID
}

// List returns one page of the tenant's assets, newest first.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, f ListFilter) (Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	limit = min(limit, MaxPageSize)
	params := dbgen.ListLibraryAssetsParams{
		TenantID: idconv.ToPg(tenantID), SeriesID: idconv.ToPgPtr(f.SeriesID), BeforeID: idconv.ToPgPtr(f.Before),
		MaxRows: int32(limit), //nolint:gosec // capped at MaxPageSize
	}
	if f.Kind != "" {
		params.Kind = pgtype.Text{String: f.Kind, Valid: true}
	}
	rows, err := s.Queries.ListLibraryAssets(ctx, params)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: rows}
	if len(rows) == limit {
		next := idconv.FromPg(rows[len(rows)-1].ID)
		page.Next = &next
	}
	return page, nil
}

// Usage is storage use per project plus the total.
type Usage struct {
	Series     []dbgen.LibraryUsageBySeriesRow
	TotalBytes int64
	Assets     int64
}

// Usage sums the tenant's ready assets per project.
func (s *Service) Usage(ctx context.Context, tenantID uuid.UUID) (Usage, error) {
	rows, err := s.Queries.LibraryUsageBySeries(ctx, idconv.ToPg(tenantID))
	if err != nil {
		return Usage{}, err
	}
	u := Usage{Series: rows}
	for _, r := range rows {
		u.TotalBytes += r.Bytes
		u.Assets += r.Assets
	}
	return u, nil
}

// Actor is who confirmed a manual cleanup, for the audit entry.
type Actor struct {
	UserID     *uuid.UUID
	Email      string
	RemoteAddr string
	UserAgent  string
}

// Confirm starts the cleanup a dry run showed: it recomputes the
// candidates, refuses with ErrPreviewChanged unless they still match
// token, enqueues a library.cleanup run limited to exactly those
// candidates, and audits it. The step re-checks every candidate when it
// runs and skips any that gained a reference since.
func (s *Service) Confirm(ctx context.Context, tenantID uuid.UUID, token string, actor Actor) (uuid.UUID, Preview, error) {
	p, err := s.Preview(ctx, tenantID)
	if err != nil {
		return uuid.Nil, Preview{}, err
	}
	if token == "" || p.Token != token {
		return uuid.Nil, Preview{}, ErrPreviewChanged
	}
	if len(p.Segments) == 0 && len(p.Takes) == 0 {
		return uuid.Nil, p, fmt.Errorf("%w: nothing to clean up", pipeline.ErrValidation)
	}
	in := CleanupInput{Token: p.Token}
	for _, c := range p.Segments {
		in.SegmentHashes = append(in.SegmentHashes, c.ID)
	}
	for _, c := range p.Takes {
		in.TakeIDs = append(in.TakeIDs, c.ID)
	}
	runID, err := s.enqueue(ctx, tenantID, KindCleanup, in, actor.UserID)
	if err != nil {
		return uuid.Nil, Preview{}, err
	}
	if err := audit.Record(ctx, s.Queries, audit.Entry{
		TenantID: &tenantID, ActorUserID: actor.UserID, ActorEmail: actor.Email,
		Action: "library.cleanup", TargetType: "pipeline_run", TargetID: runID.String(),
		Metadata: map[string]any{
			"segments": len(p.Segments), "takes": len(p.Takes), "bytes": p.Bytes,
			"segmentTtlDays": p.Settings.SegmentTTLDays, "takeTtlDays": p.Settings.TakeTTLDays,
		},
		RemoteAddr: actor.RemoteAddr, UserAgent: actor.UserAgent,
	}); err != nil {
		// An unaudited cleanup must not run.
		_ = s.Engine.CancelRun(context.WithoutCancel(ctx), tenantID, runID)
		return uuid.Nil, Preview{}, fmt.Errorf("library: audit cleanup: %w", err)
	}
	return runID, p, nil
}

// enqueue starts a one-step cleanup run scoped to the tenant.
func (s *Service) enqueue(ctx context.Context, tenantID uuid.UUID, kind string, in CleanupInput, createdBy *uuid.UUID) (uuid.UUID, error) {
	if in.Token == "" {
		in.Token = idconv.NewV7().String()
	}
	raw, err := jsonInput(in)
	if err != nil {
		return uuid.Nil, err
	}
	runID := idconv.NewV7()
	_, err = s.Engine.Enqueue(ctx, tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: ScopeTenant, ScopeID: tenantID, Kind: kind, CreatedBy: createdBy,
		Steps: []pipeline.StepSpec{{
			ID: idconv.NewV7(), Kind: kind, ScopeKind: ScopeTenant, ScopeID: tenantID,
			Priority: pipeline.PriorityBatch, Input: raw,
		}},
	})
	return runID, err
}
